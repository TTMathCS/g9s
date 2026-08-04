package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/bookmarks"
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
	screenLogin
	screenConfirm
	screenFleet
	screenBookmarks
	screenTerraform
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

// drillState is one open drill-down: the child listing, the row it was opened
// from, and enough of the parent table to put the cursor back where it was on
// the way out.
type drillState struct {
	lister gcp.Lister // the child bound to parent, so it loads like any kind
	parent gcp.Resource
	// siblings are every drill-down the parent row offers, and which of them
	// is open. A Cloud SQL instance holds databases and users, and neither is
	// underneath the other — so tab moves between them here exactly as it moves
	// between kinds on the table screen, and the trail shows them as tabs.
	siblings []gcp.ChildLister
	// boundSiblings preserves listing-specific state while tabbing between
	// siblings. Storage Objects is the first stateful child: its current prefix
	// and glob must not reset merely because Lifecycle was inspected.
	boundSiblings []gcp.Lister
	siblingIdx    int
	// parentKind is the tab the drill was opened from, named rather than
	// indexed so the breadcrumb reads right even from the merged table.
	parentKind gcp.Kind
	// parentCursor is the row the drill was opened on. esc puts it back.
	parentCursor int
	parentFilter string
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
	// drill is the child listing open on top of the current tab, or nil.
	// Drilling in does not leave the resources screen — it swaps which listing
	// the screen is showing — so every route through the table (filter, cursor,
	// describe, yank, refresh, the error and loading states) works on a
	// drill-down without knowing one is open.
	drill   *drillState
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

	// detail pane. detailRes is the resource the pane was opened on, kept
	// because the table underneath keeps moving: a fetch landing while the pane
	// is open reorders rows, and reading the row back out of the table would
	// leave the header naming one resource while the body describes another.
	detail    viewport.Model
	detailRes gcp.Resource
	hasDetail bool

	// help pane. A viewport because the panel is longer than a short terminal is
	// tall: it scrolls — a line on j/k, half a page on space — rather than
	// pushing the footer off the screen.
	help viewport.Model

	// assisted login. assisted is nil between logins and while gcloud is still
	// starting; loginProject names the project so the screen can say whose
	// login this is before gcloud is up; loginReturn is where esc goes back to.
	// loginSeq counts login attempts, so a gcloud started for an attempt the
	// user already left is recognised as stale and killed rather than adopted.
	assisted     *auth.AssistedLogin
	loginProject string
	loginReturn  screen
	loginSeq     int
	loginInput   textinput.Model

	// pending is the action waiting for confirmation, or nil. Everything the
	// action needs is captured in it when the user asks, so what is confirmed
	// and what is executed cannot drift apart while the table refreshes.
	pending       *pendingAction
	confirmInput  textinput.Model
	confirmReturn screen

	// fleet is the cross-project sweep, or nil. fleetReturn is where esc goes.
	fleet       *fleetState
	fleetReturn screen

	// marks is the saved-places file. bookmarkReturn is where esc goes, and
	// bookmarkCursor is kept separate from the table cursor so opening the
	// list and backing out does not move the table underneath.
	marks          *bookmarks.Store
	bookmarkReturn screen
	bookmarkCursor int

	// tf is the Terraform overlay, or nil. tfRows is the table it was opened
	// on, captured at that moment: the overlay marks the rows somebody was
	// looking at, and a refresh landing underneath must not change which rows
	// the verdict applies to.
	tf       *tfState
	tfRows   []gcp.Resource
	tfReturn screen

	// cacheGen counts mutations of cache. It is the validity key for rows
	// below: anything derived from the cache is stale the moment this moves.
	cacheGen int
	// rows memoises the assembled and filtered table.
	//
	// View runs on every keystroke, and it used to rebuild the merged table
	// from scratch each time — concatenating every kind's rows, rewriting a
	// Row slice for each, then lowercasing every cell to apply the filter. On
	// a real estate that was tens of thousands of allocations per frame to
	// display the forty-odd rows that fit on screen, and typing into the
	// filter visibly lagged. A pointer rather than a value because bubbletea
	// copies the model constantly; the copies share one cache, which is safe
	// because the key fully determines the contents.
	rows *rowCache

	// transient status line
	flashText  string
	flashLevel flashLevel
	flashID    int
}

// rowCache holds the table rows derived from the resource cache.
//
// Two levels, because they are invalidated by different things. base changes
// only when the underlying listings do; visible changes on every keystroke
// into the filter. Splitting them means typing a query scans precomputed
// lowercase text instead of rebuilding and re-lowercasing the whole table.
type rowCache struct {
	// baseGen and baseKind are what base was built from.
	baseGen  int
	baseKind string
	base     []gcp.Resource
	// haystack[i] is base[i]'s searchable text, lowercased once so filtering
	// allocates nothing.
	haystack []string
	baseOK   bool

	// filter is the query visible was computed for.
	filter    string
	visible   []gcp.Resource
	visibleOK bool
}

