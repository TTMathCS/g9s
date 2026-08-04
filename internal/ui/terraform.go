package ui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
	"github.com/TTMathCS/g9s/internal/tfstate"
)

// tfKind is the overlay's table: the current kind's rows, plus one column
// saying whether Terraform manages each.
//
// A screen of its own rather than a column bolted onto every kind's table.
// Forty-nine kinds would each need a column they do not always have an answer
// for, and the overlay is something you turn on to ask one question rather
// than something you want in the way the rest of the time.
var tfKind = gcp.Kind{
	ID:    "terraform",
	Title: "Terraform",
	Columns: []gcp.Column{
		{Title: "NAME", Width: 5},
		{Title: "LOCATION", Width: 3},
		{Title: "STATUS", Width: 2},
		{Title: "TERRAFORM", Width: 2, State: true},
	},
}

// tfState is one loaded overlay.
type tfState struct {
	// kind is the resource kind the overlay was opened on.
	kind gcp.Kind
	// index is the parsed state, or nil while loading.
	index *tfstate.Index
	// objects are the state files that were read, named so somebody looking at
	// a table of UNMANAGED rows can see which state was actually consulted.
	objects  []string
	warnings []gcp.Warning
	loading  bool
	err      error
	cancel   context.CancelFunc
	token    int
	rows     []gcp.Resource
	// mapped is false for a kind the overlay has no Terraform types for. The
	// rows then say "?" rather than UNMANAGED, and the screen says why.
	mapped bool
}

type tfFinishedMsg struct {
	token    int
	index    *tfstate.Index
	objects  []string
	warnings []gcp.Warning
	err      error
}

// startTerraform loads the state for the active project and overlays it on the
// current table.
func (m Model) startTerraform() (tea.Model, tea.Cmd) {
	if !m.hasActive {
		return m, flash("select a project first", flashWarn)
	}
	if m.screen != screenResources || m.drill != nil {
		return m, flash("open a resource table first — the overlay marks its rows", flashWarn)
	}

	kind := m.currentKind()
	if bucket, _ := m.cfg.TerraformBackend(m.active); bucket == "" {
		return m, flash(fmt.Sprintf("no terraform state bucket for %s — set terraform.state_bucket", m.active.Name), flashWarn)
	}

	if m.tf != nil && m.tf.cancel != nil {
		m.tf.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.Defaults.ListTimeout.Duration())
	token := 0
	if m.tf != nil {
		token = m.tf.token
	}
	token++

	_, mapped := gcp.TerraformTypesFor(kind.ID)
	m.tf = &tfState{kind: kind, loading: true, cancel: cancel, token: token, mapped: mapped}
	m.tfReturn = m.screen
	m.tfRows = m.visibleResources()
	m.screen = screenTerraform
	m.cursor = 0

	return m, loadTerraform(ctx, m.cfg, m.auth.ClientOptions(m.active), m.active, token)
}

func loadTerraform(ctx context.Context, cfg *config.Config, opts []option.ClientOption, p config.Project, token int) tea.Cmd {
	return func() tea.Msg {
		index, objects, warnings, err := gcp.TerraformState(ctx, cfg, p, opts)
		return tfFinishedMsg{token: token, index: index, objects: objects, warnings: warnings, err: err}
	}
}

func (m Model) handleTerraformFinished(msg tfFinishedMsg) (tea.Model, tea.Cmd) {
	if m.tf == nil || msg.token != m.tf.token {
		return m, nil
	}
	m.tf.index = msg.index
	m.tf.objects = msg.objects
	m.tf.warnings = msg.warnings
	m.tf.err = msg.err
	m.tf.loading = false
	m.tf.rows = m.overlayRows()
	m.clampCursor()
	return m, nil
}

