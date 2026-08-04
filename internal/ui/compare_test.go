package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

// namedSnapshot is fleetSnapshot's sibling for the comparison, where the row
// names are the thing under test rather than incidental.
func namedSnapshot(cfg *config.Config, project string, names ...string) gcp.ProjectSnapshot {
	p, _ := cfg.Project(project)
	s := gcp.ProjectSnapshot{Project: p}
	for _, n := range names {
		s.Result.Resources = append(s.Result.Resources, gcp.Resource{
			Name: n, Location: "us-central1-a", Status: "RUNNING",
		})
	}
	return s
}

func compareModel(t *testing.T, width int, snapshots ...gcp.ProjectSnapshot) Model {
	t.Helper()
	m := New(fleetCfg(), nil)
	m.width, m.height = width, 24
	m.fleet = &fleetState{lister: gcp.Listers()[0], compare: true}
	next, _ := m.handleFleetFinished(fleetFinishedMsg{
		token:  0,
		result: gcp.FleetResult{Kind: gcp.Listers()[0].Kind(), Snapshots: snapshots},
	})
	m = next.(Model)
	m.screen = screenFleet
	return m
}

// The table's shape is the feature: one column per project, and the resource
// name once rather than once per project.
func TestComparisonPutsEachProjectInItsOwnColumn(t *testing.T) {
	cfg := fleetCfg()
	m := compareModel(t, 120,
		namedSnapshot(cfg, "prod", "api-prod"),
		namedSnapshot(cfg, "stg", "api-stg"),
		namedSnapshot(cfg, "dev", "api-dev"),
	)

	if got := len(m.fleet.compareKind.Columns); got != 4 {
		t.Fatalf("got %d columns, want RESOURCE plus three projects", got)
	}
	view := m.View()
	for _, p := range []string{"PROD", "STG", "DEV"} {
		if !strings.Contains(view, p) {
			t.Errorf("view never heads a column with %q:\n%s", p, view)
		}
	}

	rows := m.visibleFleetRows()
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want the one api matched across all three", len(rows))
	}
	if len(rows[0].Row) != 4 {
		t.Errorf("row has %d cells for 4 headings", len(rows[0].Row))
	}
}

// The distinction the whole table rests on. A project that was denied has not
// told us the resource is missing from it, and a `-` there would manufacture
// the table's most consequential finding out of a permission error.
func TestAbsentAndUnreadLookDifferentInTheTable(t *testing.T) {
	cfg := fleetCfg()
	m := compareModel(t, 120,
		namedSnapshot(cfg, "prod", "api-prod"),
		namedSnapshot(cfg, "stg"),
		func() gcp.ProjectSnapshot {
			s := namedSnapshot(cfg, "dev")
			s.Err = errors.New("permission denied")
			return s
		}(),
	)

	rows := m.visibleFleetRows()
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Row[2] != "-" {
		t.Errorf("stg was read and has nothing; cell = %q, want %q", rows[0].Row[2], "-")
	}
	if rows[0].Row[3] != "?" {
		t.Errorf("dev could not be read; cell = %q, want %q — never %q", rows[0].Row[3], "?", "-")
	}
}

// One sweep, two shapes. Toggling must not cost an API call, or people learn
// to pick the right one up front and never look at the other.
func TestTogglingTheShapeDoesNotRefetch(t *testing.T) {
	cfg := fleetCfg()
	m := compareModel(t, 120,
		namedSnapshot(cfg, "prod", "api-prod"),
		namedSnapshot(cfg, "dev", "api-dev"),
	)
	before := m.fleet.result

	next, cmd := m.handleFleetKey(key("c"))
	m = next.(Model)

	if cmd != nil {
		t.Error("toggling the shape started a command — it must reuse the sweep it has")
	}
	if m.fleet.compare {
		t.Error("`c` did not leave the comparison")
	}
	if len(m.fleet.result.Snapshots) != len(before.Snapshots) {
		t.Error("toggling the shape disturbed the sweep result")
	}

	// And the flat list is what shows now.
	rows := m.visibleFleetRows()
	if len(rows) != 2 {
		t.Fatalf("got %d rows in the flat list, want one per project", len(rows))
	}
	if rows[0].Row[0] != rows[0].Project {
		t.Errorf("flat row leads with %q, want the project", rows[0].Row[0])
	}
}

