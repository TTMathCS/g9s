package gcp

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// maturityCounts matches the sentence in README.md that states how many kinds
// exist. Anchored on the prose around the numbers rather than on the numbers
// themselves, so it keeps matching as they change.
var maturityCounts = regexp.MustCompile(
	`contains (\d+) top-level\s*\n?>?\s*resource kinds and (\d+) drill-down listings`)

// TestREADMEKindCountsMatchTheCode pins two numbers a reader takes at face
// value and nothing else checks. They are written by hand, they were already
// wrong once, and being wrong is invisible: the prose reads fine either way and
// only someone counting the dashboard would notice.
func TestREADMEKindCountsMatchTheCode(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}

	match := maturityCounts.FindStringSubmatch(string(raw))
	if match == nil {
		t.Fatal("README.md no longer states the kind counts in the expected form — " +
			"update this test's pattern, or the counts have silently disappeared")
	}

	statedKinds, _ := strconv.Atoi(match[1])
	statedChildren, _ := strconv.Atoi(match[2])

	if got := len(Listers()); statedKinds != got {
		t.Errorf("README says %d top-level kinds, the code registers %d", statedKinds, got)
	}
	if got := len(Children()); statedChildren != got {
		t.Errorf("README says %d drill-downs, the code registers %d", statedChildren, got)
	}
}
