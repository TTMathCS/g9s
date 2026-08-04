package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTMathCS/g9s/internal/bookmarks"
	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

func bookmarkModel(t *testing.T) Model {
	t.Helper()
	m := New(fleetCfg(), nil)
	m.width, m.height = 100, 24
	m.marks = bookmarks.Load(filepath.Join(t.TempDir(), "bookmarks.yaml"))
	return m
}

// A bookmark is a place: the project, the kind and the query. Saving only the
// first two would drop the part that gets retyped.
func TestBookmarkingATableKeepsTheProjectKindAndFilter(t *testing.T) {
	m := bookmarkModel(t)
	m.active, m.hasActive = m.cfg.Projects[0], true
	m.kindIdx = 0
	m.screen = screenResources
	m.filter.SetValue("api")

	next, _ := m.runCommand("bm prod-api")
	m = next.(Model)

	list := m.marks.All()
	if len(list) != 1 {
		t.Fatalf("got %d bookmarks, want 1", len(list))
	}
	b := list[0]
	if b.Name != "prod-api" || b.Project != m.cfg.Projects[0].Name {
		t.Errorf("bookmark = %+v, want the current project under the typed name", b)
	}
	if b.Kind != m.listers[0].Kind().ID {
		t.Errorf("kind = %q, want the open table's kind", b.Kind)
	}
	if b.Filter != "api" {
		t.Errorf("filter = %q, want the query that was typed", b.Filter)
	}
}

// A sweep is about every project by definition, and a bookmark that pinned it
// to one would open something different from what was saved.
func TestBookmarkingASweepRecordsTheShapeNotAProject(t *testing.T) {
	m := bookmarkModel(t)
	m.fleet = &fleetState{lister: gcp.Listers()[0], compare: true}
	m.screen = screenFleet

	next, _ := m.runCommand("bm everywhere")
	m = next.(Model)

	list := m.marks.All()
	if len(list) != 1 {
		t.Fatalf("got %d bookmarks, want 1", len(list))
	}
	if list[0].Sweep != bookmarks.SweepDiff {
		t.Errorf("sweep = %q, want the comparison shape", list[0].Sweep)
	}
	if list[0].Project != "" {
		t.Errorf("project = %q, want none — a sweep is every project", list[0].Project)
	}
}

// Nothing to save is said out loud rather than writing a bookmark that opens
// somewhere arbitrary.
func TestBookmarkingFromTheProjectListRefuses(t *testing.T) {
	m := bookmarkModel(t)
	m.screen = screenProjects

	next, cmd := m.runCommand("bm nowhere")
	m = next.(Model)

	if len(m.marks.All()) != 0 {
		t.Error("a bookmark was saved from the project list")
	}
	if msg, ok := cmd().(flashMsg); !ok || msg.level == flashInfo {
		t.Errorf("refusal was not reported: %#v", cmd())
	}
}

func TestBookmarkNeedsAName(t *testing.T) {
	m := bookmarkModel(t)
	m.active, m.hasActive = m.cfg.Projects[0], true
	m.screen = screenResources

	// `:bm` with no argument opens the list rather than saving something
	// nameless, which is the only other thing it could mean.
	next, _ := m.runCommand("bm")
	if next.(Model).screen != screenBookmarks {
		t.Errorf("screen = %v, want the bookmark list", next.(Model).screen)
	}
}

// Opening one is the whole point, and it has to restore the query too.
func TestOpeningABookmarkRestoresTheProjectKindAndFilter(t *testing.T) {
	m := bookmarkModel(t)
	kind := m.listers[2].Kind().ID
	if err := m.marks.Add(bookmarks.Bookmark{
		Name: "there", Project: "dev", Kind: kind, Filter: "worker",
	}); err != nil {
		t.Fatal(err)
	}

	next, _ := m.runCommand("bm")
	m = next.(Model)
	next, _ = m.handleBookmarkKey(key("enter"))
	m = next.(Model)

	if !m.hasActive || m.active.Name != "dev" {
		t.Errorf("active project = %q, want dev", m.active.Name)
	}
	if m.currentKind().ID != kind {
		t.Errorf("kind = %q, want %q", m.currentKind().ID, kind)
	}
	if m.filter.Value() != "worker" {
		t.Errorf("filter = %q, want the saved query", m.filter.Value())
	}
	if m.screen != screenResources {
		t.Errorf("screen = %v, want the resource table", m.screen)
	}
}