// A comparison with a column silently missing is a comparison of something
// else. Too narrow means fewer columns and a line saying which were dropped.
func TestANarrowTerminalDropsColumnsAndSaysSo(t *testing.T) {
	cfg := fleetCfg()
	m := compareModel(t, 44,
		namedSnapshot(cfg, "prod", "api-prod"),
		namedSnapshot(cfg, "stg", "api-stg"),
		namedSnapshot(cfg, "dev", "api-dev"),
	)

	if m.fleet.hiddenProjects == 0 {
		t.Fatalf("44 columns fitted all three projects plus the resource name")
	}
	view := m.View()
	if !strings.Contains(view, "widen the terminal") {
		t.Errorf("dropped columns are not mentioned:\n%s", view)
	}
	// And the projects that were dropped are named, not just counted.
	if !strings.Contains(view, "dev") {
		t.Errorf("the dropped project is not named:\n%s", view)
	}
	// The table still fits the terminal it was given.
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > m.width {
			t.Errorf("line is %d columns wide in an %d-column terminal: %q", width, m.width, line)
		}
	}
}

// In the comparison a row is about every project at once, so there is no one
// project to enter. `enter` opens the breakdown instead — which is also where
// the matching heuristic can be checked against the real names.
func TestEnterOnAComparisonRowShowsThePerProjectNames(t *testing.T) {
	cfg := fleetCfg()
	m := compareModel(t, 120,
		namedSnapshot(cfg, "prod", "api-prod"),
		namedSnapshot(cfg, "dev", "api-dev"),
	)

	next, _ := m.handleFleetKey(key("enter"))
	m = next.(Model)

	if m.screen != screenDetail {
		t.Fatalf("screen = %v, want the detail pane", m.screen)
	}
	detail := renderDetail(m.detailRes)
	for _, want := range []string{"api-prod", "api-dev", "prod", "dev"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the breakdown never mentions %q:\n%s", want, detail)
		}
	}
}

// A resource missing from a project that failed is unread, and the breakdown
// has to say so in those words rather than listing it as absent.
func TestTheBreakdownSeparatesAbsentFromUnread(t *testing.T) {
	cfg := fleetCfg()
	m := compareModel(t, 120,
		namedSnapshot(cfg, "prod", "api-prod"),
		namedSnapshot(cfg, "stg"),
		func() gcp.ProjectSnapshot {
			s := namedSnapshot(cfg, "dev")
			s.Skipped, s.SkipReason = true, "not logged in"
			return s
		}(),
	)

	next, _ := m.handleFleetKey(key("enter"))
	detail := renderDetail(next.(Model).detailRes)

	if !strings.Contains(detail, "absent") || !strings.Contains(detail, "unread") {
		t.Errorf("the breakdown does not separate absent from unread:\n%s", detail)
	}
}

// The summary carries the comparison's own numbers, since "3/3 projects read"
// says nothing about whether they agreed.
func TestComparisonSummaryCountsTheDifferences(t *testing.T) {
	cfg := fleetCfg()
	m := compareModel(t, 120,
		namedSnapshot(cfg, "prod", "api-prod", "only-in-prod"),
		namedSnapshot(cfg, "stg", "api-stg"),
		namedSnapshot(cfg, "dev", "api-dev"),
	)

	summary := m.fleetSummary()
	if !strings.Contains(summary, "1 differ") {
		t.Errorf("summary = %q, want it to count the one row that does not line up", summary)
	}
	if !strings.Contains(summary, "1 the same") {
		t.Errorf("summary = %q, want it to count the row that does", summary)
	}
}
