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

// The smallest terminal the tables can say anything useful in. Below this the
// column widths collapse past the point where a row carries information, and
// the composed view runs wider and taller than the screen — which on the
// alternate screen does not clip, it scrolls, leaving a display that stays
// corrupted after the terminal grows back.
const (
	minWidth  = 40
	minHeight = 10
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "starting…"
	}
	if m.width < minWidth || m.height < minHeight {
		return m.tooSmallView()
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
	case screenLogin:
		body = m.loginView()
	case screenConfirm:
		body = m.confirmView()
	case screenFleet:
		body = m.fleetView()
	}

	return clampHeight(strings.Join([]string{m.headerView(), body, m.footerView()}, "\n"), m.height)
}

// tooSmallView is what a terminal below the usable minimum gets.
//
// Saying so plainly beats rendering a mangled table: this state is almost
// always transient — a tmux divider being dragged, a window mid-resize — and
// the useful thing is for it to be obviously a size problem and to disappear
// the moment the terminal grows. The message is built line by line against the
// real width because the one screen that must never overflow is the screen
// about overflowing.
func (m Model) tooSmallView() string {
	lines := []string{
		"terminal too small",
		fmt.Sprintf("need %dx%d", minWidth, minHeight),
		fmt.Sprintf("have %dx%d", m.width, m.height),
	}

	var kept []string
	for _, line := range lines {
		if len(kept) >= m.height {
			break
		}
		if lipgloss.Width(line) > m.width {
			// Plain text, so a rune slice is a safe cut here in a way it would
			// not be on a styled line.
			runes := []rune(line)
			if m.width <= 0 {
				continue
			}
			line = string(runes[:min(len(runes), m.width)])
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// clampHeight drops any lines past what the terminal can show.
//
// A backstop rather than the main mechanism: each view sizes its own body to
// bodyHeight, and this catches the cases where something extra — a banner, a
// wrapped warning — pushes the total past the screen. Overflow on the
// alternate screen scrolls rather than clips, so the cost of missing one is a
// display that stays wrong after the cause is gone.
func clampHeight(view string, height int) string {
	if height <= 0 {
		return view
	}
	lines := strings.Split(view, "\n")
	if len(lines) <= height {
		return view
	}
	return strings.Join(lines[:height], "\n")
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

	if m.screen == screenProjects || m.screen == screenHelp || m.screen == screenOverview || m.screen == screenLogin || m.screen == screenConfirm || m.screen == screenFleet {
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

// drillCrumbView renders "‹ GKE Clusters · batch-cluster  Node Pools (2)".
//
// Where the row offers more than one listing they all appear, the open one
// highlighted, so the second is discoverable rather than something you have to
// already know tab reaches.
func (m Model) drillCrumbView() string {
	d := m.drill
	parts := []string{tabStyle.Render("‹ " + d.parentKind.Title + " · " + d.parent.Name)}

	// openDrill always records the siblings, but the trail is the only thing
	// naming the listing on screen, so it falls back rather than render a bare
	// parent with no child beside it.
	siblings := d.siblings
	if len(siblings) == 0 {
		return lipgloss.JoinHorizontal(lipgloss.Bottom,
			parts[0], tabActiveStyle.Render(d.lister.Kind().Title), mutedStyle.Render("  esc back"))
	}

	for i, sibling := range siblings {
		// Bound to the parent, so each sibling resolves to its own cache key
		// and carries its own count — including one you tabbed away from,
		// which is the whole reason to look back at it.
		bound := gcp.BindChild(sibling, d.parent)
		if i < len(d.boundSiblings) && d.boundSiblings[i] != nil {
			bound = d.boundSiblings[i]
		}
		kind := bound.Kind()

		label := kind.Title
		if n, known := m.tabCount(kind); known {
			label += fmt.Sprintf(" (%d)", n)
			if m.cache[kind.ID].NextPageToken != "" {
				label += "+"
			}
		}
		if m.tabLoading(kind) {
			label += " …"
		}

		if i == d.siblingIdx {
			parts = append(parts, tabActiveStyle.Render(label))
			continue
		}
		parts = append(parts, tabStyle.Render(label))
	}

	hint := "  esc back"
	location := ""
	if state, ok := gcp.StorageObjectState(d.lister); ok {
		location = "gs://" + state.Bucket + "/" + state.Prefix
		if state.MatchGlob != "" {
			location += "  find " + strings.TrimPrefix(state.MatchGlob, state.Prefix)
		}
		if state.Prefix != "" || state.MatchGlob != "" {
			hint = "  esc up"
		}
	}
	if len(siblings) > 1 {
		hint = "  tab switch ·" + hint
	}
	if location != "" {
		used := 0
		for _, part := range parts {
			used += lipgloss.Width(part)
		}
		available := m.width - used - lipgloss.Width(hint) - 2
		if available >= 8 {
			parts = append(parts, mutedStyle.Render("  "+truncate(location, available-2)))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Bottom, append(parts, mutedStyle.Render(hint))...)
}

// fleetView renders one kind across every configured project.
//
// The summary line sits above the table rather than in the footer, where the
// warnings already live. A cross-project count reads as a statement about the
// estate and is only ever a statement about the projects that answered, so
// "7/12 projects read · 2 skipped" has to be the first thing seen, not
// something found afterwards.
func (m Model) fleetView() string {
	if m.fleet == nil {
		return ""
	}

	var b strings.Builder
	title := m.fleet.lister.Kind().Title + " across " + fmt.Sprint(len(m.cfg.Projects)) + " projects"
	b.WriteString("  " + titleStyle.Render(" "+truncate(title, max(10, m.width-6))+" ") + "\n")

	summary := m.fleetSummary()
	style := mutedStyle
	if !m.fleet.loading && !m.fleet.result.Trustworthy() {
		// Not a failure, but not the whole estate either. Amber is what stops
		// the number being quoted as though it were.
		style = warnStyle
	}
	b.WriteString("  " + style.Render(truncate(summary, max(10, m.width-4))) + "\n\n")

	if m.fleet.loading {
		return b.String()
	}

	rows := m.visibleFleetRows()
	if len(rows) == 0 {
		b.WriteString(mutedStyle.Render("  no matching resources in any project that could be read") + "\n")
		b.WriteString(m.fleetProblemLines())
		return b.String()
	}

	// Three lines of chrome above the table, plus whatever the problem list
	// needs below it, all of which comes out of the rows budget so the footer
	// stays on screen.
	problems := m.fleetProblemLines()
	reserved := 3 + strings.Count(problems, "\n")
	b.WriteString(m.renderTable(fleetKind, rows, max(1, m.bodyHeight()-reserved)))
	b.WriteString(problems)
	return b.String()
}

// fleetProblemLines names the projects that did not contribute, and why.
//
// One line each rather than a count, because "2 skipped" does not tell anyone
// which two — and a fleet table is most often read to find the project that is
// different, which a silently absent project can never be.
func (m Model) fleetProblemLines() string {
	if m.fleet == nil || m.fleet.loading {
		return ""
	}

	var b strings.Builder
	for _, s := range m.fleet.result.Snapshots {
		switch {
		case s.Skipped:
			fmt.Fprintf(&b, "  %s %s — %s\n", warnStyle.Render("·"),
				s.Project.Name, truncate(s.SkipReason, max(10, m.width-24)))
		case s.Err != nil:
			fmt.Fprintf(&b, "  %s %s — %s\n", badStyle.Render("·"),
				s.Project.Name, truncate(s.Err.Error(), max(10, m.width-24)))
		case !s.Result.Complete():
			fmt.Fprintf(&b, "  %s %s — %s\n", warnStyle.Render("·"),
				s.Project.Name, truncate(gcp.WarningStrings(s.Result.Warnings)[0], max(10, m.width-24)))
		}
	}
	return b.String()
}

// noProjectsView is what a config with no projects gets instead of an empty
// table.
//
// Everything here answers a question the blank version left open: which file
// did g9s read, what does it expect to find in it, and what does a working
// entry look like. Naming the path matters most — between $G9S_CONFIG, -config
// and the default, someone editing the wrong file can spend a long time being
// certain they edited the right one.
func (m Model) noProjectsView() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(warnStyle.Render("  No projects configured.") + "\n\n")

	if path := m.cfg.Path(); path != "" {
		b.WriteString(mutedStyle.Render(truncate("  g9s read "+path, m.width)) + "\n")
		b.WriteString(mutedStyle.Render("  and found no `projects:` entries in it.") + "\n\n")
	}

	b.WriteString("  Add at least one project, then start g9s again:\n\n")
	for _, line := range []string{
		"projects:",
		"  - name: sandbox",
		"    project_id: your-real-gcp-project-id",
	} {
		b.WriteString(mutedStyle.Render("    "+line) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("  `g9s doctor` checks the file without starting the UI.") + "\n")

	return b.String()
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
func (m Model) warningsFor(kind gcp.Kind) []gcp.Warning {
	if kind.ID != allKind.ID {
		return m.cache[kind.ID].Warnings
	}
	var out []gcp.Warning
	for _, l := range m.listers {
		out = append(out, m.cache[l.Kind().ID].Warnings...)
	}
	return out
}

// --- projects ---

func (m Model) projectsView() string {
	// A picker with nothing in it used to render a column header over blank
	// space: no message, no file name, no hint. Someone who mistyped a key in
	// their config could not tell whether g9s had read the right file, read
	// the wrong one, or read nothing at all.
	if len(m.cfg.Projects) == 0 {
		return m.noProjectsView()
	}

	var b strings.Builder

	// An unedited config is the other shape of "not set up yet", and the more
	// misleading one: it looks like a working setup right up until the first
	// login fails against a project that does not exist, with an error from
	// Google about permissions that sends people looking in the wrong place.
	banner := 0
	if m.cfg.AllPlaceholders() {
		b.WriteString(warnStyle.Render(
			truncate("  This is still the starter config — the projects below are examples, not yours.", m.width)) + "\n")
		banner++
		if path := m.cfg.Path(); path != "" {
			b.WriteString(mutedStyle.Render(
				truncate("  Edit "+path+" and set project_id for each.", m.width)) + "\n")
			banner++
		}
		b.WriteString("\n")
		banner++
	}

	nameWidth := 0
	for _, p := range m.cfg.Projects {
		nameWidth = max(nameWidth, len(p.Name))
	}
	nameWidth = min(nameWidth, 28)

	idWidth := max(12, min(30, m.width-nameWidth-34))

	b.WriteString(headerRowStyle.Render(
		"  "+pad("PROJECT", nameWidth)+"  "+pad("PROJECT ID", idWidth)+"  "+"CREDENTIALS") + "\n")

	// The banner comes out of the rows budget rather than being added to it,
	// or the body grows past the terminal and pushes the footer off the
	// bottom — which is how a warning about the config ends up hiding the keys
	// that fix it.
	rows := max(1, m.bodyHeight()-banner)
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
	case auth.StateWrongAccount:
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

	return m.renderTable(kind, visible, m.bodyHeight()-1)
}

// renderTable draws a header, a scrolled window of rows, and enough blank
// lines to hold the footer at the bottom.
//
// Shared by the per-kind table and the fleet sweep. They differ in what fills
// the rows and in nothing about how a table looks, and two copies of this
// would drift the moment either grew a column.
func (m Model) renderTable(kind gcp.Kind, visible []gcp.Resource, rows int) string {
	widths := columnWidths(kind.Columns, m.width-2)

	var b strings.Builder
	b.WriteString(headerRowStyle.Render("  " + renderRow(columnTitles(kind.Columns), widths)))
	b.WriteString("\n")

	// Scroll so the cursor stays on screen.
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
			{"enter", "go in — category (dashboard), the row's own listing where it has one, else describe"},
			{"1-9 a-z A-Z", "jump straight to a resource kind — the key is printed beside it"},
			{"0 / a", "all resources, every kind in one table"},
			{"tab / shift+tab", "cycle resource kinds — or, in a drill-down with two listings, those"},
			{"] / [", "the same, for when tab is taken by your terminal or multiplexer"},
			{"q / esc", "back up one level"},
			{"p", "back to the project list"},
			{"/", "filter rows (cleared when you switch kinds)"},
			{":", "command — :<kind>, :export csv|json, or :cd PATH / :find GLOB in Storage Objects"},
			{":fleet <kind>", "one kind across every configured project — :fleet vm. enter opens the row's own project"},
			{"space", "load the next Storage Objects page when more rows are available"},
		}},
		{"Actions", []helpEntry{
			{"d", "describe as YAML (enter does too, on a row with nothing under it)"},
			{"r", "refresh current kind (all of them on the dashboard)"},
			{"o", "open (Airflow UI for Composer, Cloud Console otherwise)"},
			{"y", "copy name to clipboard (OSC 52)"},
			{"s", "SSH to the selected running VM"},
			{":start", "boot the selected stopped instance"},
			{":stop", "shut the selected instance down — confirm by typing its name"},
			{":reset", "hard power-cycle it, like pulling the plug — same confirmation"},
			{"", "actions are commands, never single keys, and never bulk"},
		}},
		{"Credentials", []helpEntry{
			{"l", "gcloud login for the selected project (assisted browser flow)"},
			{"L", "login without a local browser (--no-browser)"},
			{"r", "re-check credentials (project list)"},
			{"", "browser stuck at \"localhost refused to connect\" after signing in?"},
			{"", "normal behind a corporate proxy — the login screen takes that tab's"},
			{"", "address pasted back and finishes the login. `g9s doctor` checks setup."},
		}},
		{"General", []helpEntry{
			{"?", "toggle this help"},
			{"↑/↓", "scroll this help"},
			{"ctrl+c / :q", "quit (or q from the project list)"},
		}},
	}

	// Widest key column across every section, so descriptions line up as one
	// column rather than per section.
	//
	// This works only because the kind legend is no longer one of these
	// entries. It is ninety characters wide at forty-nine kinds, and sitting
	// in the key column it set the indent for every row in the panel — pushing
	// every description off the right edge of a 100-column terminal, where the
	// border quietly clipped them. It now goes below, wrapped, where being
	// long costs nothing.
	keyWidth := 0
	for _, s := range sections {
		for _, e := range s.entries {
			keyWidth = max(keyWidth, lipgloss.Width(e.keys))
		}
	}

	// The panel is bordered and padded, so the text has less room than the
	// terminal is wide. Wrapping to it is what keeps the last column readable
	// rather than clipped by the border.
	descWidth := max(20, m.width-keyWidth-10)

	var b strings.Builder
	for _, s := range sections {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render(s.title) + "\n")
		for _, e := range s.entries {
			lines := strings.Split(wrap(e.desc, descWidth), "\n")
			for i, line := range lines {
				key := ""
				if i == 0 {
					key = e.keys
				}
				b.WriteString("  " + helpKeyStyle.Render(pad(key, keyWidth)) + "  " + helpDescStyle.Render(line) + "\n")
			}
		}
		b.WriteString("\n")
	}

	// The legend last, on its own, where being long is free.
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("Kind keys") + "\n")
	for _, line := range strings.Split(wrap(m.hotkeyLegend(), max(20, m.width-8)), "\n") {
		b.WriteString("  " + helpDescStyle.Render(line) + "\n")
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
		text := fmt.Sprintf("⚠ %s: %s", warningCount(len(warnings)),
			strings.Join(gcp.WarningStrings(warnings), "; "))
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
	total := fmt.Sprintf("%d", n)
	if m.screen == screenResources && m.cache[m.currentKind().ID].NextPageToken != "" {
		total += "+"
	}
	pos := mutedStyle.Render(fmt.Sprintf("%d/%s", min(cursor+1, n), total))
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(pos) - 1
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + pos
}

func (m Model) currentWarnings() []gcp.Warning {
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
		// Offering "enter select · l login" with nothing to select or log into
		// is an affordance that does nothing, and a key that does nothing
		// reads as a broken tool rather than an empty config. With no projects
		// every key but these is already a no-op, so the hint lists exactly
		// what still works.
		if len(m.cfg.Projects) == 0 {
			return "? help · q quit — then edit the config and start g9s again"
		}
		return "enter select · l login · r re-check · : cmd · ? help · q quit"
	case screenOverview:
		return "kind keys: " + m.hotkeyLegend() + " · enter open · 0/a all resources · r refresh all · : cmd · ? help"
	case screenResources:
		if state, objects := gcp.StorageObjectState(m.drillLister()); objects {
			more := ""
			if m.loading[m.currentKind().ID] && m.hasData(m.currentKind().ID) {
				more = "loading more… · "
			} else if m.cache[m.currentKind().ID].NextPageToken != "" {
				more = "space next page · "
			}
			open := "d describe"
			if r, selected := m.selectedResource(); selected {
				if _, folder := r.Raw.(*gcp.StorageObjectPrefix); folder {
					open = "enter folder · d describe"
				}
			}
			back := "esc back"
			if state.Prefix != "" || state.MatchGlob != "" {
				back = "esc up"
			}
			return more + open + " · :cd path · :find glob · / filter · " + back + " · ? help"
		}
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
	case screenLogin:
		return "enter deliver pasted address · ctrl+o reopen browser · ctrl+y copy link · esc cancel"
	case screenFleet:
		return "enter open in its project · d describe · y yank · / filter · r re-sweep · esc back"
	case screenConfirm:
		if m.pending != nil && m.pending.typed {
			return "type the instance name, then enter · esc cancel"
		}
		return "enter confirm · esc cancel"
	default:
		return "esc back"
	}
}

// loginView is the assisted login screen.
//
// It reads as numbered steps because that is what it is. The screen has one
// job beyond showing the link: telling the user, at the exact moment the
// browser shows "localhost refused to connect", that nothing has gone wrong
// yet and the address bar of that broken tab is the login. That moment is
// where the corporate-proxy flow died before this screen existed.
func (m Model) loginView() string {
	width := max(20, m.width-6)
	var b strings.Builder

	fmt.Fprintf(&b, "\n  %s\n\n", titleStyle.Render("Logging in to "+m.loginProject))

	if m.assisted == nil {
		b.WriteString(mutedStyle.Render("  starting gcloud…") + "\n")
		return b.String()
	}

	b.WriteString("  1. Sign in in the browser tab that just opened.\n")
	b.WriteString(mutedStyle.Render("     No tab? ctrl+o reopens it; ctrl+y copies the link:") + "\n\n")
	for _, line := range wrapHard(m.assisted.URL(), width-5) {
		b.WriteString("     " + mutedStyle.Render(line) + "\n")
	}
	b.WriteString("\n")
	b.WriteString("  2. If the sign-in finishes and this screen closes on its own, you are done.\n\n")
	b.WriteString("  3. If the browser instead lands on an error like " + warnStyle.Render("\"localhost refused to") + "\n")
	b.WriteString("     " + warnStyle.Render("connect\"") + " — that is normal behind a corporate proxy, and the login is\n")
	b.WriteString("     still alive. Copy the ENTIRE address from that tab's address bar and\n")
	b.WriteString("     paste it here, then press enter:\n\n")

	m.loginInput.Width = width - 8
	b.WriteString("     " + m.loginInput.View() + "\n\n")
	// One Render call per line: a newline inside a lipgloss render turns the
	// string into a block and pads every line to the widest, which shows up as
	// text drifting rightwards.
	for _, line := range []string{
		"     The address looks like http://localhost:8085/?state=…&code=… — g9s hands",
		"     it to gcloud on this machine, bypassing the proxy. The code in it is",
		"     single-use and useless outside this login.",
	} {
		b.WriteString(mutedStyle.Render(line) + "\n")
	}
	return b.String()
}

// wrapHard breaks text at width unconditionally — for URLs, which have no
// spaces for wrap to use.
func wrapHard(s string, width int) []string {
	if width < 1 {
		return []string{s}
	}
	var lines []string
	runes := []rune(s)
	for len(runes) > width {
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	return append(lines, string(runes))
}

func (m Model) drillLister() gcp.Lister {
	if m.drill == nil {
		return nil
	}
	return m.drill.lister
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