// A config that lost the project, or a g9s that dropped the kind, must say
// which — landing somewhere arbitrary is worse than not opening at all.
func TestABookmarkPointingAtSomethingGoneSaysWhichRatherThanGuessing(t *testing.T) {
	m := bookmarkModel(t)
	for _, b := range []bookmarks.Bookmark{
		{Name: "gone-project", Project: "retired", Kind: m.listers[0].Kind().ID},
		{Name: "gone-kind", Project: "dev", Kind: "quantum-computers"},
	} {
		next, cmd := m.openBookmark(b)
		after := next.(Model)

		if after.screen == screenResources {
			t.Errorf("bookmark %q opened a table anyway", b.Name)
		}
		msg, ok := cmd().(flashMsg)
		if !ok {
			t.Fatalf("bookmark %q reported nothing", b.Name)
		}
		if !strings.Contains(msg.text, b.Name) {
			t.Errorf("message %q does not name the bookmark", msg.text)
		}
	}
}

// The list is the only place a saved bookmark becomes visible, so it has to
// show where each one goes rather than just what it is called.
func TestTheListShowsWhereEachBookmarkGoes(t *testing.T) {
	m := bookmarkModel(t)
	if err := m.marks.Add(bookmarks.Bookmark{
		Name: "prod-api", Project: "prod", Kind: "vm", Filter: "api",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.marks.Add(bookmarks.Bookmark{
		Name: "all-errors", Kind: "errors", Sweep: bookmarks.SweepFleet,
	}); err != nil {
		t.Fatal(err)
	}
	next, _ := m.runCommand("bm")
	view := next.(Model).View()

	for _, want := range []string{"prod-api", "prod", "vm", "/api", "all-errors", ":fleet errors"} {
		if !strings.Contains(view, want) {
			t.Errorf("the bookmark list never shows %q:\n%s", want, view)
		}
	}
}

// An empty list has to say how to fill it. A blank panel reads as broken.
func TestAnEmptyBookmarkListSaysHowToMakeOne(t *testing.T) {
	m := bookmarkModel(t)
	next, _ := m.runCommand("bm")
	view := next.(Model).View()

	if !strings.Contains(view, ":bm") {
		t.Errorf("the empty bookmark list does not say how to save one:\n%s", view)
	}
}

// A file that could not be read must not look like one that is empty.
func TestAnUnreadableBookmarkFileIsSaidOutLoud(t *testing.T) {
	m := bookmarkModel(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bookmarks.yaml")
	if err := os.WriteFile(path, []byte("bookmarks: [broken: yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.marks = bookmarks.Load(path)

	next, _ := m.runCommand("bm")
	view := next.(Model).View()
	if !strings.Contains(view, "could not read bookmarks") {
		t.Errorf("a broken bookmark file reads as an empty one:\n%s", view)
	}
}

func TestRemovingABookmarkFromTheList(t *testing.T) {
	m := bookmarkModel(t)
	for _, name := range []string{"a", "b"} {
		if err := m.marks.Add(bookmarks.Bookmark{Name: name, Project: "dev", Kind: "vm"}); err != nil {
			t.Fatal(err)
		}
	}
	next, _ := m.runCommand("bm")
	m = next.(Model)
	next, _ = m.handleBookmarkKey(key("d"))
	m = next.(Model)

	list := m.marks.All()
	if len(list) != 1 || list[0].Name != "b" {
		t.Errorf("bookmarks after removing the first = %+v", list)
	}
}

// Every model has a store, including one built from a config that was never
// read from disk — the first `:bm` must not find a nil one.
func TestEveryModelHasABookmarkStore(t *testing.T) {
	cfg := &config.Config{Projects: []config.Project{{Name: "dev", ProjectID: "dev-1"}}}
	m := New(cfg, nil)
	if m.marks == nil {
		t.Fatal("no bookmark store for a config with no path")
	}
	if m.marks.Path() != bookmarks.PathFor(cfg.Path()) {
		t.Errorf("store path = %q, want the file beside the config", m.marks.Path())
	}
}
