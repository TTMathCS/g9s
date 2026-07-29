package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

func TestColumnWidthsFillAvailableSpace(t *testing.T) {
	cols := []gcp.Column{
		{Title: "NAME", Width: 6},
		{Title: "ZONE", Width: 3},
		{Title: "STATUS", Width: 2},
	}

	const available = 100
	widths := columnWidths(cols, available)

	if len(widths) != len(cols) {
		t.Fatalf("got %d widths, want %d", len(widths), len(cols))
	}

	// Sum of widths plus the two-space gaps must exactly fill the terminal, or
	// the table drifts away from the right edge.
	total := 0
	for _, w := range widths {
		total += w
	}
	total += 2 * (len(cols) - 1)

	if total != available {
		t.Errorf("widths %v total %d, want %d", widths, total, available)
	}
}

func TestColumnWidthsRespectWeights(t *testing.T) {
	cols := []gcp.Column{
		{Title: "NAME", Width: 6},
		{Title: "ZONE", Width: 3},
		{Title: "STATUS", Width: 3},
	}
	widths := columnWidths(cols, 200)

	if widths[0] <= widths[1] {
		t.Errorf("NAME (weight 6) got %d, ZONE (weight 3) got %d — heavier column should be wider", widths[0], widths[1])
	}
	if widths[1] != widths[2] {
		t.Errorf("equal weights produced %d and %d", widths[1], widths[2])
	}
}

func TestColumnWidthsSurviveTinyTerminals(t *testing.T) {
	cols := []gcp.Column{
		{Title: "NAME", Width: 6},
		{Title: "ZONE", Width: 3},
		{Title: "MACHINE TYPE", Width: 3},
		{Title: "STATUS", Width: 2},
	}

	// A narrow or nonsensical width must not produce zero or negative columns.
	for _, available := range []int{-10, 0, 1, 20, 40} {
		widths := columnWidths(cols, available)
		for i, w := range widths {
			if w < 1 {
				t.Errorf("available=%d: column %d width %d", available, i, w)
			}
		}
	}
}

func TestColumnWidthsHandleZeroWeights(t *testing.T) {
	cols := []gcp.Column{{Title: "A"}, {Title: "B"}}
	widths := columnWidths(cols, 60)
	for i, w := range widths {
		if w < 1 {
			t.Errorf("column %d width %d with zero weights", i, w)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"hello", 1, "…"},
		{"hello", 0, ""},
		{"hello", -1, ""},
		{"", 5, ""},
	}
	for _, tc := range tests {
		if got := truncate(tc.in, tc.width); got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}
	}
}

func TestTruncateCountsRunesNotBytes(t *testing.T) {
	// Resource names and warnings can contain non-ASCII; truncating by byte
	// would corrupt the output and break the layout.
	if got := truncate("héllo wörld", 5); lipgloss.Width(got) != 5 {
		t.Errorf("truncate produced display width %d, want 5 (%q)", lipgloss.Width(got), got)
	}
}

func TestPadFillsToExactWidth(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
	}{
		{"a", 10},
		{"exactly-10", 10},
		{"far too long to fit", 10},
		{"héllo", 10},
	} {
		got := pad(tc.in, tc.width)
		if lipgloss.Width(got) != tc.width {
			t.Errorf("pad(%q, %d) has display width %d", tc.in, tc.width, lipgloss.Width(got))
		}
	}
}

func TestRenderRowToleratesShortRows(t *testing.T) {
	// A lister returning fewer cells than columns must not panic the renderer.
	widths := []int{10, 10, 10}
	got := renderRow([]string{"only-one"}, widths)

	if lipgloss.Width(got) != 34 { // 3*10 + 2 gaps of 2
		t.Errorf("row display width = %d, want 34: %q", lipgloss.Width(got), got)
	}
}

