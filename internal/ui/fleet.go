package ui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

// fleetKind is the table shape for a cross-project listing.
//
// Four columns, the same four the merged view uses, plus the project. Anything
// richer would have to be per-kind, and a fleet table's job is comparison
// rather than detail: which projects have this, how many, and in what state.
// `enter` on a row goes to that project for the detail.
var fleetKind = gcp.Kind{
	ID:    "fleet",
	Title: "Fleet",
	Columns: []gcp.Column{
		{Title: "PROJECT", Width: 3},
		{Title: "NAME", Width: 5},
		{Title: "LOCATION", Width: 3},
		{Title: "STATUS", Width: 3},
	},
}

// fleetState is one cross-project sweep.
type fleetState struct {
	// lister is the kind being swept.
	lister gcp.Lister
	// result is the last completed sweep, or the zero value while the first
	// one is still running.
	result gcp.FleetResult
	// loading is true while a sweep is in flight.
	loading bool
	// cancel stops the running sweep. Leaving the screen calls it, because a
	// fleet view that goes on costing API calls after you have left is a view
	// people learn not to open.
	cancel context.CancelFunc
	// token supersedes a slow sweep whose result would otherwise land after a
	// newer one, the same guard the per-kind fetches use.
	token int
	// rows is the flattened, sorted result, held so the view does not rebuild
	// it every frame.
	rows []gcp.Resource
}

// fleetFinishedMsg carries a completed sweep.
type fleetFinishedMsg struct {
	token  int
	result gcp.FleetResult
}

// managerAccess adapts the auth manager to what the coordinator needs, and is
// where the decision "can this project be read right now" lives.
//
// A project with no valid credential is skipped rather than attempted. The
// alternative is a wall of authentication errors that say nothing except that
// the user has not logged in, which they already know.
type managerAccess struct {
	mgr    *auth.Manager
	status map[string]auth.Status
}

func (a managerAccess) Usable(p config.Project) (bool, string) {
	status, checked := a.status[p.Name]
	switch {
	case !checked:
		return false, "credentials not checked yet"
	case status.State == auth.StateMissing:
		return false, "not logged in"
	case status.State == auth.StateWrongAccount:
		return false, "credentials are for a different account"
	case !status.Valid():
		return false, "credentials expired"
	}
	return true, ""
}

func (a managerAccess) Options(p config.Project) []option.ClientOption {
	return a.mgr.ClientOptions(p)
}

// startFleet begins a cross-project sweep of one kind.
func (m Model) startFleet(lister gcp.Lister) (tea.Model, tea.Cmd) {
	if len(m.cfg.Projects) == 0 {
		return m, flash("no projects configured", flashWarn)
	}

	// A sweep already running is superseded rather than left to finish: its
	// result is for a kind nobody is looking at any more.
	if m.fleet != nil && m.fleet.cancel != nil {
		m.fleet.cancel()
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.Defaults.ListTimeout.Duration())
	token := 0
	if m.fleet != nil {
		token = m.fleet.token
	}
	token++

	m.fleet = &fleetState{lister: lister, loading: true, cancel: cancel, token: token}
	m.fleetReturn = m.screen
	m.screen = screenFleet
	m.cursor = 0
	m.filter.SetValue("")

	return m, sweepFleet(ctx, m.cfg, m.auth, lister, m.cfg.Projects, m.authStatus, token)
}

// sweepFleet runs the coordinator off the UI goroutine.
func sweepFleet(ctx context.Context, cfg *config.Config, mgr *auth.Manager, lister gcp.Lister,
	projects []config.Project, status map[string]auth.Status, token int) tea.Cmd {

	// The status map is copied because the model keeps mutating its own as
	// credential checks land, and reading it from another goroutine would be a
	// race on a map.
	snapshot := make(map[string]auth.Status, len(status))
	for k, v := range status {
		snapshot[k] = v
	}

	return func() tea.Msg {
		result := gcp.SweepFleet(ctx, cfg, lister, projects, managerAccess{mgr: mgr, status: snapshot})
		return fleetFinishedMsg{token: token, result: result}
	}
}

func (m Model) handleFleetFinished(msg fleetFinishedMsg) (tea.Model, tea.Cmd) {
	if m.fleet == nil || msg.token != m.fleet.token {
		// A superseded sweep. Its rows are for a kind the user has left.
		return m, nil
	}

	rows := msg.result.Resources()
	gcp.SortFleetResources(rows)
	// Reshape each row to the fleet columns. The per-kind Row that came back
	// describes that kind's own table and has a different width; rendering it
	// under these four headings would put values under the wrong ones.
	for i := range rows {
		rows[i].Row = []string{
			rows[i].Project, rows[i].Name, rows[i].Location, rows[i].Status,
		}
	}

	m.fleet.result = msg.result
	m.fleet.rows = rows
	m.fleet.loading = false
	m.clampCursor()
	return m, nil
}

