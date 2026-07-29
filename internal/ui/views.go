package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/gcp"
)

// chromeHeight is the number of lines the header and footer occupy, which the
// body has to work around.
const chromeHeight = 6

func (m Model) bodyHeight() int {
	h := m.height - chromeHeight
	if h < 3 {
		return 3
	}
	return h
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "starting…"
	}

	var body string
	switch m.screen {
	case screenProjects:
		body = m.projectsView()
	case screenResources:
		body = m.resourcesView()
	case screenDetail:
		body = m.detailView()
	case screenHelp:
		body = m.helpView()
	}

	return strings.Join([]string{m.headerView(), body, m.footerView()}, "\n")
}

// --- header ---

func (m Model) headerView() string {
	title := titleStyle.Render("g9s")

	crumb := "select a project"
	if m.hasActive {
		crumb = fmt.Sprintf("%s · %s", m.active.Name, m.active.ProjectID)
		if m.active.Account != "" {
			crumb += " · " + m.active.Account
		}
	}

	line := lipgloss.JoinHorizontal(lipgloss.Left, title, crumbStyle.Render(truncate(crumb, max(10, m.width-20))))

	if m.screen == screenProjects || m.screen == screenHelp {
		return line + "\n"
	}
	return line + "\n" + m.tabsView()
}

func (m Model) tabsView() string {
	parts := make([]string, 0, len(m.listers))
	for i, l := range m.listers {
		kind := l.Kind()
		label := fmt.Sprintf("%d %s", i+1, kind.Title)

		if count, ok := m.cache[kind.ID]; ok {
			label += fmt.Sprintf(" (%d)", len(count.Resources))
		}
		if m.loading[kind.ID] {
			label += " …"
		}

		if i == m.kindIdx {
			parts = append(parts, tabActiveStyle.Render(label))
		} else {
			parts = append(parts, tabStyle.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Bottom, parts...)
}

// --- projects ---

func (m Model) projectsView() string {
	var b strings.Builder

	nameWidth := 0
	for _, p := range m.cfg.Projects {
		nameWidth = max(nameWidth, len(p.Name))
	}
	nameWidth = min(nameWidth, 28)

	idWidth := max(12, min(30, m.width-nameWidth-34))

	b.WriteString(headerRowStyle.Render(
		"  "+pad("PROJECT", nameWidth)+"  "+pad("PROJECT ID", idWidth)+"  "+"CREDENTIALS") + "\n")

	for i, p := range m.cfg.Projects {
		status, known := m.authStatus[p.Name]

		line := "  " + pad(p.Name, nameWidth) + "  " + pad(p.ProjectID, idWidth) + "  "
		if i == m.projCursor {
			line = "▸ " + pad(p.Name, nameWidth) + "  " + pad(p.ProjectID, idWidth) + "  "
			b.WriteString(selectedRowStyle.Render(line))
		} else {
			b.WriteString(rowStyle.Render(line))
		}

		b.WriteString(" " + credentialBadge(status, known))
		b.WriteString("\n")
	}

	// Pad so the footer sits at the bottom of the terminal.
	for i := len(m.cfg.Projects) + 1; i < m.bodyHeight()+1; i++ {
		b.WriteString("\n")
	}
	return b.String()
}

func credentialBadge(s auth.Status, known bool) string {
	if !known {
		return mutedStyle.Render("checking…")
	}
	switch s.State {
	case auth.StateValid:
		return goodStyle.Render("● " + s.Summary())
	case auth.StateExpired:
		return warnStyle.Render("● " + s.Summary())
	case auth.StateMissing:
		return mutedStyle.Render("○ " + s.Summary())
	default:
		return mutedStyle.Render("checking…")
	}
}

// --- resources ---

func (m Model) resourcesView() string {
	kind := m.currentKind()

	if err, failed := m.loadErr[kind.ID]; failed {
		return m.centeredNotice(badStyle.Render("failed to list "+kind.Title) + "\n\n" + mutedStyle.Render(wrap(err.Error(), m.width-6)))
	}
	if m.loading[kind.ID] && !m.hasData(kind.ID) {
		return m.centeredNotice(mutedStyle.Render("listing " + kind.Title + " across " + m.scopeDescription() + "…"))
	}

	visible := m.visibleResources()
	if len(visible) == 0 {
		notice := mutedStyle.Render("no " + kind.Title + " found")
		if m.filter.Value() != "" {
			notice = mutedStyle.Render(fmt.Sprintf("no %s match %q", kind.Title, m.filter.Value()))
		}
		return m.centeredNotice(notice)
	}

	widths := columnWidths(kind.Columns, m.width-2)

	var b strings.Builder
	b.WriteString(headerRowStyle.Render("  " + renderRow(columnTitles(kind.Columns), widths)))
	b.WriteString("\n")

	// Scroll so the cursor stays on screen.
	rows := m.bodyHeight() - 1
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}
	end := min(len(visible), start+rows)

	for i := start; i < end; i++ {
		r := visible[i]
		if i == m.cursor {
			b.WriteString(selectedRowStyle.Render("▸ " + renderRow(r.Row, widths)))
		} else {
			b.WriteString("  " + renderStyledRow(r, widths, kind))
		}
		b.WriteString("\n")
	}

	for i := end - start; i < rows; i++ {
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) scopeDescription() string {
	switch m.currentKind().ID {
	case "dataproc":
		return fmt.Sprintf("%d regions", len(m.cfg.DataprocRegions(m.active)))
	case "composer":
		return fmt.Sprintf("%d locations", len(m.cfg.ComposerLocations(m.active)))
	default:
		return "all zones"
	}
}

func (m Model) centeredNotice(text string) string {
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.bodyHeight()).
		Align(lipgloss.Center, lipgloss.Center).
		Render(text)
}

func columnTitles(cols []gcp.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Title
	}
	return out
}

