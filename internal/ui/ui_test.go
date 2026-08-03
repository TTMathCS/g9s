package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/TTMathCS/g9s/internal/auth"
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

func TestTruncateCountsDisplayCellsForWideRunes(t *testing.T) {
	// CJK characters occupy two terminal cells. Counting runes instead of
	// cells would let one wide name push every column after it out of line.
	for width := 1; width <= 8; width++ {
		got := truncate("数据库集群", width)
		if w := lipgloss.Width(got); w > width {
			t.Errorf("truncate(wide, %d) rendered %d cells (%q)", width, w, got)
		}
	}
	if got := truncate("数据库集群", 10); got != "数据库集群" {
		t.Errorf("a string that fits should come back whole, got %q", got)
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

func TestEveryKindIsReachable(t *testing.T) {
	// The real invariant is that no registered lister is unreachable, not that
	// it has a digit. Past nine kinds the digits run out — `1`-`9` cover the
	// first nine, and `tab`/`shift+tab` plus `:<id>` reach all of them. A kind
	// that no key or command could select would be dead code with an API cost.
	m := New(&config.Config{Projects: []config.Project{{Name: "a", ProjectID: "a-1"}}}, nil)
	if len(m.listers) == 0 {
		t.Fatal("no listers registered")
	}

	tabs := m.tabs()
	for want, kind := range tabs {
		got, ok := m.matchKind(kind.ID)
		if !ok {
			t.Errorf("kind %q cannot be selected by `:%s`", kind.ID, kind.ID)
			continue
		}
		// An exact ID must win outright. Without that, a shorter ID earlier in
		// the list would shadow a longer one — `:vpc` landing on `vm` because
		// prefix matching got there first.
		if got != want {
			t.Errorf("`:%s` selected tab %d (%q), want tab %d", kind.ID, got, tabs[got].ID, want)
		}
	}
}

func TestCycleReachesEveryKind(t *testing.T) {
	// Tab-cycling is the only mechanism that covers kinds past the ninth
	// without typing a command, so it has to visit all of them and wrap.
	m := New(&config.Config{Projects: []config.Project{{Name: "a", ProjectID: "a-1"}}}, nil)
	m.active = m.cfg.Projects[0]
	m.hasActive = true
	m.authStatus["a"] = auth.Status{State: auth.StateValid}
	m.screen = screenResources

	seen := map[string]bool{}
	for i := 0; i < len(m.tabs()); i++ {
		seen[m.currentKind().ID] = true
		next, _ := m.handleResourcesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
		m = next.(Model)
	}

	for _, kind := range m.tabs() {
		if !seen[kind.ID] {
			t.Errorf("cycling never reached %q", kind.ID)
		}
	}
	if m.kindIdx != 0 {
		t.Errorf("a full lap ended on tab %d, want it to wrap to 0", m.kindIdx)
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
		for _, sc := range []screen{screenProjects, screenOverview, screenResources, screenDetail, screenHelp} {
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

	m.cache[m.currentKind().ID] = gcp.Result{Warnings: []gcp.Warning{gcp.Warning{Scope: "us-east1", Reason: gcp.ReasonDenied, Detail: "permission denied"}}}
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

// --- dashboard and merged view ---

// populatedModel caches every kind, so the dashboard and the merged table have
// something to aggregate.
func populatedModel(t *testing.T) Model {
	t.Helper()

	m := New(&config.Config{Projects: []config.Project{{Name: "prod", ProjectID: "prod-1"}}}, nil)
	m.width, m.height = 132, 24
	m.active, m.hasActive = m.cfg.Projects[0], true
	m.authStatus["prod"] = auth.Status{State: auth.StateValid}

	m.cache["vm"] = gcp.Result{Resources: []gcp.Resource{
		{Name: "web-01", Location: "us-central1-a", Status: "RUNNING", Row: []string{"web-01", "us-central1-a", "n2-standard-4", "10.0.0.5", "-", "RUNNING", "3d"}},
		{Name: "web-02", Location: "us-central1-a", Status: "RUNNING", Row: []string{"web-02", "us-central1-a", "n2-standard-4", "10.0.0.6", "-", "RUNNING", "3d"}},
		{Name: "db-01", Location: "us-east1-b", Status: "TERMINATED", Row: []string{"db-01", "us-east1-b", "n2-standard-8", "10.0.1.5", "-", "TERMINATED", "9d"}},
	}}
	m.cache["dataproc"] = gcp.Result{
		Resources: []gcp.Resource{{Name: "etl", Location: "us-central1", Status: "RUNNING", Row: []string{"etl", "us-central1", "RUNNING", "4", "12d"}}},
		Warnings:  []gcp.Warning{gcp.Warning{Scope: "us-east4", Reason: gcp.ReasonDenied, Detail: "permission denied"}},
	}
	m.cache["composer"] = gcp.Result{Resources: []gcp.Resource{
		{Name: "airflow", Location: "us-central1", Status: "RUNNING", Row: []string{"airflow", "us-central1", "RUNNING", "2.7.3", "88d"}},
	}}
	return m
}

func TestMergedViewRowsMatchAllKindColumns(t *testing.T) {
	// The table renderer indexes cells against the declared columns, so a
	// mismatch here is an out-of-range panic in front of a user.
	m := populatedModel(t)
	merged := m.mergedResources()

	if len(merged) != 5 {
		t.Fatalf("merged %d resources, want 5 across the three kinds", len(merged))
	}
	for _, r := range merged {
		if len(r.Row) != len(allKind.Columns) {
			t.Errorf("%s has %d cells, allKind declares %d columns", r.Name, len(r.Row), len(allKind.Columns))
		}
	}
}

func TestMergedViewKeepsListerOrderAndIdentity(t *testing.T) {
	m := populatedModel(t)
	merged := m.mergedResources()

	wantKinds := []string{"VM Instances", "VM Instances", "VM Instances", "Dataproc Clusters", "Composer Environments"}
	for i, want := range wantKinds {
		if merged[i].Row[0] != want {
			t.Errorf("row %d kind cell = %q, want %q", i, merged[i].Row[0], want)
		}
	}

	// Name, Location and Status have to survive the reshape: the merged table
	// is not a dead end, it still drives describe, open, yank and ssh.
	if merged[0].Name != "web-01" || merged[0].Location != "us-central1-a" || merged[0].Status != "RUNNING" {
		t.Errorf("identity fields lost in merge: %+v", merged[0])
	}
}

func TestMergedViewPreservesRawForActions(t *testing.T) {
	// SSHTarget and AirflowURI both type-switch on Raw, so dropping it would
	// silently disable ssh and open from the merged table.
	m := populatedModel(t)
	sentinel := struct{ marker string }{marker: "raw"}
	m.cache["vm"] = gcp.Result{Resources: []gcp.Resource{
		{Name: "web-01", Status: "RUNNING", Row: []string{"web-01"}, Raw: sentinel, ConsoleURL: "https://console.example"},
	}}

	merged := m.mergedResources()
	if merged[0].Raw != any(sentinel) {
		t.Error("Raw dropped during merge — ssh and open would stop working")
	}
	if merged[0].ConsoleURL != "https://console.example" {
		t.Error("ConsoleURL dropped during merge")
	}
}

func TestMergedViewFiltersAcrossKinds(t *testing.T) {
	m := populatedModel(t)
	m.kindIdx = m.allTabIdx()

	m.filter.SetValue("composer")
	if got := len(m.visibleResources()); got != 1 {
		t.Errorf("filtering the merged table on a kind name matched %d rows, want 1", got)
	}

	m.filter.SetValue("us-central1-a")
	if got := len(m.visibleResources()); got != 2 {
		t.Errorf("filtering on a location matched %d rows, want 2", got)
	}
}

func TestStatusCountsOrderAndTieBreak(t *testing.T) {
	got := statusCounts([]gcp.Resource{
		{Status: "RUNNING"}, {Status: "RUNNING"}, {Status: "RUNNING"},
		{Status: "TERMINATED"},
		{Status: "STAGING"},
	})

	if len(got) != 3 {
		t.Fatalf("got %d buckets, want 3: %+v", len(got), got)
	}
	if got[0].status != "RUNNING" || got[0].count != 3 {
		t.Errorf("most common bucket = %+v, want 3 RUNNING", got[0])
	}
	// Equal counts sort alphabetically so the dashboard does not reshuffle
	// between refreshes returning identical data.
	if got[1].status != "STAGING" || got[2].status != "TERMINATED" {
		t.Errorf("ties should break alphabetically, got %q then %q", got[1].status, got[2].status)
	}
}

func TestStatusCountsCollapsesLongTail(t *testing.T) {
	got := statusCounts([]gcp.Resource{
		{Status: "A"}, {Status: "B"}, {Status: "C"}, {Status: "D"}, {Status: "E"}, {Status: "F"},
	})

	if len(got) != 4 {
		t.Fatalf("got %d buckets, want 4 (three plus OTHER)", len(got))
	}
	last := got[len(got)-1]
	if last.status != "OTHER" || last.count != 3 {
		t.Errorf("tail bucket = %+v, want OTHER covering the 3 not shown individually", last)
	}

	// Every resource has to be accounted for somewhere, or the breakdown
	// contradicts the count next to it.
	total := 0
	for _, sc := range got {
		total += sc.count
	}
	if total != 6 {
		t.Errorf("buckets total %d, want all 6 resources accounted for", total)
	}
}

func TestStatusCountsLabelsMissingStatus(t *testing.T) {
	got := statusCounts([]gcp.Resource{{Status: ""}})
	if len(got) != 1 || got[0].status != "UNKNOWN" {
		t.Errorf("blank status should bucket as UNKNOWN, got %+v", got)
	}
}

func TestTabsIncludeMergedViewLast(t *testing.T) {
	m := populatedModel(t)
	tabs := m.tabs()

	if len(tabs) != len(m.listers)+1 {
		t.Fatalf("got %d tabs for %d listers, want one more", len(tabs), len(m.listers))
	}
	if tabs[len(tabs)-1].ID != allKind.ID {
		t.Errorf("merged view should be the last tab, got %q", tabs[len(tabs)-1].ID)
	}
	if m.allTabIdx() != len(tabs)-1 {
		t.Errorf("allTabIdx %d does not point at the last tab %d", m.allTabIdx(), len(tabs)-1)
	}
}

func TestTabCountAggregatesAcrossKinds(t *testing.T) {
	m := populatedModel(t)

	n, known := m.tabCount(allKind)
	if !known || n != 5 {
		t.Errorf("merged tab count = %d (known=%v), want 5", n, known)
	}

	// Unknown until at least one kind has landed, otherwise a fresh project
	// would claim it has zero of everything.
	empty := New(m.cfg, nil)
	if _, known := empty.tabCount(allKind); known {
		t.Error("merged count should be unknown before any kind loads")
	}
}

func TestCycleTabsReachesMergedView(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenResources

	seen := map[string]bool{}
	for i := 0; i < len(m.tabs()); i++ {
		seen[m.currentKind().ID] = true
		next, _ := m.handleResourcesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
		m = next.(Model)
	}
	if !seen[allKind.ID] {
		t.Error("cycling with ] never reached the merged view")
	}
	if m.kindIdx != 0 {
		t.Errorf("cycling a full lap ended on tab %d, want to wrap to 0", m.kindIdx)
	}
}

func TestDashboardEnterOpensSelectedCategory(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenOverview
	m.ovCursor = 1

	next, _ := m.handleOverviewKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(Model)

	if got.screen != screenResources {
		t.Errorf("enter on the dashboard left screen = %v, want screenResources", got.screen)
	}
	if got.kindIdx != 1 {
		t.Errorf("opened tab %d, want the one under the cursor (1)", got.kindIdx)
	}
}

func TestDashboardAKeyOpensMergedView(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenOverview

	next, _ := m.handleOverviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	got := next.(Model)

	if !got.onAllTab() {
		t.Errorf("a should open the merged view, landed on tab %d", got.kindIdx)
	}
	if got.screen != screenResources {
		t.Errorf("screen = %v, want screenResources", got.screen)
	}
}

func TestEscFromTableReturnsToDashboard(t *testing.T) {
	// esc backing out to the project list would skip the level the user
	// actually navigates from.
	m := populatedModel(t)
	m.screen = screenResources
	m.kindIdx = 2

	next, _ := m.handleResourcesKey(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(Model)

	if got.screen != screenOverview {
		t.Errorf("esc left screen = %v, want screenOverview", got.screen)
	}
	if got.ovCursor != 2 {
		t.Errorf("dashboard cursor = %d, want it parked on the category we came from (2)", got.ovCursor)
	}
}

func TestDashboardCursorStaysInRange(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenOverview

	// More presses than there are tabs, so the clamp is what stops it rather
	// than running out of rows. Derived, so adding a kind does not fail this.
	for i := 0; i < len(m.tabs())+5; i++ {
		next, _ := m.handleOverviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		m = next.(Model)
	}
	if m.ovCursor != len(m.tabs())-1 {
		t.Errorf("cursor ran to %d, want it to stop at the last tab %d", m.ovCursor, len(m.tabs())-1)
	}

	for i := 0; i < len(m.tabs())+5; i++ {
		next, _ := m.handleOverviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
		m = next.(Model)
	}
	if m.ovCursor != 0 {
		t.Errorf("cursor ran to %d going up, want 0", m.ovCursor)
	}
}

func TestDashboardRendersCountsStatusesAndWarnings(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenOverview
	// Tall enough for every category at once: the dashboard windows to fit the
	// terminal, and this test is about what a row says, not about scrolling.
	m.height = len(m.tabs()) + chromeHeight + 2
	out := m.View()

	for _, want := range []string{"VM Instances", "All Resources", "RUNNING", "TERMINATED"} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard is missing %q", want)
		}
	}
	// The Dataproc listing came back partial; that has to be visible without
	// drilling in, or a truncated list reads as complete.
	if !strings.Contains(out, "1 warning") {
		t.Error("dashboard should flag the partial Dataproc listing")
	}
}

func TestDashboardDistinguishesLoadingEmptyAndFailed(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenOverview

	m.loading["vm"] = true
	if !strings.Contains(m.View(), "loading…") {
		t.Error("a kind still fetching should say so")
	}
	m.loading["vm"] = false

	m.cache["vm"] = gcp.Result{}
	if !strings.Contains(m.View(), "none") {
		t.Error("a kind that loaded with nothing in it should read as none, not blank")
	}

	m.loadErr["vm"] = errTest{}
	if !strings.Contains(m.View(), "failed") {
		t.Error("a failed kind should say so on the dashboard")
	}
}

// --- refresh tokens ---

// TestPerKindRefreshDoesNotStrandOtherKinds is the regression test for a real
// bug: with one global token, refreshing a single kind invalidated every other
// kind's in-flight fetch, and the dropped results left their loading flags set
// forever — the dashboard showed loading… until a full refresh.
func TestPerKindRefreshDoesNotStrandOtherKinds(t *testing.T) {
	m := testModel(t, nil)
	m.cache = map[string]gcp.Result{}
	m.authStatus["sandbox"] = auth.Status{State: auth.StateValid}

	next, _ := m.loadAll() // every kind starts fetching
	m = next.(Model)

	next, _ = m.loadCurrent() // refresh just the current kind
	m = next.(Model)

	// The other kind's original fetch now lands, with its own token.
	other := m.listers[1].Kind().ID
	next, _ = m.handleResources(resourcesMsg{
		project: "sandbox",
		kind:    other,
		token:   m.refreshToken[other],
		result:  gcp.Result{},
	})
	m = next.(Model)

	if m.loading[other] {
		t.Errorf("%s still loading after its result arrived — a per-kind refresh must not invalidate other kinds", other)
	}
	if !m.hasData(other) {
		t.Errorf("%s result was dropped", other)
	}
}

func TestStaleResultOfSameKindIsDropped(t *testing.T) {
	m := testModel(t, nil)
	m.cache = map[string]gcp.Result{}
	m.authStatus["sandbox"] = auth.Status{State: auth.StateValid}

	next, _ := m.loadCurrent()
	m = next.(Model)
	kind := m.currentKind().ID
	stale := m.refreshToken[kind]

	next, _ = m.loadCurrent() // supersede it
	m = next.(Model)

	next, _ = m.handleResources(resourcesMsg{
		project: "sandbox",
		kind:    kind,
		token:   stale,
		result:  gcp.Result{Resources: []gcp.Resource{{Name: "old"}}},
	})
	m = next.(Model)

	if m.hasData(kind) {
		t.Error("superseded result should be dropped, not cached")
	}
	if !m.loading[kind] {
		t.Error("the newer fetch is still in flight; loading must stay set")
	}
}

// --- key habits ---

func TestQBacksUpOneLevelInsteadOfQuitting(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenResources

	next, _ := m.handleResourcesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	got := next.(Model)
	if got.quitting {
		t.Fatal("q on a table should back up, not quit the app")
	}
	if got.screen != screenOverview {
		t.Errorf("q from the table landed on %v, want the dashboard", got.screen)
	}

	next, _ = got.handleOverviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	got = next.(Model)
	if got.quitting || got.screen != screenProjects {
		t.Errorf("q from the dashboard should land on the project list (screen %v, quitting %v)", got.screen, got.quitting)
	}

	next, _ = got.handleProjectsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !next.(Model).quitting {
		t.Error("q from the project list should quit")
	}
}

func TestZeroKeyOpensMergedView(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenOverview

	next, _ := m.handleOverviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("0")})
	got := next.(Model)
	if !got.onAllTab() {
		t.Errorf("0 should open the merged view, landed on tab %d", got.kindIdx)
	}
}

func TestDKeyDescribesSelectedResource(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenResources
	m.width, m.height = 100, 30

	next, _ := m.handleResourcesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got := next.(Model)
	if got.screen != screenDetail {
		t.Errorf("d should describe the selected row, landed on %v", got.screen)
	}
}

// --- command mode ---

func typeCommand(t *testing.T, m Model, line string) Model {
	t.Helper()
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	m = next.(Model)
	if !m.commanding {
		t.Fatal(": should enter command mode")
	}
	for _, r := range line {
		next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(Model)
}

func TestCommandModeJumpsToKind(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenOverview

	got := typeCommand(t, m, "gke")
	if got.screen != screenResources || got.currentKind().ID != "gke" {
		t.Errorf(":gke landed on screen %v kind %q, want the gke table", got.screen, got.currentKind().ID)
	}

	got = typeCommand(t, got, "all")
	if !got.onAllTab() {
		t.Errorf(":all landed on tab %d, want the merged view", got.kindIdx)
	}

	got = typeCommand(t, got, "projects")
	if got.screen != screenProjects {
		t.Errorf(":projects landed on %v, want the project list", got.screen)
	}
}

func TestCommandModeQuitAndUnknown(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenResources

	got := typeCommand(t, m, "nonsense")
	if got.quitting || got.screen != screenResources {
		t.Errorf("an unknown command should stay put (screen %v, quitting %v)", got.screen, got.quitting)
	}

	got = typeCommand(t, m, "q")
	if !got.quitting {
		t.Error(":q should quit")
	}
}

func TestCommandModeEscCancels(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenResources

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	m = next.(Model)
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if m.commanding {
		t.Error("esc should leave command mode")
	}
	if m.screen != screenResources {
		t.Errorf("cancelling a command moved screens to %v", m.screen)
	}
}

func TestCommandModeKindMatchingByPrefix(t *testing.T) {
	m := populatedModel(t)

	tests := []struct {
		word string
		want string
	}{
		{"vm", "vm"},
		{"gke", "gke"},
		{"gcs", "gcs"},
		// Five kinds now start with "data" — dataproc, dataprocjobs, dataflow,
		// datastream, datafusion — so this expectation is load-bearing on display
		// order rather than on the word. It failed the moment the new data kinds
		// were registered ahead of Dataproc; the fix was the ordering, not this.
		{"data", "dataproc"},
		{"comp", "composer"},
		{"storage", "gcs"}, // title prefix
		{"all", "all"},
	}
	for _, tc := range tests {
		idx, ok := m.matchKind(tc.word)
		if !ok {
			t.Errorf("matchKind(%q) found nothing", tc.word)
			continue
		}
		if got := m.tabs()[idx].ID; got != tc.want {
			t.Errorf("matchKind(%q) = %s, want %s", tc.word, got, tc.want)
		}
	}
}

// TestCommandModeIDsBeatTitles pins the precedence, which is the only reason
// `:comp` still reaches Composer. "Compute Disks" and "Compute Instances" both
// begin with "comp" and both sort ahead of Composer in the display order, so a
// single pass over ids and titles together hands the word to whichever kind was
// registered first — and that answer changes every time a kind is added.
func TestCommandModeIDsBeatTitles(t *testing.T) {
	m := populatedModel(t)

	tests := []struct {
		word string
		want string
	}{
		// An id prefix beats a title prefix, however early the title sits.
		{"comp", "composer"},
		// An exact id beats a shorter id's prefix match: `vm` is the whole id
		// of Compute Instances and also the start of `vmdisks`.
		{"vm", "vm"},
		// Nothing claims this as an id, so titles get their turn.
		{"compute d", "disks"},
	}
	for _, tc := range tests {
		idx, ok := m.matchKind(tc.word)
		if !ok {
			t.Errorf("matchKind(%q) found nothing", tc.word)
			continue
		}
		if got := m.tabs()[idx].ID; got != tc.want {
			t.Errorf("matchKind(%q) = %s, want %s", tc.word, got, tc.want)
		}
	}
}

func TestCommandModeMatchesNothingSensibly(t *testing.T) {
	m := populatedModel(t)

	tests := []string{"", "zzzz", "not-a-kind"}
	for _, word := range tests {
		if idx, ok := m.matchKind(word); ok {
			t.Errorf("matchKind(%q) = %s, want no match", word, m.tabs()[idx].ID)
		}
	}
}

func TestCommandModeKindMatchingIsCaseInsensitive(t *testing.T) {
	m := populatedModel(t)

	tests := []struct {
		word string
		want string
	}{
		{"GKE", "gke"},
		{"Storage", "gcs"},
		{"CoMpOsEr", "composer"},
	}
	for _, tc := range tests {
		idx, ok := m.matchKind(tc.word)
		if !ok {
			t.Errorf("matchKind(%q) found nothing", tc.word)
			continue
		}
		if got := m.tabs()[idx].ID; got != tc.want {
			t.Errorf("matchKind(%q) = %s, want %s", tc.word, got, tc.want)
		}
	}
}

// --- open guard ---

// TestSafeToOpenRejectsNonWebSchemes covers the one place an untrusted string
// reaches a platform handler. Console URLs are built by g9s, but a Composer
// environment's Airflow URI comes straight out of the API response, and `open`
// on macOS launches whichever application claims a scheme.
func TestSafeToOpenRejectsNonWebSchemes(t *testing.T) {
	refuse := []string{
		"",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"data:text/html;base64,PHNjcmlwdD4=",
		"vscode://file/etc/passwd",
		"ssh://box/",
		"itms-services://?action=download-manifest",
		"/etc/passwd",
		"console.cloud.google.com/compute", // scheme-less, would be treated as a path
		"https://",                         // scheme but no host
		"http://",
	}
	for _, target := range refuse {
		if safeToOpen(target) {
			t.Errorf("safeToOpen(%q) = true, want it refused", target)
		}
	}
}

func TestSafeToOpenAllowsRealLinks(t *testing.T) {
	allow := []string{
		"https://console.cloud.google.com/compute/instancesDetail/zones/us-central1-a/instances/web-01?project=p-1",
		"http://localhost:8080",
		"https://x1y2z3-dot-us-central1.composer.googleusercontent.com",
	}
	for _, target := range allow {
		if !safeToOpen(target) {
			t.Errorf("safeToOpen(%q) = false, want it allowed", target)
		}
	}
}

// TestConsoleURLsPassTheOpenGuard ties the two halves together: every URL the
// resource layer builds has to survive the check that guards the opener, or
// pressing `o` would refuse its own links.
func TestConsoleURLsPassTheOpenGuard(t *testing.T) {
	m := populatedModel(t)
	m.cache["vm"] = gcp.Result{Resources: []gcp.Resource{{
		Name:       "web-01",
		Row:        []string{"web-01"},
		ConsoleURL: "https://console.cloud.google.com/compute/instancesDetail/zones/us-central1-a/instances/web%2001?project=p-1",
	}}}

	for _, r := range m.mergedResources() {
		if r.ConsoleURL != "" && !safeToOpen(r.ConsoleURL) {
			t.Errorf("console URL %q would be refused by the open guard", r.ConsoleURL)
		}
	}
}

func TestStatusStyleCoversCloudSQLStates(t *testing.T) {
	// Cloud SQL uses its own vocabulary: RUNNABLE rather than RUNNING, and
	// PENDING_CREATE/PENDING_DELETE rather than CREATING/DELETING. Falling
	// through to the default style would render a healthy database the same
	// colourless grey as an unrecognised state.
	if statusStyle("RUNNABLE").GetForeground() != statusStyle("RUNNING").GetForeground() {
		t.Error("RUNNABLE should read as healthy, like RUNNING")
	}
	for _, s := range []string{"PENDING_CREATE", "PENDING_DELETE", "MAINTENANCE"} {
		if statusStyle(s).GetForeground() != statusStyle("CREATING").GetForeground() {
			t.Errorf("%s should read as in-flight", s)
		}
	}
	for _, s := range []string{"SUSPENDED", "FAILED"} {
		if statusStyle(s).GetForeground() != statusStyle("ERROR").GetForeground() {
			t.Errorf("%s should read as needing attention", s)
		}
	}
}

func TestStatusStyleCoversNetworkingStates(t *testing.T) {
	// Each networking API has its own vocabulary for the same three ideas.
	// Falling through to the default style would render a healthy VPN tunnel
	// and a failed one identically.
	healthy := statusStyle("RUNNING").GetForeground()
	inflight := statusStyle("CREATING").GetForeground()
	broken := statusStyle("ERROR").GetForeground()

	for _, s := range []string{"ESTABLISHED", "ENABLED"} {
		if statusStyle(s).GetForeground() != healthy {
			t.Errorf("%s should read as healthy", s)
		}
	}
	for _, s := range []string{
		"WAITING_FOR_FULL_CONFIG", "FIRST_HANDSHAKE", "ALLOCATING_RESOURCES", "DEPROVISIONING",
		"PARTNER_REQUEST_RECEIVED", "PENDING_CUSTOMER",
		// Inert rather than failing, but worth drawing the eye to: a firewall
		// rule or interconnect attachment that carries nothing.
		"DISABLED",
	} {
		if statusStyle(s).GetForeground() != inflight {
			t.Errorf("%s should read as in-flight/attention", s)
		}
	}
	for _, s := range []string{
		"AUTHORIZATION_ERROR", "NEGOTIATION_FAILURE", "NETWORK_ERROR",
		"NO_INCOMING_PACKETS", "REJECTED", "DEFUNCT", "UNPROVISIONED",
	} {
		if statusStyle(s).GetForeground() != broken {
			t.Errorf("%s should read as needing attention", s)
		}
	}
}

func TestTabWindowShowsEverythingWhenItFits(t *testing.T) {
	widths := []int{10, 10, 10}
	start, end, more := tabWindow(widths, 1, 100)
	if start != 0 || end != 3 {
		t.Errorf("window = [%d,%d), want the whole strip", start, end)
	}
	if more.left || more.right {
		t.Error("no overflow markers when everything fits")
	}
}

func TestTabWindowAlwaysContainsActiveTab(t *testing.T) {
	// Thirteen kinds do not fit across a normal terminal, so the strip scrolls.
	// If it ever excluded the active tab there would be no visual indication of
	// which table is on screen.
	widths := make([]int, 14)
	for i := range widths {
		widths[i] = 20
	}

	for active := 0; active < len(widths); active++ {
		start, end, _ := tabWindow(widths, active, 132)
		if active < start || active >= end {
			t.Errorf("active %d fell outside window [%d,%d)", active, start, end)
		}
	}
}

func TestTabWindowFitsTheBudgetAndMarksOverflow(t *testing.T) {
	widths := make([]int, 14)
	for i := range widths {
		widths[i] = 20
	}

	start, end, more := tabWindow(widths, 0, 132)
	used := 0
	for i := start; i < end; i++ {
		used += widths[i]
	}
	if used > 132 {
		t.Errorf("window uses %d cells, want it inside 132", used)
	}
	// At the far left there is nothing hidden to the left, but plenty right.
	if more.left {
		t.Error("no left overflow expected at the first tab")
	}
	if !more.right {
		t.Error("right overflow marker expected with 14 tabs at width 132")
	}

	_, _, lastMore := tabWindow(widths, len(widths)-1, 132)
	if !lastMore.left || lastMore.right {
		t.Errorf("at the last tab want left overflow only, got %+v", lastMore)
	}
}

func TestTabWindowSurvivesDegenerateInput(t *testing.T) {
	if start, end, _ := tabWindow(nil, 0, 80); start != 0 || end != 0 {
		t.Errorf("empty widths gave [%d,%d)", start, end)
	}
	// A single tab wider than the terminal must still render, not vanish.
	if start, end, _ := tabWindow([]int{500}, 0, 80); start != 0 || end != 1 {
		t.Errorf("oversized single tab gave [%d,%d), want [0,1)", start, end)
	}
	// An out-of-range active index must not panic or produce an empty strip.
	if start, end, _ := tabWindow([]int{10, 10}, 99, 80); start >= end {
		t.Errorf("out-of-range active gave empty window [%d,%d)", start, end)
	}
}

func TestTabBarFitsTerminalWidthWithEveryKind(t *testing.T) {
	// The end-to-end version: the rendered strip must not overrun the terminal,
	// because a bar that overflows wraps into the table below it.
	m := populatedModel(t)
	m.screen = screenResources

	for _, width := range []int{80, 100, 132, 200} {
		m.width = width
		for idx := 0; idx < len(m.tabs()); idx++ {
			m.kindIdx = idx
			if got := lipgloss.Width(m.tabsView()); got > width {
				t.Errorf("width=%d tab=%d: strip rendered %d cells", width, idx, got)
			}
		}
	}
}
