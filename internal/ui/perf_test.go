package ui

import (
	"fmt"
	"testing"

	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

// bigModel fills every kind with rows, which is what the merged tab has to
// assemble on each frame.
func bigModel(perKind int) Model {
	cfg := &config.Config{Projects: []config.Project{{Name: "prod", ProjectID: "p-1"}}}
	m := New(cfg, nil)
	m.width, m.height = 160, 50
	m.active, m.hasActive = cfg.Projects[0], true
	m.screen = screenResources
	for _, l := range m.listers {
		k := l.Kind()
		rows := make([]gcp.Resource, perKind)
		for i := range rows {
			row := make([]string, len(k.Columns))
			for c := range row {
				row[c] = fmt.Sprintf("value-%d-%d", i, c)
			}
			rows[i] = gcp.Resource{
				Name: fmt.Sprintf("res-%d", i), Location: "us-central1-a",
				Status: "RUNNING", Row: row, KindID: k.ID,
			}
		}
		m.cache[k.ID] = gcp.Result{Resources: rows}
	}
	return m
}

func BenchmarkViewOneKind(b *testing.B) {
	m := bigModel(200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

// The merged tab is the expensive one: it concatenates every kind's rows.
func BenchmarkViewAllTab(b *testing.B) {
	m := bigModel(200)
	m.kindIdx = m.allTabIdx()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

// Typing is what lagged: every keystroke is a new query, so the filter re-runs
// even though the assembled table has not changed.
func BenchmarkViewWhileTyping(b *testing.B) {
	m := bigModel(200)
	m.kindIdx = m.allTabIdx()
	queries := []string{"r", "re", "res", "res-", "res-1", "res-12", "res-123"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.filter.SetValue(queries[i%len(queries)])
		_ = m.View()
	}
}

// --- the memoisation must never serve rows that no longer exist ---

// A cache that survives new data is worse than no cache: the table would show
// a listing that has already been replaced, with no sign that it had.
func TestArrivingRowsInvalidateTheMemoisedTable(t *testing.T) {
	m := bigModel(5)
	kindID := m.currentKind().ID

	if got := len(m.visibleResources()); got != 5 {
		t.Fatalf("got %d rows, want 5", got)
	}

	next, _ := m.handleResources(resourcesMsg{
		project: m.active.Name,
		kind:    kindID,
		token:   m.refreshToken[kindID],
		result:  gcp.Result{Resources: []gcp.Resource{{Name: "only-one", Row: []string{"only-one"}}}},
	})
	m = next.(Model)

	rows := m.visibleResources()
	if len(rows) != 1 || rows[0].Name != "only-one" {
		t.Errorf("stale rows survived a refresh: %d rows, first %q", len(rows), rows[0].Name)
	}
}

func TestSwitchingTabsInvalidatesTheMemoisedTable(t *testing.T) {
	m := bigModel(5)
	first := m.currentKind().ID
	if len(m.visibleResources()) != 5 {
		t.Fatal("setup")
	}

	// The merged tab has every kind's rows, so a cache keyed only on the
	// generation would hand back the previous tab's five.
	m.kindIdx = m.allTabIdx()
	merged := m.visibleResources()
	if len(merged) != 5*len(m.listers) {
		t.Errorf("merged tab shows %d rows, want %d — the previous tab's rows were reused",
			len(merged), 5*len(m.listers))
	}

	m.kindIdx = 0
	if got := len(m.visibleResources()); got != 5 {
		t.Errorf("back on %s the table shows %d rows, want 5", first, got)
	}
}

func TestFilterChangesRecompute(t *testing.T) {
	m := bigModel(20)

	all := len(m.visibleResources())
	if all != 20 {
		t.Fatalf("got %d rows unfiltered, want 20", all)
	}

	m.filter.SetValue("value-1-")
	narrowed := m.visibleResources()
	if len(narrowed) == 0 || len(narrowed) >= all {
		t.Errorf("filter matched %d of %d rows, want a strict subset", len(narrowed), all)
	}

	// Clearing has to restore every row, not the last filtered set.
	m.filter.SetValue("")
	if got := len(m.visibleResources()); got != all {
		t.Errorf("clearing the filter left %d rows, want %d", got, all)
	}
}

// The cached and uncached paths have to agree, or behaviour depends on how the
// model was constructed.
func TestMemoisedFilterMatchesTheDirectPath(t *testing.T) {
	m := bigModel(30)
	m.kindIdx = m.allTabIdx()

	for _, query := range []string{"", "res-1", "RES-1", "us-central1-a", "nothing-matches-this"} {
		m.filter.SetValue(query)
		m.rows = &rowCache{} // force a cold cache each time
		cached := m.visibleResources()

		direct := filterResources(m.resourcesFor(allKind.ID), lower(query))
		if len(cached) != len(direct) {
			t.Errorf("query %q: memoised %d rows, direct %d", query, len(cached), len(direct))
		}
	}
}

func lower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + 32
		}
	}
	return string(out)
}
