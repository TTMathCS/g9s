package ui

import (
	"fmt"
	"sort"
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
	case screenOverview:
		body = m.overviewView()
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

	// Credential state stays visible while working inside a project — the
	// token dying mid-session is routine with a federated IdP, and the header
	// is where you glance before starting something long.
	badge := ""
	if m.hasActive && m.screen != screenProjects {
		if status, ok := m.authStatus[m.active.Name]; ok {
			badge = "  " + credentialBadge(status, true)
		}
	}

	crumbWidth := max(10, m.width-lipgloss.Width(title)-lipgloss.Width(badge)-4)
	line := lipgloss.JoinHorizontal(lipgloss.Left, title, crumbStyle.Render(truncate(crumb, crumbWidth)), badge)

	if m.screen == screenProjects || m.screen == screenHelp || m.screen == screenOverview {
		return line + "\n"
	}
	// Inside a drill-down the tab strip would be a lie: none of those tabs is
	// what the table below is showing. A trail naming the row you came in on
	// takes its place, and is the only thing on screen that says which parent
	// these rows belong to.
	if m.drill != nil {
		return line + "\n" + m.drillCrumbView()
	}
	return line + "\n" + m.tabsView()
}

// drillCrumbView renders "‹ GKE Clusters · batch-cluster › Node Pools (2)".
func (m Model) drillCrumbView() string {
	d := m.drill
	trail := tabStyle.Render("‹ " + d.parentKind.Title + " · " + d.parent.Name)

	kind := d.lister.Kind()
	label := kind.Title
	if n, known := m.tabCount(kind); known {
		label += fmt.Sprintf(" (%d)", n)
	}
	if m.tabLoading(kind) {
		label += " …"
	}

	return lipgloss.JoinHorizontal(lipgloss.Bottom,
		trail, tabActiveStyle.Render(label), mutedStyle.Render("  esc back"))
}

