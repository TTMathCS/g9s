package gcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func permissionsDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "PERMISSIONS.md"))
	if err != nil {
		t.Fatalf("reading PERMISSIONS.md: %v", err)
	}
	return string(raw)
}

// A kind nobody can get access to is a kind nobody can use, and the failure is
// quiet: a missing API shows an empty table and a missing permission shows one
// denied scope, neither of which tells a support engineer what to ask their
// administrator for. So every registered kind has to appear in the reference,
// and adding a lister without documenting its grant fails here rather than in
// somebody's first session.
func TestEveryTopLevelKindIsDocumented(t *testing.T) {
	doc := permissionsDoc(t)

	for _, l := range Listers() {
		title := l.Kind().Title
		if !strings.Contains(doc, "| "+title+" |") {
			t.Errorf("PERMISSIONS.md has no row for the %q kind", title)
		}
	}
}

// Child listings are documented with the parent they open from, because their
// titles are not unique on their own — "Databases" is both a Cloud SQL and a
// Spanner drill-down, and they need different grants.
func TestEveryDrillDownIsDocumentedAgainstItsParent(t *testing.T) {
	doc := permissionsDoc(t)

	parentTitle := map[string]string{}
	for _, l := range Listers() {
		parentTitle[l.Kind().ID] = l.Kind().Title
	}

	for _, c := range Children() {
		child, parent := c.Kind().Title, parentTitle[c.ParentKind()]
		if parent == "" {
			t.Errorf("child %q claims parent kind %q, which is not registered", child, c.ParentKind())
			continue
		}

		var found bool
		for _, line := range strings.Split(doc, "\n") {
			if strings.HasPrefix(line, "| "+child+" |") && strings.Contains(line, parent) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("PERMISSIONS.md has no row for the %q drill-down of %q", child, parent)
		}
	}
}

// The one thing this document must never do is tell someone to grant access to
// secret values. g9s reads secret and version metadata and deliberately does
// not read payloads, so the accessor permission would hand out reach the tool
// does not use and cannot need.
func TestPermissionsDocNeverAsksForSecretPayloadAccess(t *testing.T) {
	doc := permissionsDoc(t)

	for _, line := range strings.Split(doc, "\n") {
		if !strings.Contains(line, "secretmanager.versions.access") &&
			!strings.Contains(line, "secretAccessor") {
			continue
		}
		// Naming it in order to warn against it is the point, so the mention
		// only counts as a problem when it is not being ruled out.
		if !strings.Contains(line, "not") && !strings.Contains(line, "never") {
			t.Errorf("PERMISSIONS.md appears to ask for secret payload access:\n%s", line)
		}
	}
}
