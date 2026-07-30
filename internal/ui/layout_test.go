package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func keyEsc() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEsc} }

// --- windowing ---

func TestListWindowKeepsTheCursorVisible(t *testing.T) {
	tests := []struct {
		name                string
		cursor, total, rows int
		wantStart, wantEnd  int
	}{
		{"everything fits", 0, 3, 10, 0, 3},
		{"cursor on the last visible row", 4, 20, 5, 0, 5},
		{"cursor past the window scrolls", 5, 20, 5, 1, 6},
		{"last row shows a full page", 19, 20, 5, 15, 20},
		{"cursor beyond the list is clamped", 99, 20, 5, 15, 20},
		{"no rows to draw", 0, 20, 0, 0, 0},
		{"empty list", 0, 0, 5, 0, 0},
		{"negative rows", 3, 20, -4, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := listWindow(tt.cursor, tt.total, tt.rows)
			if start != tt.wantStart || end != tt.wantEnd {
				t.Errorf("listWindow(%d, %d, %d) = [%d,%d), want [%d,%d)",
					tt.cursor, tt.total, tt.rows, start, end, tt.wantStart, tt.wantEnd)
			}
			if tt.rows > 0 && tt.total > 0 {
				if size := end - start; size > tt.rows {
					t.Errorf("window holds %d rows, more than the %d available", size, tt.rows)
				}
				cursor := min(tt.cursor, tt.total-1)
				if cursor < start || cursor >= end {
					t.Errorf("cursor %d is outside the window [%d,%d)", cursor, start, end)
				}
			}
		})
	}
}

// --- the views fit the terminal ---

// crowdedModel is deliberately more than any short terminal can show: enough
// projects and enough rows that every list screen has to scroll.
func crowdedModel(t *testing.T) Model {
	t.Helper()

	projects := make([]config.Project, 40)
	for i := range projects {
		projects[i] = config.Project{
			Name:      strings.Repeat("p", 1+i%5) + string(rune('a'+i%26)),
			ProjectID: "project-id",
		}
	}

	m := New(&config.Config{Projects: projects}, nil)
	m.active, m.hasActive = projects[0], true
	m.authStatus[projects[0].Name] = auth.Status{State: auth.StateValid}

	rows := make([]gcp.Resource, 60)
	for i := range rows {
		rows[i] = gcp.Resource{
			Name: "row", Location: "us-central1-a", Status: "RUNNING",
			Row: []string{"row", "us-central1-a", "n2-standard-4", "10.0.0.1", "-", "RUNNING", "1d"},
		}
	}
	for _, l := range m.listers {
		m.cache[l.Kind().ID] = gcp.Result{Resources: rows}
	}
	return m
}

func TestEveryViewFitsTheTerminal(t *testing.T) {
	// A body taller than the space the header and footer left it pushes the
	// footer off the bottom of the screen, which is what happened on the
	// dashboard as soon as the kinds outnumbered the terminal's rows.
	m := crowdedModel(t)

	screens := []struct {
		name   string
		screen screen
	}{
		{"projects", screenProjects},
		{"dashboard", screenOverview},
		{"resources", screenResources},
		{"detail", screenDetail},
		{"help", screenHelp},
	}

	for _, size := range []struct{ w, h int }{{80, 10}, {80, 15}, {100, 20}, {132, 24}, {200, 60}} {
		for _, s := range screens {
			m.width, m.height = size.w, size.h
			m.screen = s.screen
			m.sizeHelp()
			m.help.SetContent(m.helpContent())
			m.detail.Width, m.detail.Height = size.w-4, m.bodyHeight()
			m.detail.SetContent(strings.Repeat("a line of yaml\n", 200))
			m.detailRes, m.hasDetail = gcp.Resource{Name: "x"}, true

			// Cursors at the far end, where a windowing bug shows up.
			m.projCursor = len(m.cfg.Projects) - 1
			m.ovCursor = len(m.tabs()) - 1
			m.cursor = len(m.visibleResources()) - 1

			view := m.View()
			if lines := strings.Count(view, "\n") + 1; lines > size.h {
				t.Errorf("%s at %dx%d rendered %d lines", s.name, size.w, size.h, lines)
			}
			for i, line := range strings.Split(view, "\n") {
				if w := lipgloss.Width(line); w > size.w {
					t.Errorf("%s at %dx%d: line %d is %d cells wide", s.name, size.w, size.h, i, w)
				}
			}
		}
	}
}

