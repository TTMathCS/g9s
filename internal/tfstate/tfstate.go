// Package tfstate reads Terraform state well enough to answer one question:
// is this resource managed?
//
// Deliberately not a Terraform client and not a state parser in general. A
// state file is the most secret-dense artifact most infrastructure produces —
// database passwords, service account keys, TLS private keys and every other
// value a provider round-trips all sit in `attributes` in plain text — so this
// package reads the identity of each resource and throws the attributes away
// before anything else can see them.
//
// What survives parsing is a set of (type, name) pairs. That is enough to mark
// a row managed and not enough to leak anything.
package tfstate

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// MaxBytes bounds a state file. Real ones run from kilobytes to a few
// megabytes; past this is a mistake or something that is not a state file, and
// reading it unbounded in a TUI is an easy way to be handed a swap storm.
const MaxBytes = 32 << 20

// Index is the set of resources a state file manages.
//
// A set of names per type, and nothing else. No attributes, no IDs that embed
// attributes, no raw JSON kept for later — the whole design is that there is
// nothing in here that could be printed by accident.
type Index struct {
	byType map[string]map[string]bool
	// count is the number of managed resource instances seen, including types
	// nothing maps to. It is what makes "0 managed" distinguishable from "the
	// state file was empty or was not the one you meant".
	count int
}

// New returns an empty index, ready to Merge into.
func New() *Index { return &Index{byType: map[string]map[string]bool{}} }

// Parse reads a Terraform state document.
//
// Data sources and outputs are skipped: a data source is something Terraform
// reads rather than owns, and treating one as managed would report a resource
// as tracked by the very config that merely looks at it.
func Parse(r io.Reader) (*Index, error) {
	idx := &Index{byType: map[string]map[string]bool{}}

	// A struct rather than map[string]any: the fields not named here are
	// skipped by the decoder instead of being materialised, which is what
	// keeps every attribute value out of memory.
	var doc struct {
		Version   int `json:"version"`
		Resources []struct {
			Mode      string `json:"mode"`
			Type      string `json:"type"`
			Name      string `json:"name"`
			Instances []struct {
				// Attributes is decoded only to read `name` out of it, and is
				// dropped the moment that is done. Nothing else in here is
				// looked at, kept or returned.
				Attributes struct {
					Name string `json:"name"`
				} `json:"attributes"`
			} `json:"instances"`
		} `json:"resources"`
		// Terraform 0.11 and earlier wrote a different shape. Recognised so
		// the message can say so rather than reporting an empty state, which
		// would read as "nothing is managed".
		Modules []json.RawMessage `json:"modules"`
	}

	dec := json.NewDecoder(io.LimitReader(r, MaxBytes+1))
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing terraform state: %w", err)
	}

	if len(doc.Resources) == 0 && len(doc.Modules) > 0 {
		return nil, fmt.Errorf("this is Terraform 0.11 state (version %d); g9s reads version 4 and later", doc.Version)
	}

	for _, res := range doc.Resources {
		// mode is "managed" or "data"; older files written by 0.12 omit it for
		// managed resources, so an empty mode counts as managed.
		if res.Mode == "data" {
			continue
		}
		for _, inst := range res.Instances {
			// The resource's real name in GCP, which is the attribute — not
			// the Terraform label, which is whatever the author called the
			// block and matches nothing in the cloud.
			name := strings.TrimSpace(inst.Attributes.Name)
			idx.count++
			if name == "" {
				continue
			}
			set, ok := idx.byType[res.Type]
			if !ok {
				set = map[string]bool{}
				idx.byType[res.Type] = set
			}
			set[name] = true
		}
	}
	return idx, nil
}

// Merge folds another index into this one, for an estate whose state is split
// across several files.
func (i *Index) Merge(other *Index) {
	if i == nil || other == nil {
		return
	}
	for tfType, names := range other.byType {
		set, ok := i.byType[tfType]
		if !ok {
			set = map[string]bool{}
			i.byType[tfType] = set
		}
		for name := range names {
			set[name] = true
		}
	}
	i.count += other.count
}

// Has reports whether a resource of this Terraform type and name is managed.
func (i *Index) Has(tfType, name string) bool {
	if i == nil {
		return false
	}
	return i.byType[tfType][name]
}

// HasAny reports whether any of these Terraform types manages this name.
//
// Several types can back one g9s kind — a global and a regional flavour of the
// same thing, or a resource a provider renamed between major versions.
func (i *Index) HasAny(tfTypes []string, name string) bool {
	for _, t := range tfTypes {
		if i.Has(t, name) {
			return true
		}
	}
	return false
}

// Count is how many managed resource instances the state described.
func (i *Index) Count() int {
	if i == nil {
		return 0
	}
	return i.count
}

// Types lists the Terraform types seen, sorted. Used to say what a state file
// actually contained when nothing in it matched the open table.
func (i *Index) Types() []string {
	if i == nil {
		return nil
	}
	out := make([]string, 0, len(i.byType))
	for t := range i.byType {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Empty reports whether the index manages nothing at all.
func (i *Index) Empty() bool { return i.Count() == 0 }
