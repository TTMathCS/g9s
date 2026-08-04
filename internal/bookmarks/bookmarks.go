// Package bookmarks stores the places somebody goes back to.
//
// A bookmark is a place rather than a resource: a project, a kind, and the
// filter that was typed. The resource a filter matches changes between
// sessions; the question does not, and the question is what people retype ten
// times a day.
//
// Deliberately a file of its own rather than a section of config.yaml. The
// config is hand-written and full of comments, and rewriting it from a struct
// would quietly delete every one of them the first time somebody saved a
// bookmark. This file is g9s's to own and rewrite.
package bookmarks

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxBytes bounds the file. Bookmarks are a few dozen short lines; anything
// past this is a mistake or a pipe.
const maxBytes = 1 << 18

// Sweep names the cross-project shapes a bookmark can point at.
const (
	// SweepNone is an ordinary per-project table.
	SweepNone = ""
	// SweepFleet is `:fleet <kind>`.
	SweepFleet = "fleet"
	// SweepDiff is `:diff <kind>`.
	SweepDiff = "diff"
)

// Bookmark is one saved place.
type Bookmark struct {
	// Name is what the user typed. It is the identity: saving over a name
	// replaces it, which is what somebody refining a filter expects.
	Name string `yaml:"name"`
	// Project is the project name from the config, empty for a sweep — a sweep
	// is about every project by definition.
	Project string `yaml:"project,omitempty"`
	// Kind is the resource kind id.
	Kind string `yaml:"kind"`
	// Filter is the query that was in the filter box, if any.
	Filter string `yaml:"filter,omitempty"`
	// Sweep is SweepNone, SweepFleet or SweepDiff.
	Sweep string `yaml:"sweep,omitempty"`
}

// Valid reports whether a bookmark points anywhere.
//
// Checked on load rather than trusted: this file can be hand-edited, and a
// bookmark with no kind would open a screen showing nothing with no way to
// tell why.
func (b Bookmark) Valid() bool {
	if strings.TrimSpace(b.Name) == "" || strings.TrimSpace(b.Kind) == "" {
		return false
	}
	switch b.Sweep {
	case SweepNone:
		return strings.TrimSpace(b.Project) != ""
	case SweepFleet, SweepDiff:
		return true
	}
	return false
}

// Store is the bookmark file, loaded.
type Store struct {
	path string
	list []Bookmark
	// loadErr is a file that exists and could not be read. Kept rather than
	// returned, because a broken bookmark file must not stop g9s starting —
	// but it must not look like an empty one either.
	loadErr error
}

// PathFor returns the bookmark file that belongs beside a config file.
func PathFor(configPath string) string {
	if strings.TrimSpace(configPath) == "" {
		return "bookmarks.yaml"
	}
	return filepath.Join(filepath.Dir(configPath), "bookmarks.yaml")
}

// Load reads the bookmark file. A file that is not there is not an error: it
// is the normal state of a new installation.
func Load(path string) *Store {
	s := &Store{path: path}

	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	if err != nil {
		s.loadErr = err
		return s
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		s.loadErr = err
		return s
	}
	if len(raw) > maxBytes {
		s.loadErr = fmt.Errorf("%s is larger than %d bytes", path, maxBytes)
		return s
	}

	var file struct {
		Bookmarks []Bookmark `yaml:"bookmarks"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		s.loadErr = fmt.Errorf("parsing %s: %w", path, err)
		return s
	}

	for _, b := range file.Bookmarks {
		// A hand-edited entry that points nowhere is dropped rather than
		// listed: a row that cannot be opened is worse than a row that is not
		// there.
		if b.Valid() {
			s.list = append(s.list, b)
		}
	}
	return s
}

// All returns the bookmarks, in the order they will be shown.
func (s *Store) All() []Bookmark {
	if s == nil {
		return nil
	}
	return append([]Bookmark(nil), s.list...)
}

// Err returns the reason the file could not be read, if it could not.
func (s *Store) Err() error {
	if s == nil {
		return nil
	}
	return s.loadErr
}

// Path returns the file backing this store.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Add saves a bookmark, replacing any with the same name.
//
// Replacing rather than appending: somebody saving `errors` twice is refining
// it, and two rows with the same name and different destinations is a menu
// nobody can use.
func (s *Store) Add(b Bookmark) error {
	if s == nil {
		return errors.New("no bookmark store")
	}
	if !b.Valid() {
		return errors.New("a bookmark needs a name and somewhere to point")
	}

	replaced := false
	for i := range s.list {
		if strings.EqualFold(s.list[i].Name, b.Name) {
			s.list[i], replaced = b, true
			break
		}
	}
	if !replaced {
		s.list = append(s.list, b)
	}
	return s.save()
}

// Remove deletes a bookmark by name.
func (s *Store) Remove(name string) error {
	if s == nil {
		return errors.New("no bookmark store")
	}
	kept := s.list[:0]
	for _, b := range s.list {
		if !strings.EqualFold(b.Name, name) {
			kept = append(kept, b)
		}
	}
	s.list = kept
	return s.save()
}

// save writes the file, replacing it atomically.
//
// Temp file and rename rather than truncate and write: a crash or a full disk
// halfway through the second one leaves a half-written file that parses as a
// shorter list, which is the same as silently losing bookmarks.
func (s *Store) save() error {
	if strings.TrimSpace(s.path) == "" {
		return errors.New("no bookmark file path")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	out, err := yaml.Marshal(struct {
		Bookmarks []Bookmark `yaml:"bookmarks"`
	}{Bookmarks: s.list})
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".bookmarks-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has succeeded

	// 0600 for the same reason the config is checked for it: this names the
	// projects somebody works on, which is not secret and is not anybody
	// else's business either.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}