// New builds the initial model.
func New(cfg *config.Config, mgr *auth.Manager) Model {
	filter := textinput.New()
	filter.Prompt = "/"
	filter.Placeholder = "filter"
	filter.CharLimit = 80

	command := textinput.New()
	command.Prompt = ":"
	command.Placeholder = "kind · fleet KIND · diff KIND · bm NAME · export csv|json · cd PATH · find GLOB · all · projects · help · q"
	// Cloud Storage glob expressions can be up to 1,024 bytes. The gcp layer
	// validates bytes; this rune limit merely keeps the input bounded.
	command.CharLimit = 1024

	loginInput := textinput.New()
	loginInput.Prompt = "> "
	loginInput.Placeholder = "http://localhost:8085/?state=…&code=…"
	// A redirect URL with state and code runs a few hundred bytes; 4 KiB is
	// bounded without ever truncating a real one mid-paste.
	loginInput.CharLimit = 4096

	confirmInput := textinput.New()
	confirmInput.Prompt = "> "
	// A GCP instance name is at most 63 characters, so anything longer is not
	// a name being retyped.
	confirmInput.CharLimit = 63

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
		loginInput:   loginInput,
		confirmInput: confirmInput,
		rows:         &rowCache{},
		detail:       viewport.New(0, 0),
		help:         viewport.New(0, 0),
		// Read at startup rather than on first use, so a file g9s cannot parse
		// is a message on the bookmark screen instead of an empty list that
		// looks like nothing was ever saved.
		marks: bookmarks.Load(bookmarks.PathFor(cfg.Path())),
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

// onAllTab reports whether the merged view is selected. A drill-down is its own
// listing, never the merged one, even when it was opened from that table.
func (m Model) onAllTab() bool { return m.drill == nil && m.kindIdx == m.allTabIdx() }

// currentLister returns the lister backing what the table is showing. The
// merged tab has none, which is why the second return value exists.
func (m Model) currentLister() (gcp.Lister, bool) {
	if m.drill != nil {
		return m.drill.lister, true
	}
	if m.kindIdx < 0 || m.kindIdx >= len(m.listers) {
		return nil, false
	}
	return m.listers[m.kindIdx], true
}

func (m Model) currentKind() gcp.Kind {
	if m.drill != nil {
		return m.drill.lister.Kind()
	}
	tabs := m.tabs()
	if m.kindIdx < 0 || m.kindIdx >= len(tabs) {
		return tabs[0]
	}
	return tabs[m.kindIdx]
}

// childrenFor returns the drill-downs available from a row.
//
// The kind comes from the row rather than the tab so enter behaves the same on
// the merged table as on the kind's own — a GKE cluster is a GKE cluster
// wherever it is listed, and a key whose meaning depends on how you got to the
// row is a key nobody trusts.
func (m Model) childrenFor(r gcp.Resource) []gcp.ChildLister {
	if m.drill != nil {
		// Registered resource relationships stay one level deep. Storage folder
		// navigation is query state inside its existing child listing, handled
		// before this function; it does not manufacture a ChildLister for every
		// arbitrary path component.
		return nil
	}
	id := m.currentKind().ID
	if id == allKind.ID {
		id = r.KindID
	}
	return gcp.ChildrenOf(id)
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
//
// Memoised against the cache generation, the tab and the query, because View
// calls this on every frame and the merged tab assembles every kind's rows to
// do it. Recomputing that per keystroke was the difference between a table
// that responds and one that stutters.
func (m Model) visibleResources() []gcp.Resource {
	kindID := m.currentKind().ID
	query := strings.ToLower(strings.TrimSpace(m.filter.Value()))

	c := m.rows
	if c == nil {
		// No cache to work with — a model built by something other than New.
		// Correctness does not depend on the cache, only speed.
		return filterResources(m.resourcesFor(kindID), query)
	}

	if !c.baseOK || c.baseGen != m.cacheGen || c.baseKind != kindID {
		c.base = m.resourcesFor(kindID)
		c.haystack = make([]string, len(c.base))
		for i, r := range c.base {
			c.haystack[i] = strings.ToLower(strings.Join(r.Row, " "))
		}
		c.baseGen, c.baseKind, c.baseOK = m.cacheGen, kindID, true
		c.visibleOK = false
	}

	if c.visibleOK && c.filter == query {
		return c.visible
	}

	if query == "" {
		c.visible = c.base
	} else {
		out := make([]gcp.Resource, 0, len(c.base))
		for i, r := range c.base {
			if strings.Contains(c.haystack[i], query) {
				out = append(out, r)
			}
		}
		c.visible = out
	}
	c.filter, c.visibleOK = query, true
	return c.visible
}

// filterResources is the uncached path, kept so the behaviour is defined in
// exactly one place regardless of which route reaches it.
func filterResources(resources []gcp.Resource, query string) []gcp.Resource {
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

// invalidateRows marks everything derived from the resource cache as stale.
// Called wherever cache is mutated; the generation counter is what the
// memoised rows compare against.
func (m *Model) invalidateRows() { m.cacheGen++ }

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
		m.sizeHelp()
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

	case assistedLoginMsg:
		return m.handleAssistedLogin(msg)

	case fleetFinishedMsg:
		return m.handleFleetFinished(msg)

	case tfFinishedMsg:
		return m.handleTerraformFinished(msg)

	case actionFinishedMsg:
		return m.handleActionFinished(msg)

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
		if msg.appendPage {
			// A failed continuation does not invalidate the pages already on
			// screen. Keep them usable and let the user retry instead of replacing
			// a good table with a full-screen error.
			return m, tea.Batch(
				flash(fmt.Sprintf("load more %s: %v", msg.kind, msg.err), flashError),
				checkAuth(m.auth, m.active),
			)
		}
		m.loadErr[msg.kind] = msg.err
		// Credentials dying mid-session is the expected failure with a
		// federated IdP, so re-check rather than just showing an error.
		return m, tea.Batch(
			flash(fmt.Sprintf("%s: %v", msg.kind, msg.err), flashError),
			checkAuth(m.auth, m.active),
		)
	}

	delete(m.loadErr, msg.kind)
	if msg.appendPage {
		current := m.cache[msg.kind]
		current.Resources = append(current.Resources, msg.result.Resources...)
		current.Warnings = appendUniqueWarnings(current.Warnings, msg.result.Warnings)
		current.NextPageToken = msg.result.NextPageToken
		m.cache[msg.kind] = current
		m.invalidateRows()
	} else {
		m.cache[msg.kind] = msg.result
	}
	m.invalidateRows()
	// The merged table grows as each kind lands, so its cursor needs clamping
	// on every arrival, not just when the kind IDs match.
	if msg.kind == m.currentKind().ID || m.onAllTab() {
		m.clampCursor()
	}
	return m, nil
}

// handleAssistedLogin is the assisted flow coming up — or failing to.
func (m Model) handleAssistedLogin(msg assistedLoginMsg) (tea.Model, tea.Cmd) {
	// This start belongs to an attempt nobody is waiting on: the user pressed
	// esc during the "starting gcloud…" moment, or left and started a newer
	// attempt. Adopting it would open a browser with no screen explaining it —
	// or hand attempt two the gcloud from attempt one. Checked before the
	// error branch on purpose: a stale *failure* must not launch the fallback
	// handover for an attempt the user already walked away from.
	if m.screen != screenLogin || msg.seq != m.loginSeq {
		if msg.login != nil {
			msg.login.Cancel()
		}
		return m, nil
	}

	if msg.err != nil {
		// The assisted flow could not even start — a gcloud too old to know
		// --no-launch-browser, or output this code does not recognise. The
		// terminal-handover flow needs neither, so fall back to it rather than
		// stranding the login on a nicety.
		m.screen = m.loginReturn
		p, ok := m.cfg.Project(msg.project)
		if !ok {
			return m, nil
		}
		noBrowser := m.fallbackNoBrowser(p)
		notice := "assisted login unavailable (" + truncate(msg.err.Error(), 70) + ")"
		if noBrowser {
			notice += " — using the no-browser flow"
		} else {
			notice += " — handing the terminal to gcloud"
		}
		return m, tea.Batch(flash(notice, flashWarn), login(m.auth, p, noBrowser))
	}

	m.assisted = msg.login
	m.loginInput.SetValue("")
	m.loginInput.Focus()
	// Three things at once: watch for gcloud to finish, open the browser, and
	// put the cursor in the paste box for the case where the browser cannot
	// finish it alone.
	return m, tea.Batch(awaitAssisted(msg.login), openURL(msg.login.URL()), textinput.Blink)
}

// handleActionFinished reports an action's outcome and refreshes what it
// changed.
//
// The refresh matters as much as the message: an instance that was asked to
// stop takes tens of seconds to get there, and a table still showing RUNNING
// beside a "stop requested" flash is the kind of disagreement that gets read
// as the action having failed.
func (m Model) handleActionFinished(msg actionFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, flash(fmt.Sprintf("%s %s failed: %s",
			strings.ToLower(msg.action.Verb), msg.target.Name,
			truncate(msg.err.Error(), 90)), flashError)
	}

	cmds := []tea.Cmd{flash(fmt.Sprintf("%s requested for %s — the table will catch up on the next refresh",
		strings.ToLower(msg.action.Verb), msg.target.Name), flashInfo)}
	if l, ok := m.currentLister(); ok {
		cmds = append(cmds, m.startLoad(l))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleLoginFinished(msg loginFinishedMsg) (tea.Model, tea.Cmd) {
	// Whatever the outcome, the assisted screen is over. The restore is not
	// conditional on still holding the assisted handle: a finish that arrives
	// after the handle is gone must not strand the UI on the login screen.
	if m.assisted != nil && m.assisted.Project() == msg.project {
		m.assisted = nil
	}
	if m.screen == screenLogin && m.loginProject == msg.project {
		m.loginInput.Blur()
		m.screen = m.loginReturn
	}
	p, ok := m.cfg.Project(msg.project)
	if !ok {
		return m, nil
	}
	if msg.cancelled {
		// Ended on purpose; the failure pane would dress a decision up as a
		// problem.
		return m, flash("login cancelled for "+msg.project, flashInfo)
	}
	if msg.err != nil {
		// A flash is the wrong shape for this. The reason a login failed is
		// several lines long, it is the one thing the user needs to read, and
		// gcloud's own copy of it has already been painted over by the resume.
		// So it goes into the scrollable pane, where it can be read and yanked.
		return m.showLoginFailure(p, msg)
	}
	// A login rewrites the ADC file, so the token source built from the old
	// contents has to go with it. Without this the shared source would keep
	// minting tokens for the identity that was just replaced — every client
	// built afterwards would authenticate as the previous account, which is the
	// one failure the per-project isolation exists to prevent.
	if m.auth != nil {
		m.auth.InvalidateCredentials(msg.project)
	}

	// Drop anything fetched with the old identity, in flight included: a fetch
	// started before the login still carries the old token, and without bumping
	// the refresh tokens its result would land in the freshly cleared cache and
	// be shown as belonging to the new identity.
	if m.hasActive && m.active.Name == msg.project {
		m.invalidate()
	}
	return m, tea.Batch(checkAuth(m.auth, p), flash("login complete for "+msg.project, flashInfo))
}

// invalidate discards every cached listing and supersedes every fetch still in
// flight, so nothing fetched before this point can be presented as current.
func (m *Model) invalidate() {
	// Include child listings already seen in this session. They are not in
	// m.listers, but an in-flight child fetch carries the old identity just as
	// surely as a top-level one does.
	for id := range m.refreshToken {
		m.refreshToken[id]++
	}
	for _, l := range m.listers {
		id := l.Kind().ID
		if _, seen := m.refreshToken[id]; !seen {
			m.refreshToken[id]++
		}
	}
	m.cache = map[string]gcp.Result{}
	m.invalidateRows()
	m.loadErr = map[string]error{}
	m.loading = map[string]bool{}
	m.hasDetail = false
}

func appendUniqueWarnings(existing, more []gcp.Warning) []gcp.Warning {
	if len(more) == 0 {
		return existing
	}
	// Compared by value rather than by rendered text: two warnings that read
	// the same but classify differently are two different facts, and the one
	// that survives the merge should not depend on which page arrived first.
	seen := make(map[gcp.Warning]bool, len(existing)+len(more))
	for _, warning := range existing {
		seen[warning] = true
	}
	for _, warning := range more {
		if seen[warning] {
			continue
		}
		seen[warning] = true
		existing = append(existing, warning)
	}
	return existing
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

	// The login screen swallows keys while it is up: its input takes pasted
	// URLs, which contain `:` and `?`, so the global bindings for those must
	// not fire here.
	if m.screen == screenLogin {
		return m.handleLoginKey(msg)
	}
	// The confirmation owns every key while it is up. Nothing else may fire
	// underneath a prompt about changing production, and `q` must not quit out
	// of one leaving the caller unsure whether it ran.
	if m.screen == screenConfirm {
		return m.handleConfirmKey(msg)
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
			return m, nil
		}
		return m.openHelp()
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
		return m.handleHelpKey(msg)
	case screenFleet:
		return m.handleFleetKey(msg)
	case screenBookmarks:
		return m.handleBookmarkKey(msg)
	case screenTerraform:
		return m.handleTerraformKey(msg)
	}
	return m, nil
}

// openHelp shows the help panel, remembering where it was opened from.
func (m Model) openHelp() (tea.Model, tea.Cmd) {
	m.helpReturn = m.screen
	m.screen = screenHelp
	m.sizeHelp()
	m.help.SetContent(m.helpContent())
	m.help.GotoTop()
	return m, nil
}

// sizeHelp fits the help viewport inside the bordered panel it renders into:
// the border and padding cost two cells on each side and one line top and
// bottom.
func (m *Model) sizeHelp() {
	m.help.Width = max(10, m.width-6)
	m.help.Height = max(3, m.bodyHeight()-2)
}

func (m Model) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.screen = m.homeScreen()
		return m, nil
	}
	// Anything else scrolls: the panel is taller than a short terminal.
	var cmd tea.Cmd
	m.help, cmd = m.help.Update(msg)
	return m, cmd
}

