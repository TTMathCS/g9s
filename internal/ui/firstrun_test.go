package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/TTMathCS/g9s/internal/config"
)

func starterConfig() *config.Config {
	return &config.Config{Projects: []config.Project{
		{Name: "sandbox", ProjectID: "my-sandbox-project"},
		{Name: "prod-data", ProjectID: "my-prod-data-project"},
	}}
}

// Overflow on the alternate screen scrolls rather than clips, so a view that
// runs past the terminal leaves it corrupted after the cause is gone. Sizes
// this small are not hypothetical: dragging a tmux divider passes through all
// of them, one frame at a time.
func TestViewNeverOverflowsTheTerminal(t *testing.T) {
	sizes := []struct{ w, h int }{
		{200, 50}, {132, 40}, {100, 24}, {80, 24}, {60, 20},
		{50, 15}, {40, 10}, {39, 10}, {30, 8}, {20, 4}, {10, 3}, {1, 1},
	}

	for _, s := range sizes {
		m := New(starterConfig(), nil)
		m.width, m.height = s.w, s.h

		lines := strings.Split(m.View(), "\n")
		if len(lines) > s.h {
			t.Errorf("%dx%d: rendered %d lines into %d rows", s.w, s.h, len(lines), s.h)
		}
		for i, line := range lines {
			if w := lipgloss.Width(line); w > s.w {
				t.Errorf("%dx%d: line %d is %d cols wide: %q", s.w, s.h, i, w, line)
			}
		}
	}
}

func TestTinyTerminalSaysWhySoTheSizeIsTheObviousCause(t *testing.T) {
	m := New(starterConfig(), nil)
	m.width, m.height = 30, 8

	got := m.View()
	if !strings.Contains(got, "too small") {
		t.Errorf("a terminal below the minimum does not say so:\n%s", got)
	}
	// The required size has to be in there, or the only way to find it is to
	// resize by trial and error.
	if !strings.Contains(got, "40") || !strings.Contains(got, "10") {
		t.Errorf("the message does not name the size needed:\n%s", got)
	}
}

// A config with no projects used to render a column header over blank space.
// Someone who mistyped a key could not tell whether g9s read the right file,
// the wrong one, or nothing at all.
func TestEmptyConfigExplainsItselfAndNamesTheFile(t *testing.T) {
	cfg := &config.Config{}
	m := New(cfg, nil)
	m.width, m.height = 90, 22

	got := m.View()
	if !strings.Contains(got, "No projects configured") {
		t.Errorf("empty config renders no explanation:\n%s", got)
	}
	// Enough of a worked example to copy, since "add a project" without the
	// shape of one sends people back to the documentation.
	if !strings.Contains(got, "project_id:") {
		t.Errorf("empty state shows no example entry:\n%s", got)
	}
	// Keys that do nothing must not be advertised: with no projects, every
	// binding but help and quit is already a no-op.
	if strings.Contains(got, "enter select") || strings.Contains(got, "l login") {
		t.Errorf("empty state offers keys that do nothing:\n%s", got)
	}
}

// The unedited starter config is the more misleading state: it looks like a
// working setup right up until the first login fails against a project that
// does not exist, with an error from Google about permissions.
func TestUneditedStarterConfigIsCalledOut(t *testing.T) {
	m := New(starterConfig(), nil)
	m.width, m.height = 90, 22

	got := m.View()
	if !strings.Contains(got, "starter config") {
		t.Errorf("an unedited config is presented as a real one:\n%s", got)
	}

	// And the banner must not cost the footer: a warning about the config that
	// pushes the keys off the bottom of the screen has traded one problem for
	// a worse one.
	if !strings.Contains(got, "? help") {
		t.Errorf("the banner pushed the footer off screen:\n%s", got)
	}
}

// A real config must not be nagged at.
func TestAnEditedConfigGetsNoBanner(t *testing.T) {
	cfg := &config.Config{Projects: []config.Project{
		{Name: "prod", ProjectID: "acme-dataeng-prod-4471"},
	}}
	m := New(cfg, nil)
	m.width, m.height = 90, 22

	if got := m.View(); strings.Contains(got, "starter config") {
		t.Errorf("a real config was labelled a starter config:\n%s", got)
	}
}

// One placeholder left among real projects means the config was edited, and
// the banner would be wrong about the file as a whole.
func TestPartiallyEditedConfigIsNotCalledAStarter(t *testing.T) {
	cfg := &config.Config{Projects: []config.Project{
		{Name: "prod", ProjectID: "acme-dataeng-prod-4471"},
		{Name: "sandbox", ProjectID: "my-sandbox-project"},
	}}
	if cfg.AllPlaceholders() {
		t.Error("a config with one real project reports as all placeholders")
	}
}