func TestDashboardScrollsToTheSelectedKind(t *testing.T) {
	// The dashboard is the only place every category is listed, so a kind the
	// cursor is on must be on screen no matter how short the terminal is.
	m := crowdedModel(t)
	m.width, m.height = 132, 14
	m.screen = screenOverview

	tabs := m.tabs()
	for idx, kind := range tabs {
		m.ovCursor = idx
		if !strings.Contains(m.overviewView(), kind.Title) {
			t.Errorf("dashboard at height 14 does not show %q when its cursor is on it", kind.Title)
		}
	}
}

func TestProjectListScrollsToTheSelectedProject(t *testing.T) {
	m := crowdedModel(t)
	m.width, m.height = 132, 14
	m.screen = screenProjects

	last := len(m.cfg.Projects) - 1
	m.projCursor = last
	view := m.projectsView()
	if !strings.Contains(view, m.cfg.Projects[last].Name) {
		t.Error("the selected project scrolled off the bottom of the list")
	}
}

func TestFooterShowsPositionOnEveryListScreen(t *testing.T) {
	// With a scrolling list, the position indicator is what says there is more
	// off screen.
	m := crowdedModel(t)
	m.width, m.height = 132, 20

	for _, tt := range []struct {
		screen screen
		want   string
	}{
		{screenProjects, fmt.Sprintf("1/%d", len(m.cfg.Projects))},
		// Derived rather than hard-coded: adding a kind must not fail a test
		// about the footer.
		{screenOverview, fmt.Sprintf("1/%d", len(m.tabs()))},
	} {
		m.screen = tt.screen
		if got := m.footerView(); !strings.Contains(got, tt.want) {
			t.Errorf("footer on %v does not show %q", tt.screen, tt.want)
		}
	}
}

// --- help ---

func TestHelpScrollsInsteadOfOverflowing(t *testing.T) {
	m := crowdedModel(t)
	m.width, m.height = 100, 16

	next, _ := m.openHelp()
	m = next.(Model)

	if m.screen != screenHelp {
		t.Fatalf("openHelp left the screen at %v", m.screen)
	}
	top := m.helpView()
	if !strings.Contains(top, "Navigation") {
		t.Error("help does not start at the top")
	}
	// The last section is only reachable by scrolling at this height, which is
	// the point of the viewport.
	m.help.GotoBottom()
	if bottom := m.helpView(); !strings.Contains(bottom, "quit") {
		t.Error("scrolling to the bottom of the help does not reach the last section")
	}
}

func TestHelpReturnsWhereItWasOpened(t *testing.T) {
	m := crowdedModel(t)
	m.width, m.height = 132, 40

	for _, from := range []screen{screenProjects, screenOverview, screenResources} {
		m.screen = from
		next, _ := m.openHelp()
		opened := next.(Model)

		closed, _ := opened.handleHelpKey(keyRunes("q"))
		if got := closed.(Model).screen; got != from {
			t.Errorf("help opened from %v closed to %v", from, got)
		}
	}
}

func TestHelpFromDetailReturnsToDetail(t *testing.T) {
	m := crowdedModel(t)
	m.width, m.height = 132, 40
	m.screen = screenDetail
	m.detailRes, m.hasDetail = gcp.Resource{Name: "web-01"}, true

	next, _ := m.openHelp()
	closed, _ := next.(Model).handleHelpKey(keyEsc())
	if got := closed.(Model).screen; got != screenDetail {
		t.Errorf("help opened from the detail pane closed to %v, want back to the detail pane", got)
	}
}

func TestHelpDocumentsTheKindHotkeys(t *testing.T) {
	m := crowdedModel(t)
	content := m.helpContent()
	if legend := m.hotkeyLegend(); !strings.Contains(content, legend) {
		t.Errorf("help does not document the hotkey legend %q", legend)
	}
}