// homeScreen is where esc lands from a modal screen: back where it was opened
// from when that is still reachable, otherwise the most sensible default.
func (m Model) homeScreen() screen {
	switch {
	case m.helpReturn == screenDetail && m.hasDetail:
		return screenDetail
	case m.helpReturn == screenResources && m.hasActive:
		return screenResources
	case m.helpReturn == screenProjects:
		return screenProjects
	case m.hasActive:
		return screenOverview
	default:
		return screenProjects
	}
}

// runCommand executes a `:` command, k9s style: a kind name jumps straight to
// its table, `all` to the merged view, `projects` back to the picker.
func (m Model) runCommand(line string) (tea.Model, tea.Cmd) {
	verb, argument := splitCommand(line)
	if verb == "cd" || verb == "find" {
		return m.runStorageObjectCommand(verb, argument)
	}
	if verb == "export" {
		return m.runExport(argument)
	}
	if verb == "tf" || verb == "terraform" {
		return m.startTerraform()
	}

	// `:bm` with a name saves where you are; without one it opens the list.
	// Two commands would be one more thing to remember for no gain.
	if verb == "bm" || verb == "bookmark" || verb == "bookmarks" {
		if argument == "" {
			return m.openBookmarks()
		}
		return m.saveBookmark(argument)
	}

	// Both read every project once. They differ only in how the one sweep is
	// laid out, and `c` switches between them without fetching again.
	if verb == "fleet" || verb == "diff" {
		if argument == "" {
			return m, flash(fmt.Sprintf("%s needs a kind — for example :%s vm", verb, verb), flashWarn)
		}
		idx, ok := m.matchKind(argument)
		if !ok || idx >= len(m.listers) {
			return m, flash(fmt.Sprintf("unknown kind %q for %s", argument, verb), flashWarn)
		}
		return m.startSweep(m.listers[idx], verb == "diff")
	}
	// Actions are reachable only by typing a word, never by a single key.
	// Every other binding in g9s is one keystroke because nothing it does can
	// be regretted; these can, so beginning one is deliberate by construction
	// rather than by the confirmation alone.
	if action, ok := actionByCommand(verb); ok {
		if !m.hasActive {
			return m, flash("select a project first", flashWarn)
		}
		if m.screen != screenResources {
			return m, flash("open the VM Instances table and select a row first", flashWarn)
		}
		return m.requestAction(action)
	}

	switch line {
	case "":
		return m, nil
	case "q", "quit":
		m.quitting = true
		return m, tea.Quit
	case "help":
		return m.openHelp()
	case "proj", "projects":
		m.discardObjectBrowser()
		m.drill = nil
		m.screen = screenProjects
		return m, nil
	}

	idx, ok := m.matchKind(line)
	if !ok {
		return m, flash(fmt.Sprintf("unknown command %q — a kind id or title prefix, fleet, diff, tf, bm, export, start/stop/reset, cd, find, all, projects, help or q", line), flashWarn)
	}
	if !m.hasActive {
		return m, flash("select a project first", flashWarn)
	}
	return m.openTab(idx)
}

