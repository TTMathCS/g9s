package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Adaptive colours so the tool is legible on light and dark terminals.
var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#1a56db", Dark: "#7aa2f7"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#8b93a7"}
	colorGood   = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#9ece6a"}
	colorWarn   = lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#e0af68"}
	colorBad    = lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f7768e"}
	colorText   = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#c0caf5"}
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#ffffff")).
			Background(colorAccent).
			Padding(0, 1)

	crumbStyle = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)

	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent).
			Underline(true).
			Padding(0, 1)

	tabStyle = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)

	headerRowStyle = lipgloss.NewStyle().Bold(true).Foreground(colorMuted)

	selectedRowStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#ffffff")).
				Background(colorAccent)

	rowStyle = lipgloss.NewStyle().Foreground(colorText)

	mutedStyle = lipgloss.NewStyle().Foreground(colorMuted)
	goodStyle  = lipgloss.NewStyle().Foreground(colorGood)
	warnStyle  = lipgloss.NewStyle().Foreground(colorWarn)
	badStyle   = lipgloss.NewStyle().Foreground(colorBad)

	helpKeyStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	helpDescStyle = lipgloss.NewStyle().Foreground(colorMuted)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted).
			Padding(0, 1)
)

// statusStyle colours a resource status the way an operator reads it: green is
// running, amber is in flight, red needs attention.
func statusStyle(status string) lipgloss.Style {
	switch strings.ToUpper(status) {
	case "RUNNING", "ACTIVE", "READY":
		return goodStyle
	case "PROVISIONING", "STAGING", "CREATING", "UPDATING", "STOPPING", "DELETING", "REPAIRING", "SUSPENDING", "RECONCILING":
		return warnStyle
	case "ERROR", "FAILED", "TERMINATED", "STOPPED", "SUSPENDED", "UNHEALTHY", "DEGRADED":
		return badStyle
	default:
		return rowStyle
	}
}

// truncate shortens s to width display cells, marking the cut with an
// ellipsis. Cells, not runes: a CJK character occupies two columns, and
// counting runes would let one wide name push every column after it out of
// alignment.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}

	var b strings.Builder
	used := 0
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if used+w > width-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + "…"
}

// pad right-pads s to width, truncating when it does not fit.
func pad(s string, width int) string {
	s = truncate(s, width)
	if n := width - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