func TestStatusStyleDistinguishesHealth(t *testing.T) {
	// Same style object for a healthy and a failed resource would make the
	// table unreadable at a glance.
	if statusStyle("RUNNING").GetForeground() == statusStyle("ERROR").GetForeground() {
		t.Error("RUNNING and ERROR render identically")
	}
	if statusStyle("RUNNING").GetForeground() == statusStyle("CREATING").GetForeground() {
		t.Error("RUNNING and CREATING render identically")
	}
	// Unknown states should fall back rather than panic.
	if statusStyle("SOMETHING_NEW").GetForeground() != rowStyle.GetForeground() {
		t.Error("unknown status should use the default row style")
	}
}

func TestStatusStyleIsCaseInsensitive(t *testing.T) {
	if statusStyle("running").GetForeground() != statusStyle("RUNNING").GetForeground() {
		t.Error("status colouring should not depend on case")
	}
}

func testModel(t *testing.T, resources []gcp.Resource) Model {
	t.Helper()

	cfg := &config.Config{
		Projects: []config.Project{{Name: "sandbox", ProjectID: "sandbox-123"}},
	}
	m := New(cfg, nil)
	m.active = cfg.Projects[0]
	m.hasActive = true
	m.cache[m.currentKind().ID] = gcp.Result{Resources: resources}
	return m
}

func TestVisibleResourcesFiltersOnAllColumns(t *testing.T) {
	m := testModel(t, []gcp.Resource{
		{Name: "web-01", Row: []string{"web-01", "us-central1-a", "RUNNING"}},
		{Name: "db-01", Row: []string{"db-01", "us-east1-b", "TERMINATED"}},
		{Name: "web-02", Row: []string{"web-02", "us-east1-b", "RUNNING"}},
	})

	tests := []struct {
		query string
		want  int
	}{
		{"", 3},
		{"web", 2},
		{"us-east1", 2},
		{"TERMINATED", 1},
		{"nothing-matches", 0},
	}

	for _, tc := range tests {
		m.filter.SetValue(tc.query)
		if got := len(m.visibleResources()); got != tc.want {
			t.Errorf("filter %q matched %d resources, want %d", tc.query, got, tc.want)
		}
	}
}

func TestVisibleResourcesFilterIsCaseInsensitive(t *testing.T) {
	m := testModel(t, []gcp.Resource{
		{Name: "Web-01", Row: []string{"Web-01", "us-central1-a", "RUNNING"}},
	})

	for _, query := range []string{"web", "WEB", "WeB", "  web  "} {
		m.filter.SetValue(query)
		if got := len(m.visibleResources()); got != 1 {
			t.Errorf("filter %q matched %d, want 1", query, got)
		}
	}
}

func TestSelectedResourceBoundsCheck(t *testing.T) {
	m := testModel(t, []gcp.Resource{
		{Name: "a", Row: []string{"a"}},
		{Name: "b", Row: []string{"b"}},
	})

	m.cursor = 0
	if r, ok := m.selectedResource(); !ok || r.Name != "a" {
		t.Errorf("cursor 0 selected %v (ok=%v), want a", r.Name, ok)
	}

	// A filter that shrinks the list below the cursor must not index past the
	// end — this is the crash the clamp exists to prevent.
	m.cursor = 5
	if _, ok := m.selectedResource(); ok {
		t.Error("out-of-range cursor should report no selection")
	}

	m.cursor = -1
	if _, ok := m.selectedResource(); ok {
		t.Error("negative cursor should report no selection")
	}
}

func TestClampCursorAfterFiltering(t *testing.T) {
	m := testModel(t, []gcp.Resource{
		{Name: "web-01", Row: []string{"web-01"}},
		{Name: "web-02", Row: []string{"web-02"}},
		{Name: "db-01", Row: []string{"db-01"}},
	})

	m.cursor = 2
	m.filter.SetValue("web") // now only two rows
	m.clampCursor()

	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1 after the list shrank to 2", m.cursor)
	}
	if _, ok := m.selectedResource(); !ok {
		t.Error("clamped cursor should point at a real row")
	}
}

func TestClampCursorOnEmptyList(t *testing.T) {
	m := testModel(t, nil)
	m.cursor = 3
	m.clampCursor()

	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 for an empty list", m.cursor)
	}
}