func splitCommand(line string) (string, string) {
	line = strings.TrimSpace(line)
	if split := strings.IndexAny(line, " \t"); split >= 0 {
		return line[:split], strings.TrimSpace(line[split+1:])
	}
	return line, ""
}

func (m Model) runStorageObjectCommand(verb, argument string) (tea.Model, tea.Cmd) {
	if m.screen != screenResources || m.drill == nil {
		return m, flash(fmt.Sprintf(":%s is only available in Storage Objects", verb), flashWarn)
	}
	lister, ok := m.currentLister()
	if !ok {
		return m, flash(fmt.Sprintf(":%s is only available in Storage Objects", verb), flashWarn)
	}
	if _, objectBrowser := gcp.StorageObjectState(lister); !objectBrowser {
		return m, flash(fmt.Sprintf(":%s is only available in Storage Objects", verb), flashWarn)
	}

	var (
		next gcp.Lister
		err  error
	)
	if verb == "cd" {
		next, err = gcp.ChangeStorageObjectPath(lister, argument)
	} else {
		next, err = gcp.FindStorageObjects(lister, argument)
	}
	if err != nil {
		return m, flash(err.Error(), flashWarn)
	}
	return m.replaceDrillLister(next)
}

// matchKind resolves a command word to a tab: exact kind ID, then a prefix of
// an ID, then a prefix of a title — each pass in display order.
//
// IDs beat titles, and that ordering is load-bearing rather than tidy. `:comp`
// is the id of Composer and merely the start of "Compute Disks"; folding both
// into one pass hands it to whichever sits earlier in the display order, which
// is a coin toss decided by an unrelated list. The id is what someone typing a
// short command means, so it is what wins.
func (m Model) matchKind(word string) (int, bool) {
	word = strings.ToLower(word)
	if word == "" {
		// Every string has the empty prefix, so without this the empty word
		// silently means "the first kind". runCommand happens to return early
		// on a blank line today, but that is the caller's habit, not this
		// function's contract.
		return -1, false
	}
	tabs := m.tabs()

	for i, k := range tabs {
		if strings.ToLower(k.ID) == word {
			return i, true
		}
	}
	for i, k := range tabs {
		if strings.HasPrefix(strings.ToLower(k.ID), word) {
			return i, true
		}
	}
	for i, k := range tabs {
		if strings.HasPrefix(strings.ToLower(k.Title), word) {
			return i, true
		}
	}
	return -1, false
}

