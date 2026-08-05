package ui

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"

	"github.com/TTMathCS/g9s/internal/gcp"
)

// emptyDashboard is a model where every kind has come back with nothing, which
// is the state right after logging in to a project that uses three services.
func emptyDashboard(t *testing.T) Model {
	t.Helper()
	m := populatedModel(t)
	m.width, m.height = 120, 40
	m.screen = screenOverview
	for _, l := range m.listers {
		m.cache[l.Kind().ID] = gcp.Result{}
		delete(m.loadErr, l.Kind().ID)
		m.loading[l.Kind().ID] = false
	}
	m.invalidateRows()
	return m
}

// The request: a dashboard listing fifty kinds a project does not use is
// mostly a list of things that are not there.
func TestEmptyKindsAreHiddenFromTheDashboard(t *testing.T) {
	m := emptyDashboard(t)
	m.cache["vm"] = gcp.Result{Resources: []gcp.Resource{
		{Name: "api-01", Status: "RUNNING", Row: []string{"api-01"}},
	}}
	m.invalidateRows()

	visible, hidden := m.overviewRows()
	if hidden == 0 {
		t.Fatal("nothing was hidden on a dashboard where one kind of many has rows")
	}
	for _, row := range visible {
		if row.kind.ID != "vm" && row.kind.ID != allKind.ID {
			t.Errorf("%q has nothing in it and is still on the dashboard", row.kind.ID)
		}
	}
}

// A kind whose API nobody enabled is the other half of the request. It is the
// normal state for most services in most projects and carries no information.
func TestAKindWithItsAPIDisabledIsHidden(t *testing.T) {
	m := emptyDashboard(t)
	m.loadErr["spanner"] = &googleapi.Error{
		Code:    403,
		Message: "Cloud Spanner API has not been used in project 12345 before or it is disabled.",
	}

	if !m.kindIsEmpty(kindByID(t, m, "spanner")) {
		t.Error("a kind whose API is off is still taking a dashboard row")
	}
}

// The line that must never be filtered. "Permission denied" is the difference
// between "nothing there" and "you cannot see it", and hiding it would tell
// somebody a project is empty when they simply cannot read it.
func TestAFailureIsNeverHidden(t *testing.T) {
	m := emptyDashboard(t)

	for _, tt := range []struct {
		name string
		err  error
	}{
		{"permission denied", &googleapi.Error{Code: 403, Message: "does not have storage.buckets.list access"}},
		{"unauthenticated", &googleapi.Error{Code: 401, Message: "invalid credentials"}},
		{"something else", errors.New("connection reset by peer")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m.loadErr["gcs"] = tt.err
			if m.kindIsEmpty(kindByID(t, m, "gcs")) {
				t.Errorf("a %s failure was hidden from the dashboard", tt.name)
			}
		})
	}
}

// An empty listing that came back with a warning is the most misleading empty
// there is — capped, or partly denied — and hiding it deletes the only notice
// that it happened.
func TestAnEmptyButWarnedListingStaysVisible(t *testing.T) {
	m := emptyDashboard(t)
	m.cache["gcs"] = gcp.Result{
		Warnings: []gcp.Warning{{Scope: "us-east4", Reason: gcp.ReasonDenied, Detail: "permission denied"}},
	}
	m.invalidateRows()

	if m.kindIsEmpty(kindByID(t, m, "gcs")) {
		t.Error("an empty listing with a warning on it was hidden")
	}
}

// A row that vanishes and comes back as the sweep lands is a dashboard that
// flickers through the whole load.
func TestAKindStillLoadingIsNotHidden(t *testing.T) {
	m := emptyDashboard(t)
	m.loading["gcs"] = true
	if m.kindIsEmpty(kindByID(t, m, "gcs")) {
		t.Error("a kind still being fetched was hidden as empty")
	}
}

// Filtering that does not announce itself gets read as the whole estate.
func TestTheDashboardSaysHowManyItIsHiding(t *testing.T) {
	m := emptyDashboard(t)
	view := m.View()

	if !strings.Contains(view, "more with nothing in them") {
		t.Errorf("the dashboard hides rows without saying so:\n%s", view)
	}
	if !strings.Contains(view, "+") {
		t.Errorf("the dashboard does not say how to see them:\n%s", view)
	}
}