// overlayRows reshapes the table the overlay was opened on, adding the verdict.
func (m Model) overlayRows() []gcp.Resource {
	if m.tf == nil {
		return nil
	}
	out := make([]gcp.Resource, 0, len(m.tfRows))
	for _, r := range m.tfRows {
		state := gcp.ManagedBy(m.tf.index, m.tf.kind.ID, r.Name)
		// The Status field carries the verdict rather than the resource's own
		// state, because it is what the row is being read for and what the
		// colouring keys off. The original state stays in its own column.
		row := r
		row.Row = []string{r.Name, r.Location, r.Status, state.String()}
		row.Status = state.String()
		out = append(out, row)
	}
	return out
}

func (m Model) handleTerraformKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m.leaveTerraform()

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		if m.cursor < len(m.visibleTerraformRows())-1 {
			m.cursor++
		}
		return m, nil

	case "g":
		m.cursor = 0
		return m, nil

	case "G":
		m.cursor = max(0, len(m.visibleTerraformRows())-1)
		return m, nil

	case "r":
		return m.reloadTerraform()

	case "d", "enter":
		if r, ok := m.selectedTerraformRow(); ok {
			return m.describe(r)
		}
		return m, nil

	case "y":
		if r, ok := m.selectedTerraformRow(); ok {
			return m, copyToClipboard(r.Name, m.clipboardLimit())
		}
		return m, nil
	}
	return m, nil
}

func (m Model) leaveTerraform() (tea.Model, tea.Cmd) {
	if m.tf != nil && m.tf.cancel != nil {
		m.tf.cancel()
	}
	m.tf = nil
	m.tfRows = nil
	m.screen = m.tfReturn
	m.cursor = 0
	return m, nil
}

// reloadTerraform re-reads the state.
//
// Only the state: the resource rows underneath came from the table this was
// opened on and are still the ones being marked. Re-listing them here would
// make `r` mean two different things depending on which screen it was pressed
// from.
func (m Model) reloadTerraform() (tea.Model, tea.Cmd) {
	if m.tf == nil || !m.hasActive {
		return m, nil
	}
	if m.tf.cancel != nil {
		m.tf.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.Defaults.ListTimeout.Duration())
	m.tf.cancel = cancel
	m.tf.token++
	m.tf.loading = true
	return m, loadTerraform(ctx, m.cfg, m.auth.ClientOptions(m.active), m.active, m.tf.token)
}

func (m Model) visibleTerraformRows() []gcp.Resource {
	if m.tf == nil {
		return nil
	}
	return filterResources(m.tf.rows, strings.ToLower(strings.TrimSpace(m.filter.Value())))
}

func (m Model) selectedTerraformRow() (gcp.Resource, bool) {
	rows := m.visibleTerraformRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return gcp.Resource{}, false
	}
	return rows[m.cursor], true
}

// terraformSummary is the honesty line.
//
// The overlay's whole hazard is somebody reading UNMANAGED and deleting
// something, so what was actually consulted goes on screen beside the verdict:
// which state files were read, and — when the kind is one g9s has no Terraform
// type for — that no verdict was reached at all.
func (m Model) terraformSummary() string {
	if m.tf == nil {
		return ""
	}
	if m.tf.loading {
		return "reading terraform state…"
	}
	if m.tf.err != nil {
		return m.tf.err.Error()
	}
	if !m.tf.mapped {
		return fmt.Sprintf("g9s has no Terraform type for %s — every row reads ?, none of them reads unmanaged",
			m.tf.kind.Title)
	}

	managed := 0
	for _, r := range m.tf.rows {
		if r.Status == gcp.ManagedYes.String() {
			managed++
		}
	}
	return fmt.Sprintf("%d of %d managed · %s",
		managed, len(m.tf.rows), summariseStateObjects(m.tf.objects))
}

// summariseStateObjects names what was read, since a table of UNMANAGED rows
// most often means the wrong state file.
func summariseStateObjects(objects []string) string {
	switch len(objects) {
	case 0:
		return "no state read"
	case 1:
		return "from " + objects[0]
	default:
		return fmt.Sprintf("from %s and %d more", objects[0], len(objects)-1)
	}
}