func (m Model) handleProjectsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Every key below the quit case indexes into the project list. Load
	// rejects an empty one, but New is exported and nothing else here would
	// stop an empty config from panicking on the first arrow key.
	if len(m.cfg.Projects) == 0 && msg.String() != "q" && msg.String() != "esc" {
		return m, nil
	}

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
		m.invalidate()
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
	if len(m.cfg.Projects) == 0 {
		return m, nil
	}
	p := m.cfg.Projects[m.projCursor]
	if m.hasActive && (m.screen == screenResources || m.screen == screenOverview) {
		p = m.active
	}

	// A project reading a credentials file has no login for g9s to run: gcloud
	// would write into the isolated config directory, which is not where the
	// file is. Say where the credentials come from instead of starting a flow
	// that cannot change anything.
	if !m.auth.ManagesCredentials(p) {
		return m, flash(fmt.Sprintf(
			"%s reads credentials_file %s — refresh it with gcloud yourself, then press r",
			p.Name, m.auth.ADCPath(p)), flashWarn)
	}

	// Choose the flow that can actually finish rather than letting the user
	// find out by waiting. gcloud's browser login ends with the browser
	// fetching http://localhost:<port>/ to hand the code back; when the browser
	// is on a different machine that request never arrives, the sign-in
	// succeeds, and the terminal sits on the URL forever with nothing to
	// suggest what went wrong.
	//
	// The config setting comes first because it is the one signal that is not a
	// guess: a browser that proxies localhost breaks the same way, and only the
	// person running g9s knows whether theirs does.
	if m.loginNoBrowser(p) {
		noBrowser = true
	}
	if noBrowser {
		return m, login(m.auth, p, true)
	}

	// The browser flow runs assisted: gcloud as a piped child, the TUI still
	// alive. When the browser can reach localhost this looks exactly like the
	// old flow — sign in, done. When it cannot (a browser that proxies
	// localhost, the corporate default), the login screen offers the rescue:
	// paste the address the browser got stuck on and g9s performs that loopback
	// request itself. The old flow could not be rescued because gcloud owned
	// the terminal while it waited forever.
	if m.assisted != nil {
		return m, flash("a login is already in progress — esc to cancel it", flashWarn)
	}
	m.loginSeq++
	m.loginProject = p.Name
	m.loginReturn = m.screen
	m.screen = screenLogin
	return m, startAssisted(m.auth, p, m.loginSeq)
}