func TestPlusRevealsEveryKindAndBack(t *testing.T) {
	m := emptyDashboard(t)
	before, _ := m.overviewRows()

	next, _ := m.handleOverviewKey(keyRunes("+"))
	m = next.(Model)

	all, hidden := m.overviewRows()
	if hidden != 0 {
		t.Errorf("%d rows still hidden after +", hidden)
	}
	if len(all) != len(m.tabs()) {
		t.Errorf("showing everything rendered %d of %d tabs", len(all), len(m.tabs()))
	}
	if !strings.Contains(m.View(), "showing every kind") {
		t.Error("the expanded dashboard does not say it is expanded")
	}

	back, _ := m.handleOverviewKey(keyRunes("+"))
	if got, _ := back.(Model).overviewRows(); len(got) != len(before) {
		t.Errorf("+ twice left %d rows, want the original %d", len(got), len(before))
	}
}

// A hotkey that moved with whatever happened to be empty this morning would be
// worse than no hotkey, so the key comes from the kind's place in tabs() rather
// than its place in the filtered list.
func TestHiddenRowsDoNotRenumberTheHotkeys(t *testing.T) {
	m := emptyDashboard(t)
	// Give the last lister rows so it is the only kind left besides the merged
	// view, and its key must still be its own.
	last := m.listers[len(m.listers)-1].Kind().ID
	m.cache[last] = gcp.Result{Resources: []gcp.Resource{{Name: "x", Row: []string{"x"}}}}
	m.invalidateRows()

	visible, _ := m.overviewRows()
	for _, row := range visible {
		if row.kind.ID != last {
			continue
		}
		if got, want := m.tabKey(row.index), kindKey(len(m.listers)-1); got != want {
			t.Errorf("hotkey for %q became %q, want its own %q", last, got, want)
		}
		return
	}
	t.Fatalf("%q has rows and was hidden anyway", last)
}

// The cursor must not sit on a row nobody can see, or enter opens a table the
// user never selected.
func TestTheCursorSkipsHiddenRows(t *testing.T) {
	m := emptyDashboard(t)
	m.cache["vm"] = gcp.Result{Resources: []gcp.Resource{{Name: "api-01", Row: []string{"api-01"}}}}
	m.invalidateRows()

	visible, _ := m.overviewRows()
	if len(visible) < 2 {
		t.Fatalf("need at least two visible rows, got %d", len(visible))
	}

	m.ovCursor = visible[0].index
	next, _ := m.handleOverviewKey(keyRunes("j"))
	m = next.(Model)

	if m.ovCursor != visible[1].index {
		t.Errorf("j moved the cursor to tab %d, want the next visible row %d",
			m.ovCursor, visible[1].index)
	}

	// And G lands on the last visible row rather than the last tab.
	next, _ = m.handleOverviewKey(keyRunes("G"))
	if got := next.(Model).ovCursor; got != visible[len(visible)-1].index {
		t.Errorf("G left the cursor on tab %d, want %d", got, visible[len(visible)-1].index)
	}
}

// A cursor left on a kind that has just been hidden has to be rescued, or the
// dashboard highlights nothing and enter opens something invisible.
func TestACursorOnAKindThatBecomesHiddenIsMovedBack(t *testing.T) {
	m := populatedModel(t)
	m.width, m.height = 120, 40
	m.screen = screenOverview

	idx, ok := m.kindIndex("gcs")
	if !ok {
		t.Fatal("no gcs kind")
	}
	m.ovCursor = idx
	m.cache["gcs"] = gcp.Result{}
	m.invalidateRows()

	m = m.clampOverviewCursor()

	visible, _ := m.overviewRows()
	for _, row := range visible {
		if row.index == m.ovCursor {
			return
		}
	}
	t.Errorf("cursor left on tab %d, which is not on screen", m.ovCursor)
}

// A project where genuinely nothing loaded must not render as a blank screen
// with a footnote, which reads as broken rather than as empty.
func TestADashboardWithNothingAtAllStillDrawsARow(t *testing.T) {
	m := emptyDashboard(t)
	visible, _ := m.overviewRows()

	if len(visible) == 0 {
		t.Fatal("an entirely empty project rendered no rows at all")
	}
	if visible[0].kind.ID != allKind.ID {
		t.Errorf("the surviving row is %q, want the merged view", visible[0].kind.ID)
	}
}

func kindByID(t *testing.T, m Model, id string) gcp.Kind {
	t.Helper()
	for _, l := range m.listers {
		if l.Kind().ID == id {
			return l.Kind()
		}
	}
	t.Fatalf("no kind %q", id)
	return gcp.Kind{}
}
