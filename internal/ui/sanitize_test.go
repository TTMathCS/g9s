package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/TTMathCS/g9s/internal/gcp"
)

func TestSanitizeLineStripsControlCharacters(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"clean text is untouched", "web-01", "web-01"},
		{"csi sequence", "web\x1b[31m-01", "web[31m-01"},
		{"osc window title", "web\x1b]0;owned\x07-01", "web]0;owned-01"},
		{"bare c1 csi introducer", "web2J-01", "web2J-01"},
		{"del", "web\x7f-01", "web-01"},
		{"newlines and tabs become spaces", "a\nb\tc", "a b c"},
		{"unicode survives", "réseau-01 ✓", "réseau-01 ✓"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeLine(tt.in); got != tt.want {
				t.Errorf("sanitizeLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeLineNeutralisesRawBytes(t *testing.T) {
	// A lone 0x9b is not valid UTF-8, so it never decodes to U+009B and a
	// rune-wise scan walks straight past it — while a terminal in 8-bit mode
	// reads it as a CSI introducer. It has to come out inert either way.
	got := sanitizeLine("web\x9b2J-01")

	if strings.Contains(got, "\x9b") {
		t.Errorf("sanitizeLine kept the raw 0x9b byte: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("sanitizeLine produced invalid UTF-8: %q", got)
	}
}

func TestSanitizeBlockKeepsLineStructure(t *testing.T) {
	got := sanitizeBlock("name: x\n  labels:\n\tenv: prod\x1b[2J\n")
	want := "name: x\n  labels:\n\tenv: prod[2J\n"
	if got != want {
		t.Errorf("sanitizeBlock = %q, want %q", got, want)
	}
}

func TestTableRowsCannotCarryEscapeSequences(t *testing.T) {
	// The real exposure: row cells are API-supplied strings written straight to
	// the terminal, and a terminal acts on escape sequences it is handed. A
	// resource name carrying one could clear the screen, retitle the window, or
	// on terminals with the feature enabled, push text back onto stdin.
	m := testModel(t, []gcp.Resource{{
		Name:     "web\x1b[2J-01",
		Location: "us-central1-a",
		Status:   "RUNNING",
		Row:      []string{"web\x1b[2J-01", "us\x1b]0;owned\x07-central1-a", "n2", "10.0.0.1", "-", "RUNNING", "1d"},
	}})
	m.width, m.height = 132, 24
	m.screen = screenResources

	view := m.View()
	if strings.Contains(view, "\x1b[2J") {
		t.Error("an erase-display sequence from a resource name reached the rendered table")
	}
	if strings.Contains(view, "\x1b]0;") {
		t.Error("an OSC window-title sequence from a location reached the rendered table")
	}
	// The tool's own styling is applied after truncation, so it still colours.
	if !strings.Contains(view, "web[2J-01") {
		t.Errorf("the printable part of the name should still render:\n%s", view)
	}
}

func TestFlashTextCannotCarryEscapeSequences(t *testing.T) {
	m := testModel(t, nil)
	m.width, m.height = 132, 24
	m.flashText = "vm: rpc error \x1b[2J\x1b]0;owned\x07"

	if got := m.footerView(); strings.ContainsAny(got, "\x07") || strings.Contains(got, "\x1b]0;") {
		t.Errorf("escape sequences from an API error reached the footer: %q", got)
	}
}

func TestWrapMeasuresDisplayCells(t *testing.T) {
	// Bytes would wrap a line of accented text at roughly half the width.
	wrapped := wrap(strings.Repeat("réseau ", 6), 20)
	for _, line := range strings.Split(wrapped, "\n") {
		if w := lipgloss.Width(line); w > 20 {
			t.Errorf("wrapped line is %d cells wide: %q", w, line)
		}
	}
	if !strings.Contains(wrapped, "\n") {
		t.Error("nothing wrapped at all")
	}
}

func TestSafeToOpenRejectsEscapeSequences(t *testing.T) {
	// The URL is handed to a platform opener and echoed into the status line.
	for _, raw := range []string{
		"https://example.com/2J",
		"https://example.com/\x7f",
	} {
		if safeToOpen(raw) {
			t.Errorf("safeToOpen(%q) = true, want it refused", raw)
		}
	}
	if !safeToOpen("https://console.cloud.google.com/compute?project=p-1") {
		t.Error("a real console link was refused")
	}
}

func TestWarningCountReadsForEveryKindOfWarning(t *testing.T) {
	// The badge used to say "scope(s) unavailable". An unreachable region is
	// still the common warning, but a listing bounded by a time window or a row
	// cap is also a result that is not the whole truth, and that wording made
	// the BigQuery jobs cap read as a permissions problem.
	if got := warningCount(1); got != "1 warning" {
		t.Errorf("warningCount(1) = %q", got)
	}
	if got := warningCount(3); got != "3 warnings" {
		t.Errorf("warningCount(3) = %q", got)
	}
}

func TestFooterShowsTheWarningTextItself(t *testing.T) {
	m := testModel(t, []gcp.Resource{{Name: "a", Row: []string{"a"}}})
	m.width, m.height = 160, 24
	m.screen = screenResources
	m.cache[m.currentKind().ID] = gcp.Result{
		Resources: []gcp.Resource{{Name: "a", Row: []string{"a"}}},
		Warnings:  []string{"only the 500 most recent jobs are shown"},
	}

	got := m.footerView()
	if !strings.Contains(got, "1 warning") {
		t.Errorf("footer does not count the warning: %q", got)
	}
	// The count alone says nothing; the text is what distinguishes a capped
	// listing from an unreachable region.
	if !strings.Contains(got, "most recent jobs") {
		t.Errorf("footer does not show the warning text: %q", got)
	}
}

func TestStatusStyleCoversTheNewKinds(t *testing.T) {
	// Every status the Pub/Sub and Cloud Run listers can emit has to colour, or
	// the row it matters most on is the one that renders as plain text.
	good := []string{"READY", "SUCCEEDED", "ACTIVE"}
	warn := []string{"PENDING", "RECONCILING", "DETACHED"}
	bad := []string{"FAILED", "RESOURCE_ERROR", "INGESTION_RESOURCE_ERROR"}

	for _, s := range good {
		if statusStyle(s).GetForeground() != goodStyle.GetForeground() {
			t.Errorf("%s does not render as healthy", s)
		}
	}
	for _, s := range warn {
		if statusStyle(s).GetForeground() != warnStyle.GetForeground() {
			t.Errorf("%s does not render as in-flight", s)
		}
	}
	for _, s := range bad {
		if statusStyle(s).GetForeground() != badStyle.GetForeground() {
			t.Errorf("%s does not render as broken", s)
		}
	}
}
