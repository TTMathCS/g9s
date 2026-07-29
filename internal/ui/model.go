package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

type screen int

const (
	screenProjects screen = iota
	screenResources
	screenDetail
	screenHelp
)

// Model is the root bubbletea model.
type Model struct {
	cfg     *config.Config
	auth    *auth.Manager
	listers []gcp.Lister

	screen        screen
	width, height int
	quitting      bool

	// project picker
	projCursor int
	authStatus map[string]auth.Status

	// active selection
	active    config.Project
	hasActive bool

	// resource table
	kindIdx      int
	cache        map[string]gcp.Result
	loading      map[string]bool
	loadErr      map[string]error
	cursor       int
	refreshToken int

	// filtering
	filter    textinput.Model
	filtering bool

	// detail pane
	detail viewport.Model

	// transient status line
	flashText  string
	flashLevel flashLevel
	flashID    int
}

// New builds the initial model.
func New(cfg *config.Config, mgr *auth.Manager) Model {
	filter := textinput.New()
	filter.Prompt = "/"
	filter.Placeholder = "filter"
	filter.CharLimit = 80

	return Model{
		cfg:        cfg,
		auth:       mgr,
		listers:    gcp.Listers(),
		screen:     screenProjects,
		authStatus: map[string]auth.Status{},
		cache:      map[string]gcp.Result{},
		loading:    map[string]bool{},
		loadErr:    map[string]error{},
		filter:     filter,
		detail:     viewport.New(0, 0),
	}
}

func (m Model) Init() tea.Cmd {
	// Check every project up front so the picker shows real state rather than
	// making the user select one to discover it is expired.
	cmds := make([]tea.Cmd, 0, len(m.cfg.Projects))
	for _, p := range m.cfg.Projects {
		cmds = append(cmds, checkAuth(m.auth, p))
	}
	return tea.Batch(cmds...)
}

func (m Model) currentLister() gcp.Lister {
	return m.listers[m.kindIdx]
}

func (m Model) currentKind() gcp.Kind {
	return m.currentLister().Kind()
}

// visibleResources applies the filter to the active kind's cached results.
func (m Model) visibleResources() []gcp.Resource {
	result := m.cache[m.currentKind().ID]
	query := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if query == "" {
		return result.Resources
	}

	out := make([]gcp.Resource, 0, len(result.Resources))
	for _, r := range result.Resources {
		if strings.Contains(strings.ToLower(strings.Join(r.Row, " ")), query) {
			out = append(out, r)
		}
	}
	return out
}