// handleLoginKey drives the assisted login screen.
func (m Model) handleLoginKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.assisted != nil {
			m.assisted.Cancel()
		}
		m.quitting = true
		return m, tea.Quit

	case "esc":
		if m.assisted != nil {
			// The cancelled loginFinishedMsg that follows restores the screen;
			// doing it here as well would race the two.
			m.assisted.Cancel()
			return m, nil
		}
		m.screen = m.loginReturn
		return m, nil

	case "enter":
		if m.assisted == nil {
			return m, nil
		}
		pasted := strings.TrimSpace(m.loginInput.Value())
		if pasted == "" {
			return m, flash("paste the address from the stuck browser tab, then press enter", flashWarn)
		}
		return m, deliverCode(m.assisted, pasted)

	// The typing keys are taken by the input, so the extras live on ctrl.
	case "ctrl+o":
		if m.assisted != nil {
			return m, openURL(m.assisted.URL())
		}
		return m, nil

	case "ctrl+y":
		if m.assisted != nil {
			return m, copyToClipboard(m.assisted.URL(), m.clipboardLimit())
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.loginInput, cmd = m.loginInput.Update(msg)
	return m, cmd
}

// fallbackNoBrowser reports whether the terminal handover, used when the
// assisted flow cannot start, should take the --no-browser path.
//
// Which handover matters more than it looks. The ordinary browser flow ends
// with the browser fetching http://localhost on this machine, and on a host
// where that request is proxied away it does not fail — it waits, forever,
// having already signed in. Falling back to it on exactly the machines the
// assisted flow exists for would return the user to the original bug with no
// way out but ctrl+c. So where there is reason to believe loopback will not
// come back, hand over to --no-browser instead: it needs another machine, but
// it ends.
//
// The proxy hint is only consulted here, not in loginNoBrowser. A proxied
// loopback is no obstacle to the assisted flow — routing around it is what
// that flow is for — so it must not push the ordinary path off the browser
// flow it handles perfectly well.
func (m Model) fallbackNoBrowser(p config.Project) bool {
	return m.loginNoBrowser(p) || auth.ProxyMayBlockLoopback()
}

// loginNoBrowser reports whether a login for this project takes the
// --no-browser path even when the browser flow was asked for. The diagnosis of
// a failure depends on which flow ran, so both callers resolve it the same way.
func (m Model) loginNoBrowser(_ config.Project) bool {
	return m.cfg.Defaults.LoginNoBrowser || !auth.LoopbackUsable()
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

	case "enter":
		return m.openTab(m.ovCursor)

	case "r":
		return m.loadAll()

	case "l":
		return m.startLogin(false)

	case "L":
		return m.startLogin(true)
	}

	// Anything left is a candidate hotkey. Reached last so a kind can never
	// shadow one of the actions above; the two key sets are disjoint by
	// construction, and a test holds them that way.
	if idx, ok := m.tabForKey(msg.String()); ok {
		return m.openTab(idx)
	}
	return m, nil
}

// openTab opens one category's table: from the dashboard, from a hotkey, or by
// cycling. Every route goes through here so switching kinds always means the
// same thing — cursor at the top, no filter carried over from the last kind
// (a query typed for VMs matches nothing in DNS and reads as an empty project),
// and the dashboard cursor left on the kind you are looking at, so esc returns
// to where you were.
func (m Model) openTab(idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(m.tabs()) {
		return m, nil
	}
	// Picking a kind is picking a kind, wherever you were. A drill-down left
	// open underneath would make tab and the hotkeys land somewhere other than
	// the table they name.
	m.discardObjectBrowser()
	m.drill = nil
	m.kindIdx = idx
	m.ovCursor = idx
	m.cursor = 0
	m.filter.SetValue("")
	m.filtering = false
	m.filter.Blur()
	m.screen = screenResources
	return m.loadCurrentIfEmpty()
}