func (m Model) tabsView() string {
	tabs := m.tabs()

	labels := make([]string, len(tabs))
	widths := make([]int, len(tabs))
	for i, kind := range tabs {
		// Each tab carries the key that jumps to it, so the strip doubles as
		// the legend for a scheme nobody should have to memorise.
		label := m.tabKey(i) + " " + kind.Title

		if n, known := m.tabCount(kind); known {
			label += fmt.Sprintf(" (%d)", n)
		}
		if m.tabLoading(kind) {
			label += " …"
		}
		labels[i] = label
		// Both tab styles pad by one cell on each side.
		widths[i] = lipgloss.Width(label) + 2
	}

	start, end, more := tabWindow(widths, m.kindIdx, m.width)

	parts := make([]string, 0, end-start+2)
	if more.left {
		parts = append(parts, tabStyle.Render("‹"))
	}
	for i := start; i < end; i++ {
		if i == m.kindIdx {
			parts = append(parts, tabActiveStyle.Render(labels[i]))
		} else {
			parts = append(parts, tabStyle.Render(labels[i]))
		}
	}
	if more.right {
		parts = append(parts, tabStyle.Render("›"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Bottom, parts...)
}

// overflow records which sides of the tab strip have tabs scrolled out of view.
type overflow struct{ left, right bool }

// tabWindow picks the run of tabs to show so that the strip fits the terminal
// and always contains the active one.
//
// Thirteen kinds do not fit across 132 columns, and a bar that overflows wraps
// into the table or gets cut off mid-label. So the strip scrolls: it grows
// outward from the active tab, and marks the sides that still have tabs on them
// so it never looks like the list simply ends.
func tabWindow(widths []int, active, available int) (start, end int, more overflow) {
	if len(widths) == 0 {
		return 0, 0, overflow{}
	}
	if active < 0 || active >= len(widths) {
		active = 0
	}

	total := 0
	for _, w := range widths {
		total += w
	}
	// Everything fits, so show everything — the common case below ten kinds.
	if available <= 0 || total <= available {
		return 0, len(widths), overflow{}
	}

	// Reserve room for the two overflow markers, each one cell plus padding.
	budget := available - 6
	if budget < widths[active] {
		// Even the active tab alone overruns; show it and let it truncate
		// rather than rendering an empty strip.
		return active, active + 1, overflow{left: active > 0, right: active < len(widths)-1}
	}

	start, end = active, active+1
	used := widths[active]
	// Alternate outwards so the active tab stays roughly centred.
	for {
		grew := false
		if end < len(widths) && used+widths[end] <= budget {
			used += widths[end]
			end++
			grew = true
		}
		if start > 0 && used+widths[start-1] <= budget {
			start--
			used += widths[start]
			grew = true
		}
		if !grew {
			break
		}
	}
	return start, end, overflow{left: start > 0, right: end < len(widths)}
}

// tabCount reports a tab's resource count, and whether it is known yet. The
// merged tab is known once any kind has landed; showing nothing until all of
// them finish would make a fast kind look like a hang.
func (m Model) tabCount(kind gcp.Kind) (int, bool) {
	if kind.ID == allKind.ID {
		n, any := 0, false
		for _, l := range m.listers {
			if result, ok := m.cache[l.Kind().ID]; ok {
				n += len(result.Resources)
				any = true
			}
		}
		return n, any
	}
	result, ok := m.cache[kind.ID]
	return len(result.Resources), ok
}

// tabLoading reports whether a tab still has a fetch in flight.
func (m Model) tabLoading(kind gcp.Kind) bool {
	if kind.ID != allKind.ID {
		return m.loading[kind.ID]
	}
	for _, l := range m.listers {
		if m.loading[l.Kind().ID] {
			return true
		}
	}
	return false
}

// listWindow picks the slice of a list to render so that it fits in rows lines
// and always contains the cursor.
//
// All three list screens need this. Without it a list longer than the terminal
// renders every row, the body outgrows the space the header and footer left it,
// and the footer scrolls off the bottom — which the dashboard did as soon as the
// kinds outnumbered the terminal's rows, and the project list did for anyone
// with thirty projects.
func listWindow(cursor, total, rows int) (start, end int) {
	if rows <= 0 || total <= 0 {
		return 0, 0
	}
	if total <= rows {
		return 0, total
	}

	// Keep the cursor on screen, then pull the window back inside the list so
	// the last page is full rather than trailing off into blank rows.
	if cursor >= rows {
		start = cursor - rows + 1
	}
	if start > total-rows {
		start = total - rows
	}
	if start < 0 {
		start = 0
	}
	return start, min(total, start+rows)
}

// --- overview ---

// overviewView is the dashboard: every category with its count and a breakdown
// of what state those resources are in, so the shape of a project is legible
// before drilling into anything.
func (m Model) overviewView() string {
	var b strings.Builder

	tabs := m.tabs()
	labelWidth := 0
	for _, kind := range tabs {
		labelWidth = max(labelWidth, len(kind.Title))
	}

	b.WriteString(headerRowStyle.Render(
		strings.Repeat(" ", hotkeyWidth+2)+pad("CATEGORY", labelWidth)+"  "+pad("COUNT", 6)+"  "+"STATE") + "\n")

	rows := m.bodyHeight()
	start, end := listWindow(m.ovCursor, len(tabs), rows)

	for i := start; i < end; i++ {
		kind := tabs[i]
		// One cell wide for every kind, which is what the letters past the
		// ninth buy: no row shifts sideways as the list grows.
		key := m.tabKey(i)

		count := "—"
		if n, known := m.tabCount(kind); known {
			count = fmt.Sprintf("%d", n)
		}

		cursor := " "
		if i == m.ovCursor {
			cursor = "▸"
		}
		line := fmt.Sprintf("%s%s %s  %s  ", cursor, key, pad(kind.Title, labelWidth), pad(count, 6))
		if i == m.ovCursor {
			b.WriteString(selectedRowStyle.Render(line))
		} else {
			b.WriteString(rowStyle.Render(line))
		}

		// Written outside the row style so the selection highlight stops at the
		// count and the status colours survive on the selected row too.
		b.WriteString(" " + m.categoryState(kind))
		b.WriteString("\n")
	}

	for i := end - start; i < rows; i++ {
		b.WriteString("\n")
	}
	return b.String()
}

// categoryState renders the right-hand column of a dashboard row: a status
// breakdown when the data is in, and the reason it is not when it isn't.
func (m Model) categoryState(kind gcp.Kind) string {
	if m.tabLoading(kind) {
		return mutedStyle.Render("loading…")
	}
	if err, failed := m.loadErr[kind.ID]; failed {
		return badStyle.Render("failed: " + truncate(err.Error(), max(20, m.width/3)))
	}
	if _, known := m.tabCount(kind); !known {
		return mutedStyle.Render("not loaded")
	}

	resources := m.resourcesFor(kind.ID)
	if len(resources) == 0 {
		return mutedStyle.Render("none")
	}

	parts := make([]string, 0, 4)
	for _, sc := range statusCounts(resources) {
		parts = append(parts, statusStyle(sc.status).Render(fmt.Sprintf("%d %s", sc.count, sc.status)))
	}

	out := strings.Join(parts, mutedStyle.Render(" · "))
	if warnings := m.warningsFor(kind); len(warnings) > 0 {
		out += warnStyle.Render("   ⚠ " + warningCount(len(warnings)))
	}
	return out
}

// statusCount is one bucket of the status breakdown.
type statusCount struct {
	status string
	count  int
}

// statusCounts buckets resources by their own status string, most common
// first. Ties break alphabetically so the dashboard does not reshuffle
// between refreshes that return the same data.
func statusCounts(resources []gcp.Resource) []statusCount {
	counts := map[string]int{}
	for _, r := range resources {
		status := r.Status
		if status == "" {
			status = "UNKNOWN"
		}
		counts[strings.ToUpper(status)]++
	}

	out := make([]statusCount, 0, len(counts))
	for status, n := range counts {
		out = append(out, statusCount{status: status, count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].status < out[j].status
	})

	// Past a handful the row stops being scannable, which is the only thing
	// the dashboard is for.
	const maxBuckets = 4
	if len(out) > maxBuckets {
		other := 0
		for _, sc := range out[maxBuckets-1:] {
			other += sc.count
		}
		out = append(out[:maxBuckets-1], statusCount{status: "OTHER", count: other})
	}
	return out
}

// warningCount labels the warning badge.
//
// Not "scope(s) unavailable" any more: an unreachable region is the common
// warning but no longer the only one — a listing bounded by a time window or a
// row cap is also a result that is not the whole truth, and the warnings
// themselves say which it is.
func warningCount(n int) string {
	if n == 1 {
		return "1 warning"
	}
	return fmt.Sprintf("%d warnings", n)
}

// warningsFor returns the partial-listing warnings attached to a tab.
func (m Model) warningsFor(kind gcp.Kind) []string {
	if kind.ID != allKind.ID {
		return m.cache[kind.ID].Warnings
	}
	var out []string
	for _, l := range m.listers {
		out = append(out, m.cache[l.Kind().ID].Warnings...)
	}
	return out
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

	rows := m.bodyHeight()
	start, end := listWindow(m.projCursor, len(m.cfg.Projects), rows)

	for i := start; i < end; i++ {
		p := m.cfg.Projects[i]
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
	for i := end - start; i < rows; i++ {
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
		// A drill-down has one scope — its parent — so naming zones or regions
		// there would describe a sweep that is not happening.
		notice := "listing " + kind.Title + " across " + m.scopeDescription() + "…"
		if m.drill != nil {
			notice = "reading " + kind.Title + " for " + m.drill.parent.Name + "…"
		}
		return m.centeredNotice(mutedStyle.Render(notice))
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
	start, end := listWindow(m.cursor, len(visible), rows)

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
	case "dataproc", "dataprocjobs":
		return fmt.Sprintf("%d regions", len(m.cfg.DataprocRegions(m.active)))
	case "composer":
		return fmt.Sprintf("%d locations", len(m.cfg.ComposerLocations(m.active)))
	case "run", "runjobs":
		// Cloud Run's v2 API takes no location wildcard, so the sweep is only
		// ever as wide as the configured regions.
		return fmt.Sprintf("%d regions", len(m.cfg.Regions(m.active)))
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

	// A kind with no columns is a bug in that kind, but it must not take the
	// whole table down: the arithmetic below divides and indexes.
	if len(cols) == 0 {
		return nil
	}

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
	if !m.hasDetail {
		// Nothing to describe. A notice rather than an empty string, because an
		// empty body collapses the layout and pulls the footer up to the header.
		return m.centeredNotice(mutedStyle.Render("no resource selected — esc to go back"))
	}
	// The resource the pane was opened on, so the name above the YAML is always
	// the name of the YAML — even if a fetch has since moved the table.
	r := m.detailRes
	header := lipgloss.JoinHorizontal(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render(sanitizeLine(r.Name)),
		mutedStyle.Render("  "+sanitizeLine(r.Location)),
	)
	return header + "\n" + m.detail.View()
}

// --- help ---

type helpEntry struct{ keys, desc string }

// helpView renders the help panel. The content is built once on open and
// scrolled here, because it is longer than a short terminal is tall.
func (m Model) helpView() string {
	return panelStyle.Width(m.width - 2).Render(m.help.View())
}

func (m Model) helpContent() string {
	sections := []struct {
		title   string
		entries []helpEntry
	}{
		{"Navigation", []helpEntry{
			{"↑/k ↓/j", "move cursor"},
			{"g / G", "jump to top / bottom"},
			{"enter", "go in — category (dashboard), node pools / keys where a row has them, else describe"},
			{m.hotkeyLegend(), "jump straight to a resource kind — the key is printed beside it"},
			{"0 / a", "all resources, every kind in one table"},
			{"tab / shift+tab", "cycle resource kinds"},
			{"q / esc", "back up one level"},
			{"p", "back to the project list"},
			{"/", "filter rows (cleared when you switch kinds)"},
			{":", "command — :<kind> by id or title prefix, :all :projects :help :q"},
		}},
		{"Actions", []helpEntry{
			{"d", "describe as YAML (enter does too, on a row with nothing under it)"},
			{"r", "refresh current kind (all of them on the dashboard)"},
			{"o", "open (Airflow UI for Composer, Cloud Console otherwise)"},
			{"y", "copy name to clipboard (OSC 52)"},
			{"s", "SSH to the selected running VM"},
		}},
		{"Credentials", []helpEntry{
			{"l", "gcloud login for the selected project"},
			{"L", "login without a local browser (--no-browser)"},
			{"r", "re-check credentials (project list)"},
			{"", "stuck on the login URL after signing in? the browser could not reach"},
			{"", "http://localhost here — ctrl+c, then L. See README, How auth works."},
		}},
		{"General", []helpEntry{
			{"?", "toggle this help"},
			{"↑/↓", "scroll this help"},
			{"ctrl+c / :q", "quit (or q from the project list)"},
		}},
	}

	// Widest key column across every section, so the descriptions line up as a
	// single column rather than per section. The legend row grows with the
	// number of kinds, which is what makes measuring beat a fixed width.
	keyWidth := 0
	for _, s := range sections {
		for _, e := range s.entries {
			keyWidth = max(keyWidth, lipgloss.Width(e.keys))
		}
	}

	var b strings.Builder
	for _, s := range sections {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render(s.title) + "\n")
		for _, e := range s.entries {
			b.WriteString("  " + helpKeyStyle.Render(pad(e.keys, keyWidth)) + "  " + helpDescStyle.Render(e.desc) + "\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- footer ---

func (m Model) footerView() string {
	if m.filtering {
		return m.filter.View()
	}
	if m.commanding {
		return m.command.View()
	}
	if m.flashText != "" {
		return m.flashStyle().Render(truncate(m.flashText, m.width-1))
	}

	// Warnings earn the footer over the key hints: a partial listing that
	// looks complete is the one failure mode worth being loud about.
	if warnings := m.currentWarnings(); len(warnings) > 0 {
		text := fmt.Sprintf("⚠ %s: %s", warningCount(len(warnings)), strings.Join(warnings, "; "))
		return m.withPosition(warnStyle.Render(truncate(text, m.width-8)))
	}

	return m.withPosition(mutedStyle.Render(truncate(m.keyHint(), m.width-8)))
}

// withPosition right-aligns a cursor/total indicator on every list screen, so
// "where am I in this list" never needs counting rows — and so a list long
// enough to scroll says how much of it is off screen.
func (m Model) withPosition(left string) string {
	var cursor, n int
	switch m.screen {
	case screenResources:
		cursor, n = m.cursor, len(m.visibleResources())
	case screenOverview:
		cursor, n = m.ovCursor, len(m.tabs())
	case screenProjects:
		cursor, n = m.projCursor, len(m.cfg.Projects)
	default:
		return left
	}
	if n == 0 {
		return left
	}
	pos := mutedStyle.Render(fmt.Sprintf("%d/%d", min(cursor+1, n), n))
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(pos) - 1
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + pos
}

func (m Model) currentWarnings() []string {
	if !m.hasActive {
		return nil
	}
	switch m.screen {
	case screenResources:
		return m.warningsFor(m.currentKind())
	case screenOverview:
		// The dashboard already shows warnings per category; repeating the
		// whole set in the footer would just be noise.
		return nil
	default:
		return nil
	}
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
		return "enter select · l login · r re-check · : cmd · ? help · q quit"
	case screenOverview:
		return "kind keys: " + m.hotkeyLegend() + " · enter open · 0/a all resources · r refresh all · : cmd · ? help"
	case screenResources:
		// The hint names enter only where enter does something d does not, so
		// the drill-down is discoverable on exactly the tables that have one.
		if m.drill == nil && m.selectedHasChild() {
			return "enter " + m.selectedChildTitle() + " · d describe · o open · y yank · / filter · : cmd · esc back · ? help"
		}
		return "d describe · o open · s ssh · y yank · / filter · : cmd · r refresh · esc back · ? help"
	case screenDetail:
		return "↑/↓ scroll · y copy yaml · esc back"
	case screenHelp:
		return "↑/↓ scroll · esc back"
	default:
		return "esc back"
	}
}

// wrap breaks text onto lines no longer than width. Used for API error strings,
// hence the sanitizing: see sanitizeLine.
func wrap(text string, width int) string {
	text = sanitizeLine(text)
	if width <= 0 {
		return text
	}
	var b strings.Builder
	lineLen := 0
	for _, word := range strings.Fields(text) {
		// Display cells, like everywhere else: counting bytes wraps a line of
		// accented or CJK text far short of the width.
		wordLen := lipgloss.Width(word)
		if lineLen > 0 && lineLen+1+wordLen > width {
			b.WriteString("\n")
			lineLen = 0
		} else if lineLen > 0 {
			b.WriteString(" ")
			lineLen++
		}
		b.WriteString(word)
		lineLen += wordLen
	}
	return b.String()
}
