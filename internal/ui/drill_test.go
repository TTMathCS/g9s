package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"cloud.google.com/go/container/apiv1/containerpb"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

// drillModel is parked on the GKE table with one cluster that has node pools,
// which is the only shipped kind whose rows have something underneath them
// without a second API call.
func drillModel(t *testing.T) Model {
	t.Helper()

	cfg := &config.Config{Projects: []config.Project{{Name: "sandbox", ProjectID: "sandbox-123"}}}
	m := New(cfg, nil)
	m.width, m.height = 132, 30
	m.active, m.hasActive = cfg.Projects[0], true
	m.authStatus["sandbox"] = auth.Status{State: auth.StateValid}
	m.screen = screenResources
	m.kindIdx = gkeTabIndex(t, m)
	m.cache["gke"] = gcp.Result{Resources: []gcp.Resource{{
		Name:     "batch-cluster",
		Location: "us-central1",
		Status:   "RUNNING",
		KindID:   "gke",
		Row:      []string{"batch-cluster", "us-central1", "Standard", "7", "1.31", "RUNNING", "9d"},
		Raw:      clusterWithPools(),
	}}}
	return m
}

func gkeTabIndex(t *testing.T, m Model) int {
	t.Helper()
	for i, k := range m.tabs() {
		if k.ID == "gke" {
			return i
		}
	}
	t.Fatal("no gke tab")
	return 0
}

func clusterWithPools() *containerpb.Cluster {
	return &containerpb.Cluster{
		Name:     "batch-cluster",
		Location: "us-central1",
		NodePools: []*containerpb.NodePool{
			{Name: "default-pool", InitialNodeCount: 2, Status: containerpb.NodePool_RUNNING,
				Config: &containerpb.NodeConfig{MachineType: "e2-standard-4"}},
			{Name: "spot-workers", InitialNodeCount: 1, Status: containerpb.NodePool_RUNNING,
				Config: &containerpb.NodeConfig{MachineType: "n2-highmem-8", Spot: true}},
		},
	}
}

