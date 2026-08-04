package gcp

import (
	"errors"
	"testing"

	"github.com/TTMathCS/g9s/internal/config"
)

func namedRow(name string, status string) Resource {
	return Resource{Name: name, Location: "us-central1-a", Status: status, Row: []string{name}}
}

func snapshot(p config.Project, names ...string) ProjectSnapshot {
	s := ProjectSnapshot{Project: p}
	for _, n := range names {
		s.Result.Resources = append(s.Result.Resources, namedRow(n, "RUNNING"))
	}
	return s
}

func compareProjects() []config.Project {
	return []config.Project{
		{Name: "dev", ProjectID: "acme-dev-1"},
		{Name: "uat", ProjectID: "acme-uat-1"},
		{Name: "prod", ProjectID: "acme-prod-1"},
	}
}

// The whole feature turns on this. Environment-suffixed names are the normal
// case, and matching on the raw name would give a diagonal table where every
// resource appears in exactly one project — technically true, and useless.
func TestEnvironmentSuffixedNamesLineUpAsOneRow(t *testing.T) {
	p := compareProjects()
	c := Compare(FleetResult{Kind: Kind{ID: "vm"}, Snapshots: []ProjectSnapshot{
		snapshot(p[0], "api-dev-01", "worker-dev"),
		snapshot(p[1], "api-uat-01", "worker-uat"),
		snapshot(p[2], "api-prod-01", "worker-prod"),
	}})

	if len(c.Rows) != 2 {
		var keys []string
		for _, r := range c.Rows {
			keys = append(keys, r.Key)
		}
		t.Fatalf("got %d rows %v, want api and worker matched across all three", len(c.Rows), keys)
	}
	for _, r := range c.Rows {
		if !r.Uniform() {
			t.Errorf("row %q is present in %d of %d, want all three", r.Key, r.Present, r.Read)
		}
	}
}

// The project's own name is environment noise too: `data-warehouse` in
// `data-prod` and in `data-dev` is one thing, and only stripping both the
// environment word and the project word gets there.
func TestProjectNameSegmentsAreStrippedFromResourceNames(t *testing.T) {
	projects := []config.Project{
		{Name: "data-dev", ProjectID: "acme-data-dev"},
		{Name: "data-prod", ProjectID: "acme-data-prod"},
	}
	c := Compare(FleetResult{Snapshots: []ProjectSnapshot{
		snapshot(projects[0], "data-warehouse-dev"),
		snapshot(projects[1], "data-warehouse-prod"),
	}})

	if len(c.Rows) != 1 {
		t.Fatalf("got %d rows, want one — the same warehouse in both", len(c.Rows))
	}
	if !c.Rows[0].Uniform() {
		t.Errorf("row %q present in %d of %d", c.Rows[0].Key, c.Rows[0].Present, c.Rows[0].Read)
	}
}

// The single most consequential thing this table can get wrong. A resource
// missing from a project nobody could read is not missing — it is unread, and
// rendering the two the same invents a finding out of a permission error.
func TestAnUnreadProjectIsNeverReportedAsMissing(t *testing.T) {
	p := compareProjects()
	c := Compare(FleetResult{Snapshots: []ProjectSnapshot{
		snapshot(p[0], "api-dev"),
		{Project: p[1], Err: errors.New("permission denied")},
		{Project: p[2], Skipped: true, SkipReason: "not logged in"},
	}})

	if len(c.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(c.Rows))
	}
	row := c.Rows[0]
	if row.Cells[1].State != CellUnknown {
		t.Errorf("the failed project reads as %v, want unknown", row.Cells[1].State)
	}
	if row.Cells[2].State != CellUnknown {
		t.Errorf("the skipped project reads as %v, want unknown", row.Cells[2].State)
	}
	// And with only one project read, one project having it is not a gap.
	if row.Gap() {
		t.Error("a row present in the only project that could be read counts as a difference")
	}
	if row.Read != 1 {
		t.Errorf("Read = %d, want 1 — two projects never answered", row.Read)
	}
}