func (m Model) selectedResource() (gcp.Resource, bool) {
	visible := m.visibleResources()
	if m.cursor < 0 || m.cursor >= len(visible) {
		return gcp.Resource{}, false
	}
	return visible[m.cursor], true
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.detail.Width = msg.Width - 4
		m.detail.Height = m.bodyHeight()
		return m, nil

	case authCheckedMsg:
		m.authStatus[msg.project] = msg.status
		// A refresh triggered from the resources screen should flow straight
		// into a load once the credentials come back healthy.
		if m.hasActive && msg.project == m.active.Name && msg.status.Valid() && m.screen == screenResources {
			if !m.hasData(m.currentKind().ID) {
				return m.loadCurrent()
			}
		}
		return m, nil

	case resourcesMsg:
		return m.handleResources(msg)

	case loginFinishedMsg:
		return m.handleLoginFinished(msg)

	case flashMsg:
		m.flashID++
		m.flashText, m.flashLevel = msg.text, msg.level
		return m, clearFlashAfter(m.flashID, 5*time.Second)

	case clearFlashMsg:
		if msg.id == m.flashID {
			m.flashText = ""
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleResources(msg resourcesMsg) (tea.Model, tea.Cmd) {
	// Ignore results from a superseded refresh or a project we have left.
	if msg.token != m.refreshToken || !m.hasActive || msg.project != m.active.Name {
		return m, nil
	}

	m.loading[msg.kind] = false
	if msg.err != nil {
		m.loadErr[msg.kind] = msg.err
		// Credentials dying mid-session is the expected failure with a
		// federated IdP, so re-check rather than just showing an error.
		return m, tea.Batch(
			flash(fmt.Sprintf("%s: %v", msg.kind, msg.err), flashError),
			checkAuth(m.auth, m.active),
		)
	}

	delete(m.loadErr, msg.kind)
	m.cache[msg.kind] = msg.result
	if msg.kind == m.currentKind().ID {
		m.clampCursor()
	}
	return m, nil
}

func (m Model) handleLoginFinished(msg loginFinishedMsg) (tea.Model, tea.Cmd) {
	p, ok := m.cfg.Project(msg.project)
	if !ok {
		return m, nil
	}
	if msg.err != nil {
		return m, flash("login failed: "+msg.err.Error(), flashError)
	}
	// Drop anything fetched with the old identity.
	if m.hasActive && m.active.Name == msg.project {
		m.cache = map[string]gcp.Result{}
		m.loadErr = map[string]error{}
	}
	return m, tea.Batch(checkAuth(m.auth, p), flash("login complete for "+msg.project, flashInfo))
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The filter input swallows keys while it has focus.
	if m.filtering {
		switch msg.String() {
		case "enter":
			m.filtering = false
			m.filter.Blur()
			m.clampCursor()
			return m, nil
		case "esc":
			m.filtering = false
			m.filter.Blur()
			m.filter.SetValue("")
			m.clampCursor()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.cursor = 0
		return m, cmd
	}

	// Global keys.
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "?":
		if m.screen == screenHelp {
			m.screen = screenResources
			if !m.hasActive {
				m.screen = screenProjects
			}
		} else {
			m.screen = screenHelp
		}
		return m, nil
	}

	switch m.screen {
	case screenProjects:
		return m.handleProjectsKey(msg)
	case screenResources:
		return m.handleResourcesKey(msg)
	case screenDetail:
		return m.handleDetailKey(msg)
	case screenHelp:
		if msg.String() == "esc" || msg.String() == "q" {
			m.screen = screenProjects
			if m.hasActive {
				m.screen = screenResources
			}
		}
		return m, nil
	}
	return m, nil
}

func (m Model) handleProjectsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.quitting = true
		return m, tea.Quit

	case "up", "k":
		if m.projCursor > 0 {
			m.projCursor--
		}
		return m, nil

	case "down", "j":
		if m.projCursor < len(m.cfg.Projects)-1 {
			m.projCursor++
		}
		return m, nil

	case "g":
		m.projCursor = 0
		return m, nil

	case "G":
		m.projCursor = len(m.cfg.Projects) - 1
		return m, nil

	case "l":
		return m.startLogin(false)

	case "L":
		// Terminal is not on the machine with the browser.
		return m.startLogin(true)

	case "r":
		p := m.cfg.Projects[m.projCursor]
		return m, tea.Batch(checkAuth(m.auth, p), flash("re-checking "+p.Name, flashInfo))

	case "enter":
		return m.selectProject()
	}
	return m, nil
}

func (m Model) selectProject() (tea.Model, tea.Cmd) {
	p := m.cfg.Projects[m.projCursor]

	if status, ok := m.authStatus[p.Name]; !ok || !status.Valid() {
		return m, flash(fmt.Sprintf("%s is %s — press l to log in", p.Name, statusWord(status, ok)), flashWarn)
	}

	// Switching projects invalidates everything fetched for the previous one.
	if !m.hasActive || m.active.Name != p.Name {
		m.cache = map[string]gcp.Result{}
		m.loadErr = map[string]error{}
		m.loading = map[string]bool{}
		m.kindIdx = 0
	}
	m.active, m.hasActive = p, true
	m.screen = screenResources
	m.cursor = 0
	m.filter.SetValue("")

	return m.loadCurrent()
}

func (m Model) startLogin(noBrowser bool) (tea.Model, tea.Cmd) {
	p := m.cfg.Projects[m.projCursor]
	if m.hasActive && m.screen == screenResources {
		p = m.active
	}
	return m, login(m.auth, p, noBrowser)
}