func press(m Model, key string) Model {
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestEnterDrillsIntoARowWithChildren(t *testing.T) {
	m := press(drillModel(t), "enter")

	if m.drill == nil {
		t.Fatal("enter on a GKE cluster did not open its node pools")
	}
	if m.screen != screenResources {
		t.Errorf("screen = %v, want to stay on the table", m.screen)
	}
	if got := m.currentKind().Title; got != "Node Pools" {
		t.Errorf("current kind = %q, want the child's", got)
	}
	// The cache key has to carry the parent, or two clusters' node pools
	// overwrite each other and the second one opened shows the first one's.
	if got := m.currentKind().ID; got != "nodepools/batch-cluster" {
		t.Errorf("cache key = %q, want it qualified by the parent", got)
	}
}

func TestEnterStillDescribesARowWithoutChildren(t *testing.T) {
	m := drillModel(t)
	// Compute instances have no drill-down, so enter has to keep meaning what
	// it always did rather than doing nothing.
	m.kindIdx = 0
	m.cache["vm"] = gcp.Result{Resources: []gcp.Resource{{
		Name: "web-01", KindID: "vm", Row: []string{"web-01", "us-central1-a", "n2", "10.0.0.1", "-", "RUNNING", "1d"},
	}}}

	m = press(m, "enter")
	if m.drill != nil {
		t.Error("a VM row opened a drill-down")
	}
	if m.screen != screenDetail {
		t.Errorf("screen = %v, want the describe pane", m.screen)
	}
}

func TestDescribeStillReachesTheParentItself(t *testing.T) {
	// enter drills, so d is the only way left to see the cluster's own YAML.
	// If it drilled too, the parent would be undescribable.
	m := press(drillModel(t), "d")

	if m.drill != nil {
		t.Error("d opened a drill-down")
	}
	if m.screen != screenDetail {
		t.Fatalf("screen = %v, want the describe pane", m.screen)
	}
	if m.detailRes.Name != "batch-cluster" {
		t.Errorf("described %q, want the cluster", m.detailRes.Name)
	}
}

func TestEscLeavesTheDrillForItsParentTable(t *testing.T) {
	m := drillModel(t)
	m.cursor = 0
	m.filter.SetValue("batch")

	m = press(m, "enter")
	if m.drill == nil {
		t.Fatal("did not drill in")
	}
	// The child listing gets its own empty filter: a query typed for clusters
	// matches nothing among node pools and would read as an empty pool list.
	if got := m.filter.Value(); got != "" {
		t.Errorf("filter inside the drill = %q, want it cleared", got)
	}

	m = press(m, "esc")
	if m.drill != nil {
		t.Fatal("esc did not leave the drill-down")
	}
	if m.screen != screenResources {
		t.Errorf("screen = %v, want the parent table — esc is enter's opposite", m.screen)
	}
	if got := m.filter.Value(); got != "batch" {
		t.Errorf("filter after coming back = %q, want the parent's restored", got)
	}
}

func TestSwitchingKindsAbandonsTheDrill(t *testing.T) {
	m := press(drillModel(t), "enter")
	if m.drill == nil {
		t.Fatal("did not drill in")
	}

	// Pressing a kind hotkey names a table; landing anywhere else is wrong.
	m = press(m, "1")
	if m.drill != nil {
		t.Error("a kind hotkey left the drill-down open underneath")
	}
	if got := m.currentKind().ID; got != "vm" {
		t.Errorf("current kind = %q, want vm", got)
	}
}

func TestDrillIsNotTheMergedTable(t *testing.T) {
	// onAllTab drives loadCurrentIfEmpty, which would otherwise try to fill in
	// every kind instead of loading the child.
	m := drillModel(t)
	m.kindIdx = m.allTabIdx()
	m.cache["gke"] = gcp.Result{Resources: []gcp.Resource{{
		Name: "batch-cluster", KindID: "gke", Row: []string{"batch-cluster"}, Raw: clusterWithPools(),
	}}}

	if !m.onAllTab() {
		t.Fatal("not on the merged tab to begin with")
	}
	m = press(m, "enter")
	if m.drill == nil {
		t.Fatal("enter on a cluster row in the merged table did not drill in")
	}
	if m.onAllTab() {
		t.Error("the drill-down still reports itself as the merged table")
	}
}

func TestDrillRowsRenderWithTheChildsColumns(t *testing.T) {
	m := press(drillModel(t), "enter")
	m.cache[m.currentKind().ID] = gcp.Result{Resources: []gcp.Resource{
		{Name: "default-pool", Status: "RUNNING", Row: []string{"default-pool", "e2-standard-4", "6", "off", "1.31", "upgrade+repair", "RUNNING"}},
	}}

	view := m.View()
	if !strings.Contains(view, "MACHINE TYPE") {
		t.Errorf("the node pool columns are not rendered:\n%s", view)
	}
	if !strings.Contains(view, "default-pool") {
		t.Errorf("the node pool row is not rendered:\n%s", view)
	}
	// The trail is the only thing on screen naming which cluster these are.
	if !strings.Contains(view, "batch-cluster") {
		t.Errorf("the drill trail does not name the parent:\n%s", view)
	}
	if !strings.Contains(view, "Node Pools") {
		t.Errorf("the drill trail does not name the child listing:\n%s", view)
	}
}

func TestEveryChildHasAParentThatExists(t *testing.T) {
	// A drill-down whose ParentKind matches no lister is unreachable — enter
	// would never find it, and nothing else would say so.
	ids := map[string]bool{}
	for _, l := range gcp.Listers() {
		ids[l.Kind().ID] = true
	}
	for _, c := range gcp.Children() {
		if !ids[c.ParentKind()] {
			t.Errorf("child %q hangs off %q, which is not a registered kind",
				c.Kind().ID, c.ParentKind())
		}
	}
}

func TestChildKindIDsDoNotCollideWithKinds(t *testing.T) {
	// Cache keys are kind ids. A child sharing one with a top-level kind would
	// have them overwrite each other.
	ids := map[string]bool{allKind.ID: true}
	for _, l := range gcp.Listers() {
		ids[l.Kind().ID] = true
	}
	for _, c := range gcp.Children() {
		if ids[c.Kind().ID] {
			t.Errorf("child kind id %q is already a top-level kind", c.Kind().ID)
		}
		ids[c.Kind().ID] = true
	}
}

func TestDrillLoadsThroughTheOrdinaryFetchPath(t *testing.T) {
	// The claim the whole design rests on: a bound child is a Lister, so it
	// goes through startLoad, the refresh token, the cache and the error path
	// with nothing below the UI aware a parent is involved.
	m := drillModel(t)
	m = press(m, "enter")
	if m.drill == nil {
		t.Fatal("did not drill in")
	}

	id := m.currentKind().ID
	lister, ok := m.currentLister()
	if !ok {
		t.Fatal("the drill-down has no lister, so r would refresh the wrong table")
	}

	// Run it the way listResources does, and feed the result back in the way
	// the message loop does.
	result, err := lister.List(t.Context(), m.cfg, m.active, nil)
	if err != nil {
		t.Fatalf("drill listing: %v", err)
	}
	m.refreshToken[id] = 1
	next, _ := m.Update(resourcesMsg{project: "sandbox", kind: id, token: 1, result: result})
	m = next.(Model)

	if got := len(m.visibleResources()); got != 2 {
		t.Fatalf("the table shows %d node pools, want the 2 from the cluster", got)
	}
	if m.visibleResources()[0].Name != "default-pool" {
		t.Errorf("first row = %q", m.visibleResources()[0].Name)
	}
}

func TestDrillErrorsRenderLikeAnyOtherListingFailure(t *testing.T) {
	m := press(drillModel(t), "enter")
	id := m.currentKind().ID

	m.loadErr[id] = errTest{}
	if got := m.resourcesView(); !strings.Contains(got, "failed to list Node Pools") {
		t.Errorf("a failed drill-down does not report itself:\n%s", got)
	}
}

// sqlDrillModel is parked on the Cloud SQL table, the one kind whose rows offer
// two listings — which is the case the sibling machinery exists for.
func sqlDrillModel(t *testing.T) Model {
	t.Helper()

	cfg := &config.Config{Projects: []config.Project{{Name: "sandbox", ProjectID: "sandbox-123"}}}
	m := New(cfg, nil)
	m.width, m.height = 132, 30
	m.active, m.hasActive = cfg.Projects[0], true
	m.authStatus["sandbox"] = auth.Status{State: auth.StateValid}
	m.screen = screenResources
	for i, k := range m.tabs() {
		if k.ID == "sql" {
			m.kindIdx = i
		}
	}
	m.cache["sql"] = gcp.Result{Resources: []gcp.Resource{{
		Name:     "orders-primary",
		Location: "us-central1",
		Status:   "RUNNABLE",
		KindID:   "sql",
		Row:      []string{"orders-primary", "us-central1", "POSTGRES_15", "db-custom-4", "REGIONAL", "RUNNABLE", "200d"},
		Raw:      &sqladmin.DatabaseInstance{Name: "orders-primary", Region: "us-central1"},
	}}}
	return m
}

func TestEnterOpensTheFirstOfSeveralListings(t *testing.T) {
	m := press(sqlDrillModel(t), "enter")

	if m.drill == nil {
		t.Fatal("enter on a Cloud SQL instance did not drill in")
	}
	if got := len(m.drill.siblings); got != 2 {
		t.Fatalf("the drill knows about %d listings, want both", got)
	}
	if got := m.currentKind().Title; got != "Databases" {
		t.Errorf("opened %q, want the first listing", got)
	}
}

func TestTabMovesBetweenSiblingListings(t *testing.T) {
	m := press(sqlDrillModel(t), "enter")

	m = press(m, "tab")
	if m.drill == nil {
		t.Fatal("tab left the drill-down instead of moving inside it")
	}
	if got := m.currentKind().Title; got != "Users" {
		t.Errorf("after tab the listing is %q, want Users", got)
	}
	// Each sibling caches separately, or the second would show the first's rows.
	if got := m.currentKind().ID; got != "sqlusers/orders-primary" {
		t.Errorf("cache key = %q", got)
	}

	// And it wraps, the same way the tab strip does.
	m = press(m, "tab")
	if got := m.currentKind().Title; got != "Databases" {
		t.Errorf("tab did not wrap: %q", got)
	}
}

func TestShiftTabMovesBackBetweenSiblings(t *testing.T) {
	m := press(sqlDrillModel(t), "enter")
	m = press(m, "shift+tab")

	if m.drill == nil {
		t.Fatal("shift+tab left the drill-down")
	}
	if got := m.currentKind().Title; got != "Users" {
		t.Errorf("shift+tab from the first listing = %q, want the last", got)
	}
}

func TestTabStillLeavesADrillWithOneListing(t *testing.T) {
	// A GKE cluster offers only node pools, so there is nothing to move
	// between and tab has to keep its ordinary meaning rather than doing
	// nothing at all.
	m := press(drillModel(t), "enter")
	if len(m.drill.siblings) != 1 {
		t.Fatalf("node pools should be the only listing, got %d", len(m.drill.siblings))
	}

	m = press(m, "tab")
	if m.drill != nil {
		t.Error("tab stayed in a drill-down that has nowhere to move")
	}
}

func TestEscFromASiblingReturnsToTheParentTable(t *testing.T) {
	// Not to the first sibling: esc means "out", at whichever listing you
	// happen to be on.
	m := press(sqlDrillModel(t), "enter")
	m = press(m, "tab")
	m = press(m, "esc")

	if m.drill != nil {
		t.Fatal("esc from the second listing did not leave the drill")
	}
	if got := m.currentKind().ID; got != "sql" {
		t.Errorf("landed on %q, want the parent table", got)
	}
}

func TestTheTrailNamesEverySiblingListing(t *testing.T) {
	// The second listing has to be discoverable, not something you only find
	// by already knowing tab reaches it.
	m := press(sqlDrillModel(t), "enter")
	m.cache[m.currentKind().ID] = gcp.Result{Resources: []gcp.Resource{
		{Name: "orders", Row: []string{"orders", "UTF8", "en_US.UTF8"}},
	}}

	view := m.View()
	for _, want := range []string{"orders-primary", "Databases", "Users", "tab switch"} {
		if !strings.Contains(view, want) {
			t.Errorf("the trail does not mention %q:\n%s", want, view)
		}
	}
}

func TestHintNamesBothListingsBeforeYouPressAnything(t *testing.T) {
	m := sqlDrillModel(t)
	if got := m.keyHint(); !strings.Contains(got, "databases / users") {
		t.Errorf("hint = %q, want both listings named", got)
	}
}
