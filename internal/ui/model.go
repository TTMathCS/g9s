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
	screenOverview
	screenResources
	screenDetail
	screenHelp
)

// allKind is a synthetic kind that merges every real kind into one table.
//
// It is not a Lister: there is nothing extra to fetch, because it is assembled
// from what the real listers already cached. The columns are deliberately the
// four fields every Resource carries regardless of kind — anything richer
// would have to be per-kind, which is what the individual tables are for.
var allKind = gcp.Kind{
	ID:    "all",
	Title: "All Resources",
	Columns: []gcp.Column{
		{Title: "KIND", Width: 3},
		{Title: "NAME", Width: 6},
		{Title: "LOCATION", Width: 4},
		{Title: "STATUS", Width: 3},
	},
}

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

	// overview dashboard
	ovCursor int
	// helpReturn remembers which screen opened help, so esc goes back rather
	// than dumping the user somewhere they were not.
	helpReturn screen

	// active selection
	active    config.Project
	hasActive bool

	// resource table
	kindIdx int
	cache   map[string]gcp.Result
	loading map[string]bool
	loadErr map[string]error
	cursor  int
	// refreshToken is per kind: refreshing one kind must not invalidate the
	// in-flight fetches of the others, or their results arrive stale and the
	// dashboard shows loading… forever.
	refreshToken map[string]int

	// filtering
	filter    textinput.Model
	filtering bool

	// command mode (`:`)
	command    textinput.Model
	commanding bool

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

	command := textinput.New()
	command.Prompt = ":"
	command.Placeholder = "vm · gke · gcs · dataproc · composer · all · projects · q"
	command.CharLimit = 40

	return Model{
		cfg:          cfg,
		auth:         mgr,
		listers:      gcp.Listers(),
		screen:       screenProjects,
		authStatus:   map[string]auth.Status{},
		cache:        map[string]gcp.Result{},
		loading:      map[string]bool{},
		loadErr:      map[string]error{},
		refreshToken: map[string]int{},
		filter:       filter,
		command:      command,
		detail:       viewport.New(0, 0),
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

// tabs are the selectable kinds: every lister, then the merged view.
func (m Model) tabs() []gcp.Kind {
	out := make([]gcp.Kind, 0, len(m.listers)+1)
	for _, l := range m.listers {
		out = append(out, l.Kind())
	}
	return append(out, allKind)
}

// allTabIdx is the index of the merged view in tabs().
func (m Model) allTabIdx() int { return len(m.listers) }

// onAllTab reports whether the merged view is selected.
func (m Model) onAllTab() bool { return m.kindIdx == m.allTabIdx() }

// currentLister returns the lister backing the active tab. The merged tab has
// none, which is why the second return value exists.
func (m Model) currentLister() (gcp.Lister, bool) {
	if m.kindIdx < 0 || m.kindIdx >= len(m.listers) {
		return nil, false
	}
	return m.listers[m.kindIdx], true
}

func (m Model) currentKind() gcp.Kind {
	tabs := m.tabs()
	if m.kindIdx < 0 || m.kindIdx >= len(tabs) {
		return tabs[0]
	}
	return tabs[m.kindIdx]
}

// mergedResources flattens every loaded kind into one list, in lister order,
// re-shaping each row to allKind's columns. Name, Location, Status and Raw are
// carried through untouched, so describe, open, yank and ssh keep working from
// the merged table exactly as they do from a per-kind one.
func (m Model) mergedResources() []gcp.Resource {
	out := make([]gcp.Resource, 0, 64)
	for _, l := range m.listers {
		kind := l.Kind()
		for _, r := range m.cache[kind.ID].Resources {
			merged := r
			merged.Row = []string{kind.Title, r.Name, r.Location, r.Status}
			out = append(out, merged)
		}
	}
	return out
}

// resourcesFor returns the rows backing a tab, before filtering.
func (m Model) resourcesFor(kindID string) []gcp.Resource {
	if kindID == allKind.ID {
		return m.mergedResources()
	}
	return m.cache[kindID].Resources
}

// visibleResources applies the filter to the active tab's rows.
func (m Model) visibleResources() []gcp.Resource {
	resources := m.resourcesFor(m.currentKind().ID)
	query := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if query == "" {
		return resources
	}

	out := make([]gcp.Resource, 0, len(resources))
	for _, r := range resources {
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
		if m.hasActive && msg.project == m.active.Name && msg.status.Valid() {
			switch m.screen {
			case screenOverview:
				return m.loadMissing()
			case screenResources:
				if !m.hasData(m.currentKind().ID) {
					return m.loadCurrentIfEmpty()
				}
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
	// Ignore results from a superseded refresh of this kind or a project we
	// have left. Tokens are per kind, so refreshing one kind cannot strand
	// another kind's in-flight result.
	if msg.token != m.refreshToken[msg.kind] || !m.hasActive || msg.project != m.active.Name {
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
	// The merged table grows as each kind lands, so its cursor needs clamping
	// on every arrival, not just when the kind IDs match.
	if msg.kind == m.currentKind().ID || m.onAllTab() {
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

	// The command line swallows keys while it has focus.
	if m.commanding {
		switch msg.String() {
		case "enter":
			m.commanding = false
			m.command.Blur()
			line := strings.TrimSpace(m.command.Value())
			m.command.SetValue("")
			return m.runCommand(line)
		case "esc":
			m.commanding = false
			m.command.Blur()
			m.command.SetValue("")
			return m, nil
		}
		var cmd tea.Cmd
		m.command, cmd = m.command.Update(msg)
		return m, cmd
	}

	// Global keys.
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case ":":
		m.commanding = true
		m.command.Focus()
		return m, textinput.Blink
	case "?":
		if m.screen == screenHelp {
			m.screen = m.homeScreen()
		} else {
			m.helpReturn = m.screen
			m.screen = screenHelp
		}
		return m, nil
	}

	switch m.screen {
	case screenProjects:
		return m.handleProjectsKey(msg)
	case screenOverview:
		return m.handleOverviewKey(msg)
	case screenResources:
		return m.handleResourcesKey(msg)
	case screenDetail:
		return m.handleDetailKey(msg)
	case screenHelp:
		if msg.String() == "esc" || msg.String() == "q" {
			m.screen = m.homeScreen()
		}
		return m, nil
	}
	return m, nil
}

// homeScreen is where esc lands from a modal screen: back where help was
// opened from when that is known, otherwise the most sensible default.
func (m Model) homeScreen() screen {
	switch {
	case m.helpReturn == screenResources && m.hasActive:
		return screenResources
	case m.hasActive:
		return screenOverview
	default:
		return screenProjects
	}
}

// runCommand executes a `:` command, k9s style: a kind name jumps straight to
// its table, `all` to the merged view, `projects` back to the picker.
func (m Model) runCommand(line string) (tea.Model, tea.Cmd) {
	switch line {
	case "":
		return m, nil
	case "q", "quit":
		m.quitting = true
		return m, tea.Quit
	case "help":
		m.helpReturn = m.screen
		m.screen = screenHelp
		return m, nil
	case "proj", "projects":
		m.screen = screenProjects
		return m, nil
	}

	idx, ok := m.matchKind(line)
	if !ok {
		return m, flash(fmt.Sprintf("unknown command %q — a kind (vm, gke, gcs…), all, projects, help or q", line), flashWarn)
	}
	if !m.hasActive {
		return m, flash("select a project first", flashWarn)
	}
	m.screen = screenOverview // openTab drills in from the dashboard level
	return m.openTab(idx)
}

// matchKind resolves a command word to a tab: exact kind ID first, then a
// prefix of the ID or title, in display order.
func (m Model) matchKind(word string) (int, bool) {
	word = strings.ToLower(word)
	tabs := m.tabs()
	for i, k := range tabs {
		if strings.ToLower(k.ID) == word {
			return i, true
		}
	}
	for i, k := range tabs {
		if strings.HasPrefix(strings.ToLower(k.ID), word) ||
			strings.HasPrefix(strings.ToLower(k.Title), word) {
			return i, true
		}
	}
	return -1, false
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
	m.screen = screenOverview
	m.cursor = 0
	m.ovCursor = 0
	m.filter.SetValue("")

	// The dashboard is only useful if every category is populated, so selecting
	// a project fans out across all of them at once rather than lazily.
	return m.loadAll()
}

func (m Model) startLogin(noBrowser bool) (tea.Model, tea.Cmd) {
	p := m.cfg.Projects[m.projCursor]
	if m.hasActive && (m.screen == screenResources || m.screen == screenOverview) {
		p = m.active
	}
	return m, login(m.auth, p, noBrowser)
}

// handleOverviewKey drives the dashboard. Its whole job is to get you into a
// category, so every key that picks one also opens it.
func (m Model) handleOverviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	tabs := m.tabs()

	switch msg.String() {
	// q backs up a level, k9s style; quitting is ctrl+c, :q, or q from the
	// project list.
	case "q", "esc", "p":
		m.screen = screenProjects
		return m, nil

	case "up", "k":
		if m.ovCursor > 0 {
			m.ovCursor--
		}
		return m, nil

	case "down", "j":
		if m.ovCursor < len(tabs)-1 {
			m.ovCursor++
		}
		return m, nil

	case "g":
		m.ovCursor = 0
		return m, nil

	case "G":
		m.ovCursor = len(tabs) - 1
		return m, nil

	case "a", "0":
		// 0 is the k9s reflex for "everything at once".
		return m.openTab(m.allTabIdx())

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(msg.String()[0] - '1')
		if idx < len(m.listers) {
			return m.openTab(idx)
		}
		return m, nil

	case "enter":
		return m.openTab(m.ovCursor)

	case "r":
		return m.loadAll()

	case "l":
		return m.startLogin(false)

	case "L":
		return m.startLogin(true)
	}
	return m, nil
}

// openTab drills from the dashboard into one category's table.
func (m Model) openTab(idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(m.tabs()) {
		return m, nil
	}
	m.kindIdx = idx
	m.ovCursor = idx
	m.cursor = 0
	m.filter.SetValue("")
	m.screen = screenResources
	return m.loadCurrentIfEmpty()
}

func (m Model) handleResourcesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	visible := m.visibleResources()

	switch msg.String() {
	case "q", "esc":
		// Back up one level rather than all the way out: the dashboard is
		// where you came from and where you pick the next category.
		m.screen = screenOverview
		m.ovCursor = m.kindIdx
		return m, nil

	case "p":
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
		m.kindIdx = (m.kindIdx + 1) % len(m.tabs())
		m.cursor = 0
		return m.loadCurrentIfEmpty()

	case "shift+tab", "[":
		m.kindIdx = (m.kindIdx - 1 + len(m.tabs())) % len(m.tabs())
		m.cursor = 0
		return m.loadCurrentIfEmpty()

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(msg.String()[0] - '1')
		if idx < len(m.listers) {
			m.kindIdx = idx
			m.cursor = 0
			return m.loadCurrentIfEmpty()
		}
		return m, nil

	case "a", "0":
		m.kindIdx = m.allTabIdx()
		m.cursor = 0
		return m.loadCurrentIfEmpty()

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

	// d is describe, the k9s reflex; enter does the same because it is the
	// natural "show me this row" key.
	case "enter", "d":
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

// loadCurrent forces a refresh of the active kind. On the merged tab, where
// there is no single lister to refresh, it reloads everything.
func (m Model) loadCurrent() (tea.Model, tea.Cmd) {
	if !m.hasActive {
		return m, nil
	}
	if !m.credentialsUsable() {
		return m, flash("credentials expired — press l to log in again", flashWarn)
	}

	lister, ok := m.currentLister()
	if !ok {
		return m.loadAll()
	}

	return m, m.startLoad(lister)
}

// startLoad marks one kind loading and returns its fetch command, bumping that
// kind's token so any older in-flight fetch of the same kind is superseded.
func (m Model) startLoad(l gcp.Lister) tea.Cmd {
	id := l.Kind().ID
	m.refreshToken[id]++
	m.loading[id] = true
	delete(m.loadErr, id)
	return listResources(m.cfg, m.auth, m.active, l, m.refreshToken[id])
}

// loadAll refreshes every kind concurrently.
func (m Model) loadAll() (tea.Model, tea.Cmd) {
	if !m.hasActive {
		return m, nil
	}
	if !m.credentialsUsable() {
		return m, flash("credentials expired — press l to log in again", flashWarn)
	}

	cmds := make([]tea.Cmd, 0, len(m.listers))
	for _, l := range m.listers {
		cmds = append(cmds, m.startLoad(l))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) credentialsUsable() bool {
	status, ok := m.authStatus[m.active.Name]
	return ok && status.Valid()
}

// loadCurrentIfEmpty fetches only what is missing, so tabbing between kinds is
// instant after the first visit. On the merged tab that means filling in any
// kind still unloaded rather than refetching the ones already cached.
func (m Model) loadCurrentIfEmpty() (tea.Model, tea.Cmd) {
	if m.onAllTab() {
		return m.loadMissing()
	}
	kind := m.currentKind().ID
	if m.hasData(kind) || m.loading[kind] {
		return m, nil
	}
	return m.loadCurrent()
}

// loadMissing fetches the kinds that have neither data nor a load in flight.
func (m Model) loadMissing() (tea.Model, tea.Cmd) {
	if !m.hasActive || !m.credentialsUsable() {
		return m, nil
	}

	var cmds []tea.Cmd
	for _, l := range m.listers {
		id := l.Kind().ID
		if m.hasData(id) || m.loading[id] {
			continue
		}
		cmds = append(cmds, m.startLoad(l))
	}
	if cmds == nil {
		return m, nil
	}
	return m, tea.Batch(cmds...)
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
