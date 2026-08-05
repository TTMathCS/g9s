package ui

import (
	"github.com/TTMathCS/g9s/internal/gcp"
)

// overviewRow is one dashboard line.
//
// index is the row's position in tabs(), carried rather than recomputed because
// it is what the hotkey is derived from. Filtering tabs() itself would renumber
// every key below the first hidden kind, so the key for a resource would depend
// on what happened to be empty this morning — a hotkey that moves is worse than
// no hotkey.
type overviewRow struct {
	index int
	kind  gcp.Kind
}

// overviewRows is the dashboard's visible lines, and how many were left out.
//
// A project uses a handful of the services g9s knows about, so a dashboard that
// lists all of them is mostly a list of things that are not there. What is
// hidden is only ever "loaded, and there is nothing" or "nobody enabled this
// API" — the two states that carry no information.
//
// What is never hidden is a failure. "Permission denied" is the difference
// between "nothing there" and "you cannot see it", and a dashboard that
// collapses that distinction to save a line is one that quietly tells you a
// project is empty when you simply cannot read it.
func (m Model) overviewRows() (visible []overviewRow, hidden int) {
	tabs := m.tabs()
	visible = make([]overviewRow, 0, len(tabs))

	for i, kind := range tabs {
		row := overviewRow{index: i, kind: kind}
		if m.showAllKinds || !m.kindIsEmpty(kind) {
			visible = append(visible, row)
			continue
		}
		hidden++
	}

	// Never everything. A project where genuinely nothing loaded would
	// otherwise render as a blank screen with a footnote, which reads as a
	// broken dashboard rather than as an empty project.
	if len(visible) == 0 {
		return []overviewRow{{index: m.allTabIdx(), kind: allKind}}, max(0, hidden-1)
	}
	return visible, hidden
}

// kindIsEmpty reports whether a dashboard row would say nothing.
func (m Model) kindIsEmpty(kind gcp.Kind) bool {
	// The merged view is the way back to everything and is never hidden.
	if kind.ID == allKind.ID {
		return false
	}
	// Still in flight: what it holds is not known yet, and a row that vanishes
	// and comes back as the sweep lands is a dashboard that flickers.
	if m.tabLoading(kind) {
		return false
	}

	if err, failed := m.loadErr[kind.ID]; failed {
		// The one failure worth hiding: nobody enabled the service. Every other
		// failure stays, because it is a fact about this project that the row
		// is the only place to learn.
		return gcp.IsAPIDisabled(err)
	}

	if _, known := m.tabCount(kind); !known {
		// Never fetched — on the dashboard that means the sweep has not reached
		// it, which is not the same as empty.
		return false
	}
	if len(m.resourcesFor(kind.ID)) > 0 {
		return false
	}
	// Empty, but with something to say about why: a capped or partially denied
	// listing that came back with no rows is the most misleading empty there
	// is, and hiding it would delete the only warning about it.
	return len(m.warningsFor(kind)) == 0
}

// ovCursorPosition is where the cursor sits among the visible rows.
func (m Model) ovCursorPosition(rows []overviewRow) int {
	for i, row := range rows {
		if row.index == m.ovCursor {
			return i
		}
	}
	return 0
}

// moveOverviewCursor steps the cursor through visible rows only, so hidden
// kinds are skipped rather than sat on invisibly.
func (m Model) moveOverviewCursor(delta int) Model {
	rows, _ := m.overviewRows()
	if len(rows) == 0 {
		return m
	}
	pos := min(max(m.ovCursorPosition(rows)+delta, 0), len(rows)-1)
	m.ovCursor = rows[pos].index
	return m
}

// clampOverviewCursor puts the cursor on a visible row.
//
// Needed because rows appear and disappear as listings land: a kind that was
// selected while loading can be hidden the moment it comes back empty, and the
// cursor would otherwise point at a row nobody can see.
func (m Model) clampOverviewCursor() Model {
	rows, _ := m.overviewRows()
	if len(rows) == 0 {
		return m
	}
	for _, row := range rows {
		if row.index == m.ovCursor {
			return m
		}
	}
	m.ovCursor = rows[0].index
	return m
}

// toggleShowAllKinds switches between the useful rows and every row.
func (m Model) toggleShowAllKinds() Model {
	m.showAllKinds = !m.showAllKinds
	return m.clampOverviewCursor()
}
