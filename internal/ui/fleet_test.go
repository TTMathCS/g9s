package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

func fleetCfg() *config.Config {
	return &config.Config{Projects: []config.Project{
		{Name: "prod", ProjectID: "acme-prod-1"},
		{Name: "stg", ProjectID: "acme-stg-1"},
		{Name: "dev", ProjectID: "acme-dev-1"},
	}}
}

func fleetSnapshot(cfg *config.Config, name string, rows int, err error, skip string, partial bool) gcp.ProjectSnapshot {
	p, _ := cfg.Project(name)
	s := gcp.ProjectSnapshot{Project: p}
	switch {
	case skip != "":
		s.Skipped, s.SkipReason = true, skip
	case err != nil:
		s.Err = err
	default:
		for i := 0; i < rows; i++ {
			s.Result.Resources = append(s.Result.Resources, gcp.Resource{
				Name: "vm", Location: "us-central1-a", Status: "RUNNING",
			})
		}
		if partial {
			s.Result.Warnings = []gcp.Warning{
				{Scope: "us-east4", Reason: gcp.ReasonDenied, Detail: "permission denied"},
			}
		}
	}
	return s
}

func fleetModel(t *testing.T, snapshots ...gcp.ProjectSnapshot) Model {
	t.Helper()
	cfg := fleetCfg()
	m := New(cfg, nil)
	m.width, m.height = 100, 24
	result := gcp.FleetResult{Kind: gcp.Listers()[0].Kind(), Snapshots: snapshots}
	m.fleet = &fleetState{lister: gcp.Listers()[0]}
	next, _ := m.handleFleetFinished(fleetFinishedMsg{token: 0, result: result})
	m = next.(Model)
	m.screen = screenFleet
	return m
}

// The count is the number people quote. A fleet view's whole hazard is that it
// reads as a statement about the estate when it is a statement about the
// projects that answered.
func TestFleetSummaryNamesEveryProjectThatDidNotContribute(t *testing.T) {
	cfg := fleetCfg()
	m := fleetModel(t,
		fleetSnapshot(cfg, "prod", 2, nil, "", true),
		fleetSnapshot(cfg, "stg", 1, nil, "", false),
		fleetSnapshot(cfg, "dev", 0, errors.New("permission denied"), "", false),
	)

	summary := m.fleetSummary()
	if !strings.Contains(summary, "1/3") {
		t.Errorf("summary = %q, want it to say how many of three were read completely", summary)
	}
	for _, want := range []string{"partial", "failed"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary = %q, missing %q", summary, want)
		}
	}

	// And the view has to name which, since "1 failed" does not tell anyone
	// which project to go and look at.
	view := m.View()
	for _, want := range []string{"prod", "dev"} {
		if !strings.Contains(view, want) {
			t.Errorf("view never names the project %q that did not contribute", want)
		}
	}
}

func TestACompleteSweepReadsAsComplete(t *testing.T) {
	cfg := fleetCfg()
	m := fleetModel(t,
		fleetSnapshot(cfg, "prod", 2, nil, "", false),
		fleetSnapshot(cfg, "stg", 1, nil, "", false),
		fleetSnapshot(cfg, "dev", 1, nil, "", false),
	)

	summary := m.fleetSummary()
	if !strings.Contains(summary, "3/3") {
		t.Errorf("summary = %q, want 3/3", summary)
	}
	for _, unwanted := range []string{"partial", "failed", "skipped"} {
		if strings.Contains(summary, unwanted) {
			t.Errorf("a complete sweep reports %q: %s", unwanted, summary)
		}
	}
	if !m.fleet.result.Trustworthy() {
		t.Error("a sweep where every project answered is not trustworthy")
	}
}

// Two instances with the same name in dev and prod are the pair a comparison
// exists to look at, so the project has to be the first column.
func TestFleetRowsAreShapedToTheFleetColumns(t *testing.T) {
	cfg := fleetCfg()
	m := fleetModel(t,
		fleetSnapshot(cfg, "prod", 1, nil, "", false),
		fleetSnapshot(cfg, "dev", 1, nil, "", false),
	)

	rows := m.visibleFleetRows()
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for _, r := range rows {
		if len(r.Row) != len(fleetKind.Columns) {
			t.Fatalf("row has %d cells, want %d to match the headings",
				len(r.Row), len(fleetKind.Columns))
		}
		if r.Row[0] != r.Project {
			t.Errorf("first cell is %q, want the project %q", r.Row[0], r.Project)
		}
	}
}

// A fleet view that goes on costing API calls after you have left is a view
// people learn not to open.
func TestLeavingTheFleetCancelsTheSweep(t *testing.T) {
	cfg := fleetCfg()
	m := fleetModel(t, fleetSnapshot(cfg, "prod", 1, nil, "", false))

	cancelled := false
	m.fleet.cancel = func() { cancelled = true }
	m.fleetReturn = screenProjects

	next, _ := m.handleFleetKey(key("esc"))
	m = next.(Model)

	if !cancelled {
		t.Error("leaving the fleet view did not cancel the sweep")
	}
	if m.fleet != nil {
		t.Error("the fleet state survived leaving")
	}
	if m.screen != screenProjects {
		t.Errorf("screen = %v, want the screen it was opened from", m.screen)
	}
}

// A slow sweep landing after a newer one would replace the table the user is
// looking at with rows for a kind they left.
func TestSupersededSweepIsDropped(t *testing.T) {
	cfg := fleetCfg()
	m := fleetModel(t, fleetSnapshot(cfg, "prod", 1, nil, "", false))
	m.fleet.token = 7

	stale := gcp.FleetResult{Snapshots: []gcp.ProjectSnapshot{
		fleetSnapshot(cfg, "dev", 99, nil, "", false),
	}}
	next, _ := m.handleFleetFinished(fleetFinishedMsg{token: 3, result: stale})
	m = next.(Model)

	if len(m.visibleFleetRows()) != 1 {
		t.Errorf("a stale sweep replaced the current rows: %d rows", len(m.visibleFleetRows()))
	}
}

// enter is the way from "which project is different" to "what is going on in
// that project", which is the whole point of noticing.
func TestEnterOpensTheProjectTheRowCameFrom(t *testing.T) {
	cfg := fleetCfg()
	m := fleetModel(t,
		fleetSnapshot(cfg, "prod", 1, nil, "", false),
		fleetSnapshot(cfg, "stg", 1, nil, "", false),
	)
	m.fleet.cancel = func() {}
	m.cursor = 1 // stg, since rows sort by project

	want := m.visibleFleetRows()[1].Project
	next, _ := m.handleFleetKey(key("enter"))
	m = next.(Model)

	if !m.hasActive || m.active.Name != want {
		t.Errorf("active project = %q, want %q", m.active.Name, want)
	}
	if m.screen != screenResources {
		t.Errorf("screen = %v, want the resource table", m.screen)
	}
	if m.fleet != nil {
		t.Error("the fleet sweep was left running after entering a project")
	}
}

// Skipped projects are the ones most likely to be misread as empty.
func TestSkippedProjectsSayWhyRatherThanLookingEmpty(t *testing.T) {
	cfg := fleetCfg()
	m := fleetModel(t,
		fleetSnapshot(cfg, "prod", 1, nil, "", false),
		fleetSnapshot(cfg, "dev", 0, nil, "not logged in", false),
	)

	view := m.View()
	if !strings.Contains(view, "not logged in") {
		t.Errorf("a skipped project does not say why:\n%s", view)
	}
	if !strings.Contains(m.fleetSummary(), "skipped") {
		t.Errorf("summary = %q, want it to count the skipped project", m.fleetSummary())
	}
}
