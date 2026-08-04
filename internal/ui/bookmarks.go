package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTMathCS/g9s/internal/bookmarks"
)

// currentBookmark describes where the user is, well enough to come back.
//
// A place, not a resource: the project, the kind and the filter that was
// typed. Which rows that filter matches changes between sessions; the question
// it encodes does not, and the question is what gets retyped ten times a day.
func (m Model) currentBookmark(name string) (bookmarks.Bookmark, error) {
	b := bookmarks.Bookmark{Name: name, Filter: strings.TrimSpace(m.filter.Value())}

	switch m.screen {
	case screenFleet:
		if m.fleet == nil {
			return b, fmt.Errorf("nothing to bookmark here")
		}
		b.Kind = m.fleet.lister.Kind().ID
		b.Sweep = bookmarks.SweepFleet
		if m.fleet.compare {
			b.Sweep = bookmarks.SweepDiff
		}
	case screenResources, screenOverview:
		if !m.hasActive {
			return b, fmt.Errorf("select a project first")
		}
		// A drill-down is deliberately not bookmarkable. It is a place inside a
		// parent resource that may not exist next week, and a bookmark that
		// opens an error is worse than one that does not exist.
		if m.drill != nil {
			return b, fmt.Errorf("bookmark the parent table rather than a drill-down")
		}
		b.Project = m.active.Name
		b.Kind = m.currentKind().ID
	default:
		return b, fmt.Errorf("nothing to bookmark here")
	}

	if !b.Valid() {
		return b, fmt.Errorf("nothing to bookmark here")
	}
	return b, nil
}

// saveBookmark stores the current place under a name.
func (m Model) saveBookmark(name string) (tea.Model, tea.Cmd) {
	name = strings.TrimSpace(name)
	if name == "" {
		return m, flash("a bookmark needs a name — for example :bm prod-errors", flashWarn)
	}

	b, err := m.currentBookmark(name)
	if err != nil {
		return m, flash(err.Error(), flashWarn)
	}
	if err := m.marks.Add(b); err != nil {
		// Writing failed — a read-only home directory, a full disk. Said out
		// loud, because the alternative is somebody trusting a bookmark that
		// was never written.
		return m, flash("could not save bookmark: "+err.Error(), flashError)
	}
	return m, flash("bookmarked as "+name, flashInfo)
}

// openBookmarks shows the saved places.
func (m Model) openBookmarks() (tea.Model, tea.Cmd) {
	m.bookmarkReturn = m.screen
	m.screen = screenBookmarks
	m.bookmarkCursor = 0
	return m, nil
}

func (m Model) handleBookmarkKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	list := m.marks.All()

	switch msg.String() {
	case "q", "esc":
		m.screen = m.bookmarkReturn
		return m, nil

	case "up", "k":
		if m.bookmarkCursor > 0 {
			m.bookmarkCursor--
		}
		return m, nil

	case "down", "j":
		if m.bookmarkCursor < len(list)-1 {
			m.bookmarkCursor++
		}
		return m, nil

	case "g":
		m.bookmarkCursor = 0
		return m, nil

	case "G":
		m.bookmarkCursor = max(0, len(list)-1)
		return m, nil

	case "d", "x":
		if m.bookmarkCursor < 0 || m.bookmarkCursor >= len(list) {
			return m, nil
		}
		name := list[m.bookmarkCursor].Name
		if err := m.marks.Remove(name); err != nil {
			return m, flash("could not remove bookmark: "+err.Error(), flashError)
		}
		m.bookmarkCursor = max(0, min(m.bookmarkCursor, len(m.marks.All())-1))
		return m, flash("removed "+name, flashInfo)

	case "enter":
		if m.bookmarkCursor < 0 || m.bookmarkCursor >= len(list) {
			return m, nil
		}
		return m.openBookmark(list[m.bookmarkCursor])
	}
	return m, nil
}

// openBookmark goes where a bookmark points.
//
// Everything it names is re-resolved rather than trusted: the config it was
// saved against can have lost the project, and g9s can have dropped the kind.
// Both say which is missing rather than opening something arbitrary — a
// bookmark that silently lands somewhere else is worse than one that fails.
func (m Model) openBookmark(b bookmarks.Bookmark) (tea.Model, tea.Cmd) {
	idx, ok := m.kindIndex(b.Kind)
	if !ok {
		return m, flash(fmt.Sprintf("bookmark %q points at an unknown kind %q", b.Name, b.Kind), flashWarn)
	}

	m.screen = m.bookmarkReturn
	m.filter.SetValue(b.Filter)
	m.filtering = false

	if b.Sweep == bookmarks.SweepFleet || b.Sweep == bookmarks.SweepDiff {
		next, cmd := m.startSweep(m.listers[idx], b.Sweep == bookmarks.SweepDiff)
		// startSweep clears the filter, since a sweep of a different kind has
		// nothing to do with the query that was typed for the last one. Here
		// the filter is part of the bookmark, so it goes back afterwards.
		restored := next.(Model)
		restored.filter.SetValue(b.Filter)
		return restored, cmd
	}

	p, found := m.cfg.Project(b.Project)
	if !found {
		return m, flash(fmt.Sprintf("bookmark %q points at %q, which is not in the config",
			b.Name, b.Project), flashWarn)
	}

	if !m.hasActive || m.active.Name != p.Name {
		m.invalidate()
	}
	m.active, m.hasActive = p, true
	m.projCursor = m.projectIndex(p.Name)
	m.kindIdx = idx
	m.drill = nil
	m.screen = screenResources
	m.cursor = 0
	m.filter.SetValue(b.Filter)

	return m.loadCurrentIfEmpty()
}

// bookmarkDestination is the second column: where a bookmark goes, in the
// words the user would use to get there themselves.
func bookmarkDestination(b bookmarks.Bookmark) string {
	switch b.Sweep {
	case bookmarks.SweepFleet:
		return ":fleet " + b.Kind
	case bookmarks.SweepDiff:
		return ":diff " + b.Kind
	default:
		return b.Project + " · " + b.Kind
	}
}