func (m Model) handleResourcesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	visible := m.visibleResources()

	switch msg.String() {
	case "q", "esc":
		// Back up one level rather than all the way out. Inside a drill-down
		// that normally means the table it was opened from. Storage Objects
		// adds real path levels: clear a glob, then walk toward the bucket root,
		// then leave the drill.
		if m.drill != nil {
			if next, moved := gcp.ParentStorageObjectPath(m.drill.lister); moved {
				return m.replaceDrillLister(next)
			}
			return m.closeDrill(), nil
		}
		m.screen = screenOverview
		m.ovCursor = m.kindIdx
		return m, nil

	case "p":
		m.discardObjectBrowser()
		m.drill = nil
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

	// Inside a drill-down with more than one listing, tab moves between those
	// rather than out to the next kind — it is still "the next table across",
	// just at the level you are actually on. With only one listing there is
	// nothing to move to, so tab keeps its usual meaning and leaves the drill.
	case "tab", "]":
		if m.drill != nil && len(m.drill.siblings) > 1 {
			return m.openSibling(m.drill.siblingIdx + 1)
		}
		return m.openTab((m.kindIdx + 1) % len(m.tabs()))

	case "shift+tab", "[":
		if m.drill != nil && len(m.drill.siblings) > 1 {
			return m.openSibling(m.drill.siblingIdx - 1)
		}
		return m.openTab((m.kindIdx - 1 + len(m.tabs())) % len(m.tabs()))

	case "/":
		m.filtering = true
		m.filter.Focus()
		return m, textinput.Blink

	case "r":
		return m.loadCurrent()

	case " ", "space":
		return m.loadNextPage()

	case "l":
		return m.startLogin(false)

	case "L":
		return m.startLogin(true)

	// enter means "go into this", the same as it does on the dashboard: where
	// a row has a listing underneath it, that is what opens. Where it does not
	// — every kind but two — going into a row can only mean describing it, so
	// enter keeps doing that and d is unchanged either way.
	case "enter":
		r, ok := m.selectedResource()
		if !ok {
			return m, nil
		}
		if m.drill != nil {
			if next, opened := gcp.OpenStorageObjectFolder(m.drill.lister, r); opened {
				return m.replaceDrillLister(next)
			}
		}
		if children := m.childrenFor(r); len(children) > 0 {
			return m.openDrill(children, r)
		}
		return m.describe(r)

	// d is describe, the k9s reflex, and always describes — including on a row
	// that has children, where it is the only way to see the parent itself.
	case "d":
		r, ok := m.selectedResource()
		if !ok {
			return m, nil
		}
		return m.describe(r)

	case "o":
		r, ok := m.selectedResource()
		if !ok {
			return m, nil
		}
		// For Composer the Airflow UI is what an operator actually wants; for
		// everything else there is only the Console page. This used to be two
		// keys, `o` and `c`, which did the same thing on every kind but that
		// one — and the alphabet had better uses for the second.
		if uri, isComposer := gcp.AirflowURI(r); isComposer {
			return m, openURL(uri)
		}
		return m, openURL(r.ConsoleURL)

	case "y":
		r, ok := m.selectedResource()
		if !ok {
			return m, nil
		}
		return m, copyToClipboard(r.Name, m.clipboardLimit())

	case "s":
		return m.startSSH()
	}

	// Same as the dashboard: unclaimed keys fall through to the kind hotkeys.
	if idx, ok := m.tabForKey(msg.String()); ok {
		return m.openTab(idx)
	}
	return m, nil
}

// selectedHasChild reports whether enter on the current row drills in.
func (m Model) selectedHasChild() bool {
	return len(m.selectedChildren()) > 0
}

// selectedChildTitle names those listings for the hint line, lowercased and
// joined, so a row offering two says so before you press anything.
func (m Model) selectedChildTitle() string {
	children := m.selectedChildren()
	if len(children) == 0 {
		return "open"
	}
	titles := make([]string, 0, len(children))
	for _, c := range children {
		titles = append(titles, strings.ToLower(c.Kind().Title))
	}
	return strings.Join(titles, " / ")
}

func (m Model) selectedChildren() []gcp.ChildLister {
	r, ok := m.selectedResource()
	if !ok {
		return nil
	}
	return m.childrenFor(r)
}