func TestWrapRespectsWidth(t *testing.T) {
	text := "permission denied on northamerica-northeast1 while listing dataproc clusters for this project"
	for _, line := range strings.Split(wrap(text, 30), "\n") {
		if len(line) > 30 {
			t.Errorf("line exceeds width: %q (%d)", line, len(line))
		}
	}
}

func TestWrapHandlesDegenerateWidths(t *testing.T) {
	if got := wrap("hello world", 0); got != "hello world" {
		t.Errorf("wrap with width 0 = %q, want the input unchanged", got)
	}
}

func TestKindTabsCoverAllListers(t *testing.T) {
	// The number keys 1..N are wired to lister indices, so a lister that the
	// tab bar cannot reach would be invisible.
	m := New(&config.Config{Projects: []config.Project{{Name: "a", ProjectID: "a-1"}}}, nil)
	if len(m.listers) == 0 {
		t.Fatal("no listers registered")
	}
	if len(m.listers) > 5 {
		t.Errorf("%d listers registered but only keys 1-5 are bound", len(m.listers))
	}
}

// TestViewRendersEveryScreen drives the render paths at a range of terminal
// sizes. The layout does index arithmetic over rows and columns, so this is
// the cheapest guard against an out-of-range panic reaching a user.
func TestViewRendersEveryScreen(t *testing.T) {
	resources := []gcp.Resource{
		{Name: "web-01", Location: "us-central1-a", Status: "RUNNING", Row: []string{"web-01", "us-central1-a", "n2-standard-4", "10.0.0.5", "34.1.2.3", "RUNNING", "3d"}},
		{Name: "db-01", Location: "us-east1-b", Status: "TERMINATED", Row: []string{"db-01", "us-east1-b", "n2-standard-8", "10.0.1.5", "-", "TERMINATED", "9d"}},
	}

	sizes := []struct{ w, h int }{
		{200, 60}, // wide
		{80, 24},  // standard
		{40, 12},  // narrow
		{20, 6},   // absurd but reachable by dragging a pane
	}

	for _, size := range sizes {
		for _, sc := range []screen{screenProjects, screenResources, screenDetail, screenHelp} {
			m := testModel(t, resources)
			m.width, m.height = size.w, size.h
			m.screen = sc
			m.detail.Width, m.detail.Height = size.w-4, m.bodyHeight()
			m.detail.SetContent("name: web-01\nzone: us-central1-a\n")

			out := m.View()
			if out == "" {
				t.Errorf("screen %v at %dx%d rendered nothing", sc, size.w, size.h)
			}
		}
	}
}

// TestViewRendersWarningsAndFlashes covers the footer states, which is where
// partial-listing information reaches the user.
func TestViewRendersFooterStates(t *testing.T) {
	m := testModel(t, nil)
	m.width, m.height = 80, 24
	m.screen = screenResources

	m.cache[m.currentKind().ID] = gcp.Result{Warnings: []string{"us-east1: permission denied"}}
	if !strings.Contains(m.View(), "permission denied") {
		t.Error("warning should reach the footer")
	}

	// A flash takes precedence over warnings while it is showing.
	m.flashText, m.flashLevel = "login complete", flashInfo
	if !strings.Contains(m.View(), "login complete") {
		t.Error("flash should take the footer while active")
	}
}

// TestViewSurvivesEmptyAndLoadingStates checks the paths that render before
// any data exists, including a cursor left over from a previous project.
func TestViewSurvivesEmptyAndLoadingStates(t *testing.T) {
	m := testModel(t, nil)
	m.width, m.height = 80, 24
	m.screen = screenResources
	m.cursor = 7

	delete(m.cache, m.currentKind().ID)
	m.loading[m.currentKind().ID] = true
	if out := m.View(); out == "" {
		t.Error("loading state rendered nothing")
	}

	m.loading[m.currentKind().ID] = false
	m.loadErr[m.currentKind().ID] = errTest{}
	if !strings.Contains(m.View(), "failed to list") {
		t.Error("error state should say the listing failed")
	}
}

type errTest struct{}

func (errTest) Error() string { return "boom" }