func (m Model) handleResourcesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	visible := m.visibleResources()

	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit

	case "esc", "p":
		m.screen = screenProjects
		return m, nil

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		if m.cursor < len(visible)-1 {
			m.cursor++
		}
		return m, nil

	case "g":
		m.cursor = 0
		return m, nil

	case "G":
		m.cursor = max(0, len(visible)-1)
		return m, nil

	case "tab", "]":
		m.kindIdx = (m.kindIdx + 1) % len(m.listers)
		m.cursor = 0
		return m.loadCurrentIfEmpty()

	case "shift+tab", "[":
		m.kindIdx = (m.kindIdx - 1 + len(m.listers)) % len(m.listers)
		m.cursor = 0
		return m.loadCurrentIfEmpty()

	case "1", "2", "3", "4", "5":
		idx := int(msg.String()[0] - '1')
		if idx < len(m.listers) {
			m.kindIdx = idx
			m.cursor = 0
			return m.loadCurrentIfEmpty()
		}
		return m, nil

	case "/":
		m.filtering = true
		m.filter.Focus()
		return m, textinput.Blink

	case "r":
		return m.loadCurrent()

	case "l":
		return m.startLogin(false)

	case "L":
		return m.startLogin(true)

	case "enter":
		r, ok := m.selectedResource()
		if !ok {
			return m, nil
		}
		m.detail.Width = m.width - 4
		m.detail.Height = m.bodyHeight()
		m.detail.SetContent(renderDetail(r))
		m.detail.GotoTop()
		m.screen = screenDetail
		return m, nil

	case "o":
		r, ok := m.selectedResource()
		if !ok {
			return m, nil
		}
		// For Composer the Airflow UI is what an operator actually wants.
		if uri, isComposer := gcp.AirflowURI(r); isComposer {
			return m, openURL(uri)
		}
		return m, openURL(r.ConsoleURL)

	case "c":
		r, ok := m.selectedResource()
		if !ok {
			return m, nil
		}
		return m, openURL(r.ConsoleURL)

	case "y":
		r, ok := m.selectedResource()
		if !ok {
			return m, nil
		}
		return m, copyToClipboard(r.Name)

	case "s":
		return m.startSSH()
	}
	return m, nil
}

func (m Model) startSSH() (tea.Model, tea.Cmd) {
	r, ok := m.selectedResource()
	if !ok {
		return m, nil
	}
	name, zone, sshable := gcp.SSHTarget(r)
	if !sshable {
		return m, flash("ssh is only available for running VM instances", flashWarn)
	}
	return m, sshTo(m.auth, m.active, name, zone)
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "enter":
		m.screen = screenResources
		return m, nil
	case "y":
		if r, ok := m.selectedResource(); ok {
			return m, copyToClipboard(renderDetail(r))
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.detail, cmd = m.detail.Update(msg)
	return m, cmd
}

// loadCurrent forces a refresh of the active kind.
func (m Model) loadCurrent() (tea.Model, tea.Cmd) {
	if !m.hasActive {
		return m, nil
	}
	if status, ok := m.authStatus[m.active.Name]; !ok || !status.Valid() {
		return m, flash("credentials expired — press l to log in again", flashWarn)
	}

	lister := m.currentLister()
	m.refreshToken++
	m.loading[lister.Kind().ID] = true
	delete(m.loadErr, lister.Kind().ID)

	return m, listResources(m.cfg, m.auth, m.active, lister, m.refreshToken)
}

// loadCurrentIfEmpty fetches only when the kind has not been loaded yet, so
// tabbing between kinds is instant after the first visit.
func (m Model) loadCurrentIfEmpty() (tea.Model, tea.Cmd) {
	kind := m.currentKind().ID
	if m.hasData(kind) || m.loading[kind] {
		return m, nil
	}
	return m.loadCurrent()
}

func (m Model) hasData(kind string) bool {
	_, ok := m.cache[kind]
	return ok
}

func (m *Model) clampCursor() {
	n := len(m.visibleResources())
	if m.cursor >= n {
		m.cursor = max(0, n-1)
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func statusWord(s auth.Status, known bool) string {
	if !known {
		return "unchecked"
	}
	return s.State.String()
}
