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
// Some children read what the parent listing already fetched and cost no API
// call at all; others fetch. The interface takes a context and client options
// so both work, and the difference is worth thinking about per kind: a
// drill-down is the right home for a query too expensive to run for every row
// on every refresh but cheap enough for one row on demand. The load balancer's
// backend health is the clearest case — it walks a chain of four resources to
// answer for a single rule.
type ChildLister interface {
	Kind() Kind
	// ParentKind is the Kind.ID of the listing this drills down from.
	ParentKind() string
	List(ctx context.Context, cfg *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error)
}

// Children returns the registered drill-downs, in display order.
func Children() []ChildLister {
	return []ChildLister{
		AttachedDiskLister{},
		NodePoolLister{},
		TopicSubscriptionLister{},
		SQLDatabaseLister{},
		SQLUserLister{},
		BigQueryTableLister{},
		CloudRunRevisionLister{},
		CloudRunExecutionLister{},
		SubnetLister{},
		LoadBalancerHealthLister{},
		DNSRecordLister{},
		SecretVersionLister{},
		ServiceAccountKeyLister{},
	}
}

// ChildrenOf returns every drill-down registered for a kind, in display order.
//
// More than one is allowed, because more than one is sometimes the honest
// answer: a Cloud SQL instance holds databases and users, and neither is a
// sub-listing of the other. enter opens the first, and tab moves between them
// the same way it moves between top-level kinds.
func ChildrenOf(kindID string) []ChildLister {
	if kindID == "" {
		return nil
	}
	var out []ChildLister
	for _, c := range Children() {
		if c.ParentKind() == kindID {
			out = append(out, c)
		}
	}
	return out
}

// ChildOf returns the first drill-down for a kind, if it has one.
func ChildOf(kindID string) (ChildLister, bool) {
	children := ChildrenOf(kindID)
	if len(children) == 0 {
		return nil, false
	}
	return children[0], true
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
