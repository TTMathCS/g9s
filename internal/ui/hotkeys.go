package ui

// Hotkeys: one keypress per resource kind, in display order.
//
// Nine digits stopped covering the kinds at the tenth one. A kind reachable
// only by tab-cycling or a typed command is a kind nobody uses, so the sequence
// carries on into letters — but not alphabetically, because most of the
// alphabet is already an action on the table screen. `d` describes, `o` opens,
// `y` yanks, `s` ssh-es, `r` refreshes, `l`/`L` log in, `p` goes back to
// projects, `g`/`G` and `j`/`k` move, `q` backs out, `a` is the merged view.
// Handing any of those to a kind would break the action that owns it, so the
// letter run is exactly what nothing else claims, alphabetical within that.
//
// The mapping is not something to memorise: every kind's key is printed beside
// it in the dashboard and in the tab strip, so the legend is always on screen.
//
// Lowercase runs out at twenty-three, which is the whole lowercase alphabet.
// The run then continues into shift — `A` to `Z`, minus the two already bound
// (`G` jumps to the bottom, `L` logs in without a browser). Still one press,
// still printed beside the kind, and it keeps the scheme mechanical rather than
// making the twenty-fourth kind a special case reachable only by typing.
//
// This is a keyspace, not a recommendation. A dashboard with forty rows is not
// a good dashboard, and anything that belongs to a parent row should still be a
// drill-down (see gcp.ChildLister) — those cost no key and do not lengthen the
// list. What changed is that the keyspace is no longer the thing deciding which
// kinds can exist.

// kindKeys selects a resource kind by index into Listers(). Forty-seven kinds
// fit; past that a kind gets noHotkey and is reached with tab, the dashboard
// cursor or `:<kind>`. TestEveryKindStillHasAKey fails first.
var kindKeys = []string{
	"1", "2", "3", "4", "5", "6", "7", "8", "9",
	"b", "c", "e", "f", "h", "i", "m", "n", "t", "u", "v", "w", "x", "z",
	// Shift, in alphabetical order, skipping G and L.
	"A", "B", "C", "D", "E", "F", "H", "I", "J", "K", "M", "N", "O",
	"P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z",
}

// allKeys select the merged view. `0` is the k9s reflex for "everything at
// once"; `a` is for "all", and is why the letter run above starts at `b`.
var allKeys = []string{"0", "a"}

// actionKeys are the keys already bound on the screens where kind hotkeys are
// live. Listed here rather than left implicit in the key handlers because the
// one thing that must stay true is that no kind ever shadows an action —
// TestKindKeysAvoidActionKeys is what keeps it true as either list grows.
var actionKeys = []string{
	"a", "d", "g", "G", "j", "k", "l", "L", "o", "p", "q", "r", "s", "y",
	"0", "/", ":", "?", "[", "]", "enter", "esc", "tab", "shift+tab",
	"up", "down", "ctrl+c",
}

// noHotkey marks a kind the alphabet did not reach.
const noHotkey = "·"

// hotkeyWidth is the display width of every hotkey, so the dashboard's key
// column lines up without measuring. Single characters throughout is half the
// reason the scheme continues into letters instead of into "10", "11", "12".
const hotkeyWidth = 1

// kindKey is the hotkey for a lister at index, or noHotkey past the alphabet.
func kindKey(index int) string {
	if index < 0 || index >= len(kindKeys) {
		return noHotkey
	}
	return kindKeys[index]
}

// tabKey is the hotkey shown for a tab, the merged view included.
func (m Model) tabKey(index int) string {
	if index == m.allTabIdx() {
		return "a"
	}
	return kindKey(index)
}

// tabForKey resolves a keypress to a tab index. Keys past the registered
// listers do not match, so pressing `w` with thirteen kinds loaded does
// nothing rather than jumping somewhere arbitrary.
func (m Model) tabForKey(key string) (int, bool) {
	for _, k := range allKeys {
		if key == k {
			return m.allTabIdx(), true
		}
	}
	for i, k := range kindKeys {
		if key != k {
			continue
		}
		if i >= len(m.listers) {
			return -1, false
		}
		return i, true
	}
	return -1, false
}

// hotkeyLegend describes the assigned keys for the help screen, e.g.
// "1-9 then b e f h i m n t u v w". Generated rather than written out so it
// cannot drift from what the keys actually do.
func (m Model) hotkeyLegend() string {
	n := min(len(m.listers), len(kindKeys))
	if n == 0 {
		return ""
	}
	if n <= 9 {
		return "1-" + kindKeys[n-1]
	}

	legend := "1-9 then"
	for _, k := range kindKeys[9:n] {
		legend += " " + k
	}
	return legend
}