// handleFleetKey drives the fleet table.
func (m Model) handleFleetKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "p":
		return m.leaveFleet()

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		if m.cursor < len(m.visibleFleetRows())-1 {
			m.cursor++
		}
		return m, nil

	case "g":
		m.cursor = 0
		return m, nil

	case "G":
		m.cursor = max(0, len(m.visibleFleetRows())-1)
		return m, nil

	case "r":
		if m.fleet != nil {
			return m.startFleet(m.fleet.lister)
		}
		return m, nil

	case "d":
		if r, ok := m.selectedFleetRow(); ok {
			return m.describe(r)
		}
		return m, nil

	case "y":
		if r, ok := m.selectedFleetRow(); ok {
			return m, copyToClipboard(r.Name, m.clipboardLimit())
		}
		return m, nil

	case "enter":
		// Into the project the row came from, on the kind being swept. The
		// fleet table is for comparison; anything more than four columns about
		// one resource belongs in that project's own table.
		return m.enterFleetRow()
	}
	return m, nil
}

// leaveFleet cancels the sweep on the way out.
func (m Model) leaveFleet() (tea.Model, tea.Cmd) {
	if m.fleet != nil && m.fleet.cancel != nil {
		m.fleet.cancel()
	}
	m.fleet = nil
	m.screen = m.fleetReturn
	m.cursor = 0
	return m, nil
}

// enterFleetRow switches to the project a row came from.
func (m Model) enterFleetRow() (tea.Model, tea.Cmd) {
	r, ok := m.selectedFleetRow()
	if !ok {
		return m, nil
	}
	p, found := m.cfg.Project(r.Project)
	if !found {
		return m, nil
	}

	kindID := ""
	if m.fleet != nil {
		kindID = m.fleet.lister.Kind().ID
	}

	next, cmd := m.leaveFleet()
	m = next.(Model)

	// Switching projects invalidates everything cached for the previous one,
	// exactly as selecting from the picker does.
	if !m.hasActive || m.active.Name != p.Name {
		m.invalidate()
	}
	m.active, m.hasActive = p, true
	m.projCursor = m.projectIndex(p.Name)
	if idx, ok := m.kindIndex(kindID); ok {
		m.kindIdx = idx
	}
	m.screen = screenResources
	m.cursor = 0

	loaded, loadCmd := m.loadCurrentIfEmpty()
	return loaded, tea.Batch(cmd, loadCmd)
}

// visibleFleetRows applies the filter to the swept rows.
func (m Model) visibleFleetRows() []gcp.Resource {
	if m.fleet == nil {
		return nil
	}
	return filterResources(m.fleet.rows, strings.ToLower(strings.TrimSpace(m.filter.Value())))
}

func (m Model) selectedFleetRow() (gcp.Resource, bool) {
	rows := m.visibleFleetRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return gcp.Resource{}, false
	}
	return rows[m.cursor], true
}

// projectIndex finds a project's position in the picker, so entering a fleet
// row leaves the cursor where the user would expect it on the way back.
func (m Model) projectIndex(name string) int {
	for i, p := range m.cfg.Projects {
		if p.Name == name {
			return i
		}
	}
	return 0
}

// kindIndex resolves a kind id to its tab index.
func (m Model) kindIndex(kindID string) (int, bool) {
	for i, l := range m.listers {
		if l.Kind().ID == kindID {
			return i, true
		}
	}
	return 0, false
}

// fleetSummary is the progress and honesty line.
//
// It is the most important thing on the screen. A cross-project count reads as
// a statement about the estate, and it is only ever a statement about the
// projects that answered — so how many did, and how many did not, sits beside
// the number rather than somewhere a reader has to go looking.
func (m Model) fleetSummary() string {
	if m.fleet == nil {
		return ""
	}
	total := len(m.cfg.Projects)
	if m.fleet.loading {
		return fmt.Sprintf("sweeping %d projects…", total)
	}

	complete, partial, failed, skipped := m.fleet.result.Counts()
	summary := fmt.Sprintf("%d/%d projects read", complete, total)

	var caveats []string
	if partial > 0 {
		caveats = append(caveats, fmt.Sprintf("%d partial", partial))
	}
	if failed > 0 {
		caveats = append(caveats, fmt.Sprintf("%d failed", failed))
	}
	if skipped > 0 {
		caveats = append(caveats, fmt.Sprintf("%d skipped", skipped))
	}
	if len(caveats) > 0 {
		summary += " · " + strings.Join(caveats, ", ")
	}
	return summary
}
