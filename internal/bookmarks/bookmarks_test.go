package bookmarks

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	return Load(filepath.Join(t.TempDir(), "bookmarks.yaml"))
}

// A new installation has no bookmark file, and that is the normal state rather
// than something to report.
func TestAMissingFileIsAnEmptyListAndNotAnError(t *testing.T) {
	s := tempStore(t)
	if err := s.Err(); err != nil {
		t.Errorf("a missing bookmark file reported %v", err)
	}
	if len(s.All()) != 0 {
		t.Errorf("got %d bookmarks from a file that does not exist", len(s.All()))
	}
}

func TestSavedBookmarksSurviveAReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bookmarks.yaml")

	s := Load(path)
	if err := s.Add(Bookmark{Name: "prod-vms", Project: "prod", Kind: "vm", Filter: "api"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(Bookmark{Name: "fleet-errors", Kind: "errors", Sweep: SweepFleet}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	reloaded := Load(path)
	if err := reloaded.Err(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	list := reloaded.All()
	if len(list) != 2 {
		t.Fatalf("got %d bookmarks after reload, want 2", len(list))
	}
	if list[0].Name != "prod-vms" || list[0].Filter != "api" || list[0].Kind != "vm" {
		t.Errorf("first bookmark came back as %+v", list[0])
	}
	if list[1].Sweep != SweepFleet {
		t.Errorf("the sweep bookmark came back as %+v", list[1])
	}
}

// Somebody saving the same name twice is refining it. Two rows with one name
// and different destinations is a menu nobody can use.
func TestSavingTheSameNameReplacesRatherThanDuplicates(t *testing.T) {
	s := tempStore(t)
	if err := s.Add(Bookmark{Name: "here", Project: "dev", Kind: "vm", Filter: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Bookmark{Name: "HERE", Project: "dev", Kind: "vm", Filter: "new"}); err != nil {
		t.Fatal(err)
	}

	list := s.All()
	if len(list) != 1 {
		t.Fatalf("got %d bookmarks, want the name replaced", len(list))
	}
	if list[0].Filter != "new" {
		t.Errorf("filter = %q, want the newer one", list[0].Filter)
	}
}

func TestRemoveDeletesByNameAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bookmarks.yaml")

	s := Load(path)
	for _, name := range []string{"a", "b", "c"} {
		if err := s.Add(Bookmark{Name: name, Project: "dev", Kind: "vm"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Remove("b"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	list := Load(path).All()
	if len(list) != 2 {
		t.Fatalf("got %d bookmarks after removing one of three", len(list))
	}
	for _, b := range list {
		if b.Name == "b" {
			t.Error("the removed bookmark came back from the file")
		}
	}
}

// The file is hand-editable, so it can contain a row that points nowhere. A
// row that cannot be opened is worse than a row that is not there.
func TestBookmarksThatPointNowhereAreDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bookmarks.yaml")
	if err := os.WriteFile(path, []byte(`
bookmarks:
  - name: fine
    project: dev
    kind: vm
  - name: no-kind
    project: dev
  - name: no-project
    kind: vm
  - kind: vm
    project: dev
  - name: sweep-needs-no-project
    kind: vm
    sweep: fleet
`), 0o600); err != nil {
		t.Fatal(err)
	}

	list := Load(path).All()
	if len(list) != 2 {
		var names []string
		for _, b := range list {
			names = append(names, b.Name)
		}
		t.Fatalf("kept %v, want only the two that point somewhere", names)
	}
}

// A file that exists and cannot be parsed must not look like an empty one:
// those are opposite situations and only one of them is fine.
func TestAnUnreadableFileIsReportedRatherThanLookingEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bookmarks.yaml")
	if err := os.WriteFile(path, []byte("bookmarks: [this is: not: valid yaml"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := Load(path)
	if s.Err() == nil {
		t.Error("a malformed bookmark file loaded as an empty list with no complaint")
	}
	if len(s.All()) != 0 {
		t.Error("a malformed file produced bookmarks")
	}
}

// A half-written file parses as a shorter list, which is the same as silently
// losing bookmarks — so the write is a rename over a complete temp file, and
// no temp files are left lying beside it.
func TestSavingLeavesNoTemporaryFilesBehind(t *testing.T) {
	dir := t.TempDir()
	s := Load(filepath.Join(dir, "bookmarks.yaml"))
	if err := s.Add(Bookmark{Name: "a", Project: "dev", Kind: "vm"}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "bookmarks.yaml" {
			t.Errorf("left %q beside the bookmark file", e.Name())
		}
	}
}

// This file names the projects somebody works on. Not secret, and not anybody
// else's business either.
func TestTheFileIsNotReadableByEveryoneOnTheMachine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "bookmarks.yaml")
	s := Load(path)
	if err := s.Add(Bookmark{Name: "a", Project: "dev", Kind: "vm"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("bookmark file mode is %04o, want no group or world access", mode)
	}
}

// The bookmark file belongs beside the config it was saved against, so a
// -config pointing somewhere else gets its own bookmarks rather than the
// default ones.
func TestTheBookmarkFileSitsBesideItsConfig(t *testing.T) {
	got := PathFor("/home/someone/.config/g9s/config.yaml")
	want := filepath.Join("/home/someone/.config/g9s", "bookmarks.yaml")
	if got != want {
		t.Errorf("PathFor = %q, want %q", got, want)
	}
	if PathFor("") != "bookmarks.yaml" {
		t.Errorf("PathFor(\"\") = %q", PathFor(""))
	}
}

// A store that never loaded must not panic when something is saved to it.
func TestANilStoreRefusesRatherThanPanics(t *testing.T) {
	var s *Store
	if err := s.Add(Bookmark{Name: "a", Project: "dev", Kind: "vm"}); err == nil {
		t.Error("adding to a nil store succeeded")
	}
	if s.All() != nil || s.Err() != nil || s.Path() != "" {
		t.Error("a nil store returned something")
	}
}

func TestAddRefusesABookmarkThatPointsNowhere(t *testing.T) {
	s := tempStore(t)
	for _, b := range []Bookmark{
		{Name: "", Project: "dev", Kind: "vm"},
		{Name: "a", Kind: "vm"},
		{Name: "a", Project: "dev"},
		{Name: "a", Kind: "vm", Sweep: "sideways"},
	} {
		if err := s.Add(b); err == nil {
			t.Errorf("stored %+v, which points nowhere", b)
		}
	}
	if len(s.All()) != 0 {
		t.Errorf("got %d bookmarks after only invalid ones", len(s.All()))
	}
	if _, err := os.Stat(s.Path()); err == nil {
		t.Error("a rejected bookmark still wrote the file")
	} else if !strings.Contains(err.Error(), "no such file") && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