// showLoginFailure puts the reason a login failed somewhere it can be read.
//
// gcloud's output is included verbatim underneath the diagnosis, because a
// failure this code does not recognise still has to leave the user something
// to act on — an unrecognised error shown in full beats a tidy summary that
// happens to be wrong.
func (m Model) showLoginFailure(p config.Project, msg loginFinishedMsg) (tea.Model, tea.Cmd) {
	var b strings.Builder

	fmt.Fprintf(&b, "Login failed for %s (%s)\n\n", p.Name, p.ProjectID)

	attempt := auth.LoginAttempt{Output: msg.output, NoBrowser: m.loginNoBrowser(p), Err: msg.err}
	if diag, ok := auth.DiagnoseLogin(attempt); ok {
		b.WriteString(diag.Summary + "\n\n")
		for _, line := range diag.Remedy {
			b.WriteString("  " + line + "\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("g9s does not recognise this failure. gcloud's own output is below.\n")
		b.WriteString("`g9s doctor` checks config, gcloud, identity and API access outside the UI.\n\n")
	}

	if msg.err != nil {
		fmt.Fprintf(&b, "gcloud exited with: %v\n\n", msg.err)
	}

	out := strings.TrimSpace(msg.output)
	if out == "" {
		out = "(gcloud produced no output)"
	}
	b.WriteString("--- gcloud output ---\n")
	b.WriteString(out + "\n")

	m.detail.Width = m.width - 4
	m.detail.Height = m.bodyHeight()
	m.detail.SetContent(b.String())
	m.detail.GotoTop()
	// Named so the pane's header says what it is, and so `y` yanks something
	// identifiable when the user pastes it into a ticket.
	m.detailRes = gcp.Resource{Name: "login failed: " + p.Name, Location: p.ProjectID}
	m.hasDetail = true
	m.screen = screenDetail
	return m, nil
}

// describe opens the YAML pane on a row.
func (m Model) describe(r gcp.Resource) (tea.Model, tea.Cmd) {
	m.detail.Width = m.width - 4
	m.detail.Height = m.bodyHeight()
	m.detail.SetContent(renderDetail(r))
	m.detail.GotoTop()
	m.detailRes, m.hasDetail = r, true
	m.screen = screenDetail
	return m, nil
}

// openDrill opens a row's child listing in place of the current table.
//
// The parent's cursor and filter are kept rather than recomputed: coming back
// out to a table that has scrolled somewhere else, or lost the query you were
// working through, makes drilling in feel like losing your place.
func (m Model) openDrill(siblings []gcp.ChildLister, parent gcp.Resource) (tea.Model, tea.Cmd) {
	if len(siblings) == 0 {
		return m, nil
	}
	boundSiblings := make([]gcp.Lister, len(siblings))
	for i, sibling := range siblings {
		boundSiblings[i] = gcp.BindChild(sibling, parent)
	}
	m.drill = &drillState{
		lister:        boundSiblings[0],
		parent:        parent,
		siblings:      siblings,
		boundSiblings: boundSiblings,
		parentKind:    m.currentKind(),
		parentCursor:  m.cursor,
		parentFilter:  m.filter.Value(),
	}
	// Object paths are transient navigation state. Reopening a bucket starts
	// at its root instead of showing whichever folder happened to be cached
	// when the browser was last closed.
	if _, objectBrowser := gcp.StorageObjectState(m.drill.lister); objectBrowser {
		delete(m.cache, m.drill.lister.Kind().ID)
		m.invalidateRows()
		delete(m.loadErr, m.drill.lister.Kind().ID)
	}
	m.cursor = 0
	m.filter.SetValue("")
	m.filtering = false
	m.filter.Blur()
	return m.loadCurrentIfEmpty()
}

// openSibling switches to another of the parent's listings, without leaving the
// drill or losing the way back out.
func (m Model) openSibling(idx int) (tea.Model, tea.Cmd) {
	if m.drill == nil || len(m.drill.siblings) < 2 {
		return m, nil
	}
	n := len(m.drill.siblings)
	idx = ((idx % n) + n) % n

	next := *m.drill
	next.siblingIdx = idx
	if idx < len(next.boundSiblings) && next.boundSiblings[idx] != nil {
		next.lister = next.boundSiblings[idx]
	} else {
		next.lister = gcp.BindChild(next.siblings[idx], next.parent)
	}
	m.drill = &next

	// Same rule as switching kinds: cursor to the top, and the filter goes with
	// the listing it was typed for.
	m.cursor = 0
	m.filter.SetValue("")
	m.filtering = false
	m.filter.Blur()
	return m.loadCurrentIfEmpty()
}

// closeDrill goes back up to the table the drill was opened from.
func (m Model) closeDrill() Model {
	if m.drill == nil {
		return m
	}
	m.cursor = m.drill.parentCursor
	m.filter.SetValue(m.drill.parentFilter)
	m.discardObjectBrowser()
	m.drill = nil
	m.clampCursor()
	return m
}

// replaceDrillLister changes query state inside one child listing. It clears
// the old page before loading so a breadcrumb for /new/path never sits above
// rows fetched for /old/path.
func (m Model) replaceDrillLister(next gcp.Lister) (tea.Model, tea.Cmd) {
	if m.drill == nil || next == nil {
		return m, nil
	}
	d := *m.drill
	d.lister = next
	if d.siblingIdx >= 0 && d.siblingIdx < len(d.boundSiblings) {
		d.boundSiblings[d.siblingIdx] = next
	}
	m.drill = &d

	id := next.Kind().ID
	delete(m.cache, id)
	m.invalidateRows()
	delete(m.loadErr, id)
	m.cursor = 0
	m.filter.SetValue("")
	m.filtering = false
	m.filter.Blur()
	return m.loadCurrent()
}

// discardObjectBrowser drops query-shaped cache entries and supersedes their
// in-flight requests. A child cache key identifies bucket + kind, not prefix;
// retaining it after leaving the drill would let a later root browser reuse
// rows from a deeper path.
func (m *Model) discardObjectBrowser() {
	if m.drill == nil {
		return
	}
	listers := m.drill.boundSiblings
	if len(listers) == 0 {
		listers = []gcp.Lister{m.drill.lister}
	}
	for _, lister := range listers {
		if _, ok := gcp.StorageObjectState(lister); !ok {
			continue
		}
		id := lister.Kind().ID
		m.refreshToken[id]++
		delete(m.cache, id)
		m.invalidateRows()
		delete(m.loadErr, id)
		delete(m.loading, id)
	}
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
		// The pane's own resource, not whatever the cursor now points at: a
		// fetch landing while the pane is open moves the rows underneath it,
		// and copying a different resource from the one on screen is worse
		// than not copying at all.
		if !m.hasDetail {
			return m, nil
		}
		return m, copyToClipboard(renderDetail(m.detailRes), m.clipboardLimit())
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

// loadNextPage appends one explicit continuation page. It is intentionally a
// no-op for ordinary listings: those drain their APIs internally and have no
// continuation token to expose.
func (m Model) loadNextPage() (tea.Model, tea.Cmd) {
	if !m.hasActive || !m.credentialsUsable() {
		return m, nil
	}
	lister, ok := m.currentLister()
	if !ok {
		return m, nil
	}
	id := lister.Kind().ID
	if m.loading[id] {
		return m, nil
	}
	next, ok := gcp.ContinueStorageObjects(lister, m.cache[id].NextPageToken)
	if !ok {
		return m, nil
	}

	m.refreshToken[id]++
	m.loading[id] = true
	delete(m.loadErr, id)
	return m, listResourcePage(m.cfg, m.auth, m.active, next, m.refreshToken[id], true)
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
