package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
)

func hotkeyModel(t *testing.T) Model {
	t.Helper()

	m := New(&config.Config{Projects: []config.Project{{Name: "prod", ProjectID: "prod-1"}}}, nil)
	m.width, m.height = 132, 40
	m.active, m.hasActive = m.cfg.Projects[0], true
	m.authStatus["prod"] = auth.Status{State: auth.StateValid}
	return m
}

func TestKindKeysAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range append(append([]string{}, kindKeys...), allKeys...) {
		if seen[k] {
			t.Errorf("key %q is assigned twice", k)
		}
		seen[k] = true
	}
}

func TestKindKeysAvoidActionKeys(t *testing.T) {
	// The invariant that makes the fall-through dispatch safe: a kind hotkey
	// must never be a key that already does something. If this fails, pressing
	// a kind's key on the table screen fires an action instead — or worse, the
	// action wins and the kind is unreachable.
	action := map[string]bool{}
	for _, k := range actionKeys {
		action[k] = true
	}
	for _, k := range kindKeys {
		if action[k] {
			t.Errorf("kind hotkey %q collides with an action key", k)
		}
	}
}

func TestKindKeysCoverEveryLister(t *testing.T) {
	// Every registered kind must have a one-press key. This is the test that
	// fails when a fourteenth, then a twenty-third kind is added, which is the
	// reminder to widen the alphabet rather than let a kind quietly become
	// reachable only by typing a command.
	m := hotkeyModel(t)
	for i := range m.listers {
		if key := kindKey(i); key == noHotkey {
			t.Errorf("lister %d (%s) has no hotkey — kindKeys is out of room",
				i, m.listers[i].Kind().ID)
		}
	}
}

func TestEveryKindHasAOnePressKeyFromBothScreens(t *testing.T) {
	// The end-to-end version, and the whole point of the scheme: one keypress
	// from the dashboard and one from the table must land on every kind,
	// including the ones past the ninth.
	base := hotkeyModel(t)
	tabs := base.tabs()

	for idx, kind := range tabs {
		key := base.tabKey(idx)
		if key == noHotkey {
			t.Errorf("%s has no hotkey", kind.ID)
			continue
		}

		for _, from := range []struct {
			name   string
			screen screen
			handle func(Model, tea.KeyMsg) (tea.Model, tea.Cmd)
		}{
			{"dashboard", screenOverview, Model.handleOverviewKey},
			{"table", screenResources, Model.handleResourcesKey},
		} {
			m := base
			m.screen = from.screen
			// Start somewhere else, so landing on the right tab means the key
			// worked rather than that nothing happened.
			m.kindIdx = (idx + 1) % len(tabs)

			next, _ := from.handle(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			got := next.(Model)

			if got.kindIdx != idx {
				t.Errorf("from %s: %q selected tab %d (%s), want %d (%s)",
					from.name, key, got.kindIdx, tabs[got.kindIdx].ID, idx, kind.ID)
			}
			if got.screen != screenResources {
				t.Errorf("from %s: %q left the screen at %v, want the table",
					from.name, key, got.screen)
			}
		}
	}
}

func TestHotkeysPastTheListerCountDoNothing(t *testing.T) {
	// A key the alphabet defines but no kind claims must be inert, not a jump
	// to some arbitrary tab.
	m := hotkeyModel(t)
	if len(kindKeys) <= len(m.listers) {
		t.Skip("every hotkey is claimed by a lister")
	}

	unused := kindKeys[len(m.listers)]
	if _, ok := m.tabForKey(unused); ok {
		t.Errorf("%q resolved to a tab, but only %d listers are registered", unused, len(m.listers))
	}
}

func TestMergedViewAnswersToZeroAndA(t *testing.T) {
	m := hotkeyModel(t)
	for _, key := range allKeys {
		idx, ok := m.tabForKey(key)
		if !ok || idx != m.allTabIdx() {
			t.Errorf("%q selected tab %d (ok=%v), want the merged tab %d", key, idx, ok, m.allTabIdx())
		}
	}
}

func TestHotkeyIsShownBesideEveryKind(t *testing.T) {
	// The keys are only usable because they are on screen. Both the dashboard
	// and the tab strip have to print the one that jumps to each kind.
	m := hotkeyModel(t)
	m.screen = screenOverview

	dashboard := m.overviewView()
	for i, kind := range m.tabs() {
		want := m.tabKey(i) + " " + kind.Title
		if !strings.Contains(dashboard, want) {
			t.Errorf("dashboard is missing %q", want)
		}
	}

	m.screen = screenResources
	for i, kind := range m.tabs() {
		m.kindIdx = i
		if strip := m.tabsView(); !strings.Contains(strip, m.tabKey(i)+" "+kind.Title) {
			t.Errorf("tab strip for %s does not show its hotkey", kind.ID)
		}
	}
}

func TestHotkeysAreOneCellWide(t *testing.T) {
	// The dashboard's key column is a fixed cell wide, which is what keeps the
	// category names aligned as kinds are added. Two-character keys would shift
	// every row past the ninth.
	for _, k := range append(append([]string{}, kindKeys...), allKeys...) {
		if len([]rune(k)) != hotkeyWidth {
			t.Errorf("hotkey %q is %d cells, want %d", k, len([]rune(k)), hotkeyWidth)
		}
	}
}

func TestHotkeyLegendMatchesTheKeys(t *testing.T) {
	m := hotkeyModel(t)
	legend := m.hotkeyLegend()

	for i := range m.listers {
		key := kindKey(i)
		if i < 9 {
			continue // covered by the "1-9" range rather than listed
		}
		if !strings.Contains(legend, key) {
			t.Errorf("legend %q omits the key for %s", legend, m.listers[i].Kind().ID)
		}
	}
	if !strings.HasPrefix(legend, "1-") {
		t.Errorf("legend %q should start with the digit range", legend)
	}
}