// columnWidths distributes the available width across columns in proportion to
// their declared weights, with a floor so no column collapses entirely.
func columnWidths(cols []gcp.Column, available int) []int {
	const gap = 2
	const minWidth = 6

	available -= gap * (len(cols) - 1)
	if available < minWidth*len(cols) {
		available = minWidth * len(cols)
	}

	total := 0
	for _, c := range cols {
		total += c.Width
	}
	if total == 0 {
		total = len(cols)
	}

	widths := make([]int, len(cols))
	used := 0
	for i, c := range cols {
		w := available * c.Width / total
		if w < minWidth {
			w = minWidth
		}
		widths[i] = w
		used += w
	}

	// Give any rounding remainder to the first column, which is always NAME.
	if remainder := available - used; remainder > 0 {
		widths[0] += remainder
	}
	return widths
}

func renderRow(cells []string, widths []int) string {
	parts := make([]string, 0, len(widths))
	for i := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		parts = append(parts, pad(cell, widths[i]))
	}
	return strings.Join(parts, "  ")
}

// renderStyledRow colours the status cell so the table scans at a glance.
func renderStyledRow(r gcp.Resource, widths []int, kind gcp.Kind) string {
	statusIdx := -1
	for i, c := range kind.Columns {
		if c.Title == "STATUS" || c.Title == "STATE" {
			statusIdx = i
			break
		}
	}

	parts := make([]string, 0, len(widths))
	for i := range widths {
		cell := ""
		if i < len(r.Row) {
			cell = r.Row[i]
		}
		padded := pad(cell, widths[i])
		if i == statusIdx {
			parts = append(parts, statusStyle(cell).Render(padded))
		} else {
			parts = append(parts, rowStyle.Render(padded))
		}
	}
	return strings.Join(parts, "  ")
}

// --- detail ---

func (m Model) detailView() string {
	r, ok := m.selectedResource()
	if !ok {
		return ""
	}
	header := lipgloss.JoinHorizontal(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render(r.Name),
		mutedStyle.Render("  "+r.Location),
	)
	return header + "\n" + m.detail.View()
}

// --- help ---

type helpEntry struct{ keys, desc string }

func (m Model) helpView() string {
	sections := []struct {
		title   string
		entries []helpEntry
	}{
		{"Navigation", []helpEntry{
			{"↑/k ↓/j", "move cursor"},
			{"g / G", "jump to top / bottom"},
			{"1 2 3", "switch resource kind"},
			{"tab / shift+tab", "cycle resource kinds"},
			{"p / esc", "back to project list"},
			{"enter", "describe selected resource"},
			{"/", "filter rows (esc clears)"},
		}},
		{"Actions", []helpEntry{
			{"r", "refresh current kind"},
			{"o", "open (Airflow UI for Composer, Console otherwise)"},
			{"c", "open in Cloud Console"},
			{"y", "copy name to clipboard (OSC 52)"},
			{"s", "SSH to the selected running VM"},
		}},
		{"Credentials", []helpEntry{
			{"l", "gcloud login for the selected project"},
			{"L", "login without a local browser (--no-browser)"},
			{"r", "re-check credentials (project list)"},
		}},
		{"General", []helpEntry{
			{"?", "toggle this help"},
			{"q / ctrl+c", "quit"},
		}},
	}

	var b strings.Builder
	for _, s := range sections {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render(s.title) + "\n")
		for _, e := range s.entries {
			b.WriteString("  " + helpKeyStyle.Render(pad(e.keys, 18)) + helpDescStyle.Render(e.desc) + "\n")
		}
		b.WriteString("\n")
	}
	return panelStyle.Width(m.width - 2).Render(strings.TrimRight(b.String(), "\n"))
}

// --- footer ---

func (m Model) footerView() string {
	if m.filtering {
		return m.filter.View()
	}
	if m.flashText != "" {
		return m.flashStyle().Render(truncate(m.flashText, m.width-1))
	}

	// Warnings earn the footer over the key hints: a partial listing that
	// looks complete is the one failure mode worth being loud about.
	if warnings := m.currentWarnings(); len(warnings) > 0 {
		text := fmt.Sprintf("⚠ %d scope(s) unavailable: %s", len(warnings), strings.Join(warnings, "; "))
		return warnStyle.Render(truncate(text, m.width-1))
	}

	return mutedStyle.Render(truncate(m.keyHint(), m.width-1))
}

func (m Model) currentWarnings() []string {
	if m.screen != screenResources || !m.hasActive {
		return nil
	}
	return m.cache[m.currentKind().ID].Warnings
}

func (m Model) flashStyle() lipgloss.Style {
	switch m.flashLevel {
	case flashError:
		return badStyle
	case flashWarn:
		return warnStyle
	default:
		return mutedStyle
	}
}

func (m Model) keyHint() string {
	switch m.screen {
	case screenProjects:
		return "enter select · l login · r re-check · ? help · q quit"
	case screenResources:
		return "enter describe · o open · s ssh · y yank · / filter · r refresh · p projects · ? help"
	case screenDetail:
		return "↑/↓ scroll · y copy yaml · esc back"
	default:
		return "esc back"
	}
}

// wrap breaks text onto lines no longer than width.
func wrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	var b strings.Builder
	lineLen := 0
	for _, word := range strings.Fields(text) {
		if lineLen > 0 && lineLen+1+len(word) > width {
			b.WriteString("\n")
			lineLen = 0
		} else if lineLen > 0 {
			b.WriteString(" ")
			lineLen++
		}
		b.WriteString(word)
		lineLen += len(word)
	}
	return b.String()
}
