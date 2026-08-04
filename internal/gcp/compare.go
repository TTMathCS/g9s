package gcp

import (
	"sort"
	"strings"

	"github.com/TTMathCS/g9s/internal/config"
)

// Cell is one project's answer about one resource in a comparison.
type Cell struct {
	// Name is the resource's real name in that project, which is usually not
	// the row's key — `api-dev-01` and `api-prod-01` are the same row.
	Name string
	// Status is what that project's listing reported.
	Status string
	// State says whether this project has the resource, does not have it, or
	// was never successfully read.
	State CellState
}

// CellState separates "not there" from "we never found out", which is the
// whole reason a comparison can be trusted.
type CellState int

const (
	// CellUnknown is a project that failed or was skipped. Rendering it the
	// same as absent would invent a finding: a resource missing from a project
	// nobody could read is not missing, it is unread.
	CellUnknown CellState = iota
	// CellAbsent is a project that was read and does not have it.
	CellAbsent
	// CellPresent is a project that has it.
	CellPresent
)

// ComparisonRow is one resource across every project.
type ComparisonRow struct {
	// Key is the normalised name the row is grouped under.
	Key string
	// Cells is parallel to the comparison's projects, so column i is always
	// project i.
	Cells []Cell
	// Present and Read are how many projects have it and how many were read at
	// all. A row present in 2 of 3 read projects is the finding; a row present
	// in 2 of 2 read projects with a third unread is not.
	Present, Read int
}

// Uniform reports whether every project that was read has this resource.
func (r ComparisonRow) Uniform() bool { return r.Read > 0 && r.Present == r.Read }

// Gap reports whether some read project has it and another read project does
// not — the thing a comparison exists to find.
func (r ComparisonRow) Gap() bool { return r.Present > 0 && r.Present < r.Read }

// Comparison is one kind laid out with projects as columns.
//
// The fleet sweep answers "what is out there"; this answers "what is out there
// in one environment and not another", which is a different table rather than
// a different sort of the same one.
type Comparison struct {
	Kind      Kind
	Projects  []config.Project
	Rows      []ComparisonRow
	Snapshots []ProjectSnapshot
}

// Compare turns a fleet sweep into a project-by-project table.
//
// Rows are grouped by a normalised key rather than the raw name, because
// environment-suffixed names are the normal case and exact matching would
// produce a diagonal table where every resource appears in exactly one project
// — technically true, and useless.
func Compare(f FleetResult) Comparison {
	out := Comparison{Kind: f.Kind, Snapshots: f.Snapshots}
	for _, s := range f.Snapshots {
		out.Projects = append(out.Projects, s.Project)
	}

	readable := make([]bool, len(f.Snapshots))
	read := 0
	for i, s := range f.Snapshots {
		readable[i] = !s.Skipped && s.Err == nil
		if readable[i] {
			read++
		}
	}

	// Keys are assembled in a stable order so a collision inside one project
	// resolves the same way every sweep, rather than depending on which zone
	// answered first.
	byKey := map[string]*ComparisonRow{}
	var order []string

	for i, s := range f.Snapshots {
		if !readable[i] {
			continue
		}
		resources := append([]Resource(nil), s.Result.Resources...)
		sort.SliceStable(resources, func(a, b int) bool { return resources[a].Name < resources[b].Name })

		for _, r := range resources {
			key := comparisonKey(r.Name, s.Project)

			// Two resources in one project reaching the same key would merge
			// two distinct things into one row, which is worse than a row that
			// fails to line up. The second one keeps its real name instead.
			if row, seen := byKey[key]; seen && row.Cells[i].State == CellPresent {
				key = strings.ToLower(r.Name)
			}

			row, seen := byKey[key]
			if !seen {
				row = &ComparisonRow{Key: key, Cells: make([]Cell, len(f.Snapshots))}
				for j := range row.Cells {
					if readable[j] {
						row.Cells[j].State = CellAbsent
					}
				}
				byKey[key] = row
				order = append(order, key)
			}
			if row.Cells[i].State != CellPresent {
				row.Present++
			}
			row.Cells[i] = Cell{Name: r.Name, Status: r.Status, State: CellPresent}
		}
	}

	for _, key := range order {
		row := byKey[key]
		row.Read = read
		out.Rows = append(out.Rows, *row)
	}
	sortComparisonRows(out.Rows)
	return out
}

// sortComparisonRows puts the gaps first.
//
// A comparison of forty resources where thirty-seven line up is a table whose
// answer is three rows long, and making the reader find them is making them do
// the comparison themselves.
func sortComparisonRows(rows []ComparisonRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Gap() != rows[j].Gap() {
			return rows[i].Gap()
		}
		return rows[i].Key < rows[j].Key
	})
}

// environmentTokens are the words that name an environment rather than a
// resource. Stripped from both ends of a name so `api-prod` and `dev-api` both
// reduce to `api`.
var environmentTokens = map[string]bool{
	"dev": true, "devel": true, "development": true,
	"uat": true, "qa": true, "test": true, "tst": true, "testing": true,
	"stg": true, "stage": true, "staging": true,
	"prd": true, "prod": true, "production": true,
	"preprod": true, "nonprod": true, "perf": true,
	"sbx": true, "sandbox": true, "demo": true, "int": true,
}

// comparisonKey reduces a resource name to what it would be called in any
// environment.
//
// Two sources of environment noise are removed: the well-known environment
// words, and any segment the project itself is named after — a resource called
// `data-warehouse-prod` in a project called `data-prod` is the same thing as
// `data-warehouse-dev` in `data-dev`, and only stripping both gets there.
//
// This is a heuristic and it is presented as one: the table shows each
// project's real name for the row, so a reader can see when two things were
// lined up that should not have been.
func comparisonKey(name string, p config.Project) string {
	noise := map[string]bool{}
	for _, source := range []string{p.Name, p.ProjectID} {
		for _, seg := range splitName(source) {
			// Numeric project suffixes only: stripping every segment of the
			// project name would leave `data-warehouse` in project `data` as
			// `warehouse`, which is fine, but stripping short generic segments
			// from unrelated resources is not. Length two and up, and never a
			// segment that is the whole resource name.
			if len(seg) >= 2 {
				noise[seg] = true
			}
		}
	}

	segments := splitName(name)
	kept := make([]string, 0, len(segments))
	for _, seg := range segments {
		if environmentTokens[seg] || noise[seg] {
			continue
		}
		kept = append(kept, seg)
	}
	if len(kept) == 0 {
		// Everything was environment noise, which means the name is the
		// environment — a bucket called `dev-data` in project `dev-data`. Its
		// own name is the only honest key left.
		return strings.ToLower(name)
	}
	return strings.Join(kept, "-")
}

// splitName breaks a resource name into the segments people actually compose
// them from.
func splitName(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/'
	})
	return fields
}

// ComparisonCounts summarises a comparison for the honesty line.
func (c Comparison) Counts() (uniform, gaps, unread int) {
	for _, r := range c.Rows {
		switch {
		case r.Gap():
			gaps++
		case r.Uniform():
			uniform++
		}
	}
	for _, s := range c.Snapshots {
		if s.Skipped || s.Err != nil {
			unread++
		}
	}
	return uniform, gaps, unread
}