// The finding is the row that does not line up, and making the reader scroll
// past thirty-seven that do is making them do the comparison themselves.
func TestRowsThatDifferSortAboveRowsThatMatch(t *testing.T) {
	p := compareProjects()
	c := Compare(FleetResult{Snapshots: []ProjectSnapshot{
		snapshot(p[0], "aaa-dev", "zzz-dev"),
		snapshot(p[1], "aaa-uat", "zzz-uat"),
		snapshot(p[2], "aaa-prod"),
	}})

	if len(c.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(c.Rows))
	}
	if !c.Rows[0].Gap() {
		t.Errorf("first row is %q, want the one prod is missing", c.Rows[0].Key)
	}
	if c.Rows[0].Key != "zzz" {
		t.Errorf("first row is %q, want zzz — the gap, despite sorting last alphabetically", c.Rows[0].Key)
	}

	uniform, gaps, unread := c.Counts()
	if uniform != 1 || gaps != 1 || unread != 0 {
		t.Errorf("counts = %d uniform, %d gaps, %d unread; want 1/1/0", uniform, gaps, unread)
	}
}

// Two distinct resources in one project reaching the same key must not become
// one row. A row that fails to line up is a small annoyance; two different
// machines merged into one row is a wrong answer.
func TestTwoResourcesInOneProjectNeverMergeIntoOneRow(t *testing.T) {
	p := []config.Project{{Name: "dev", ProjectID: "acme-dev-1"}}
	c := Compare(FleetResult{Snapshots: []ProjectSnapshot{
		snapshot(p[0], "api", "api-dev"),
	}})

	if len(c.Rows) != 2 {
		t.Fatalf("got %d rows, want 2 — `api` and `api-dev` are different machines", len(c.Rows))
	}
	names := map[string]bool{}
	for _, r := range c.Rows {
		names[r.Cells[0].Name] = true
	}
	if !names["api"] || !names["api-dev"] {
		t.Errorf("rows carry names %v, want both real names", names)
	}
}

// A resource named only after its environment has nothing left after
// stripping, and inventing an empty key would collapse every such resource in
// the estate into one row.
func TestAResourceNamedOnlyForItsEnvironmentKeepsItsName(t *testing.T) {
	p := compareProjects()
	c := Compare(FleetResult{Snapshots: []ProjectSnapshot{
		snapshot(p[0], "dev"),
		snapshot(p[2], "prod"),
	}})

	if len(c.Rows) != 2 {
		t.Fatalf("got %d rows, want 2 distinct rows rather than one empty key", len(c.Rows))
	}
	for _, r := range c.Rows {
		if r.Key == "" {
			t.Error("a row has an empty key")
		}
	}
}

// Cells are positional, so column i is project i on every row — otherwise the
// table reads one project's state under another's heading.
func TestCellsAreParallelToTheProjectColumns(t *testing.T) {
	p := compareProjects()
	c := Compare(FleetResult{Snapshots: []ProjectSnapshot{
		snapshot(p[0], "api-dev"),
		snapshot(p[1]),
		snapshot(p[2], "api-prod"),
	}})

	if len(c.Projects) != 3 {
		t.Fatalf("got %d project columns, want 3", len(c.Projects))
	}
	row := c.Rows[0]
	if len(row.Cells) != 3 {
		t.Fatalf("row has %d cells for 3 projects", len(row.Cells))
	}
	if row.Cells[0].Name != "api-dev" || row.Cells[2].Name != "api-prod" {
		t.Errorf("cells = %q / %q, want the dev and prod names in their own columns",
			row.Cells[0].Name, row.Cells[2].Name)
	}
	if row.Cells[1].State != CellAbsent {
		t.Errorf("uat was read and has nothing, reads as %v, want absent", row.Cells[1].State)
	}
	if !row.Gap() {
		t.Error("a resource in two of three read projects is not reported as a difference")
	}
}
