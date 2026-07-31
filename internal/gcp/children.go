package gcp

import (
	"context"

	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// ChildLister lists the resources belonging to one parent row.
//
// The reason this exists: the hotkey alphabet runs out at twenty-three kinds,
// and the kinds worth adding next are not project-wide lists at all. Node pools
// belong to a cluster, keys belong to a service account. "Every node pool in the
// project" is the wrong question to build a table around, and giving one a
// top-level tab spends a scarce key on a listing nobody opens cold.
//
// A child is reached with enter on its parent's row, costs no hotkey, and is
// otherwise an ordinary listing: same table, same filter, same describe pane.
//
// Both children registered so far read what the parent listing already fetched,
// so drilling in costs no API call. That is not a rule — List takes a context
// and client options precisely so a child can fetch — it is what these two
// happen to need.
type ChildLister interface {
	Kind() Kind
	// ParentKind is the Kind.ID of the listing this drills down from.
	ParentKind() string
	List(ctx context.Context, cfg *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error)
}

// Children returns the registered drill-downs.
func Children() []ChildLister {
	return []ChildLister{
		NodePoolLister{},
		ServiceAccountKeyLister{},
	}
}

// ChildOf returns the drill-down for a kind, if it has one.
func ChildOf(kindID string) (ChildLister, bool) {
	for _, c := range Children() {
		if c.ParentKind() == kindID {
			return c, true
		}
	}
	return nil, false
}

// BindChild pairs a drill-down with the row it was opened from, giving back an
// ordinary Lister.
//
// This is what keeps drill-downs out of the fetch plumbing. Loading, caching,
// refreshing, the error path and the in-flight token all key off Kind().ID, so
// a bound child that reports an id of its own travels the exact path a
// top-level kind does, with nothing below the UI aware a parent is involved.
func BindChild(child ChildLister, parent Resource) Lister {
	return boundChild{child: child, parent: parent}
}

type boundChild struct {
	child  ChildLister
	parent Resource
}

// Kind qualifies the child's id with the parent's name, because the id is a
// cache key and two clusters' node pools must not overwrite each other. The
// title is left alone: every screen that shows it also shows the parent right
// beside it, so repeating the parent there would only make the line longer.
func (b boundChild) Kind() Kind {
	k := b.child.Kind()
	k.ID = k.ID + "/" + b.parent.Name
	return k
}

func (b boundChild) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	return b.child.List(ctx, cfg, p, b.parent, opts)
}
