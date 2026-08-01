package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// ComputeDiskLister lists persistent disks.
//
// One aggregatedList covers every zone and region server-side, the same trick
// Compute instances use.
//
// The reason this is a top-level kind and not a drill-down from the VM that
// uses it: the disks worth finding are the ones no VM uses. An unattached disk
// bills at the same rate as an attached one, forever, and it has no parent row
// to hang off — so a per-instance listing is exactly the view that cannot show
// it. That is also why ATTACHED TO leads with a dash rather than a name, and
// why the status says UNATTACHED rather than treating "no users" as ordinary.
type ComputeDiskLister struct{}

func (ComputeDiskLister) Kind() Kind {
	return Kind{
		ID:    "disks",
		Title: "Compute Disks",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "LOCATION", Width: 2},
			{Title: "SIZE", Width: 1},
			{Title: "TYPE", Width: 2},
			{Title: "ATTACHED TO", Width: 4},
			{Title: "IDLE", Width: 2},
			{Title: "STATUS", Width: 2},
		},
	}
}

func (ComputeDiskLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	client, err := compute.NewDisksRESTClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("disks client: %w", err)
	}
	defer client.Close()

	it := client.AggregatedList(ctx, &computepb.AggregatedListDisksRequest{
		Project: p.ProjectID,
	})
	return collectAggregated(it,
		func(pair compute.DisksScopedListPair) (string, []*computepb.Disk) {
			return lastSegment(pair.Key), pair.Value.GetDisks()
		},
		func(scope string, d *computepb.Disk) Resource {
			return diskResource(p, scope, d)
		})
}

func diskResource(p config.Project, scope string, d *computepb.Disk) Resource {
	name := d.GetName()

	// A regional disk is replicated across two zones and its scope comes back
	// as the region; a zonal one as the zone. Either way the scope is where it
	// lives and where the Console expects to find it.
	users := d.GetUsers()

	return Resource{
		Name:     name,
		Location: scope,
		Status:   diskStatus(d),
		Row: []string{
			name,
			scope,
			fmt.Sprintf("%dGB", d.GetSizeGb()),
			lastSegment(d.GetType()),
			diskAttachment(users),
			diskIdleFor(d),
			diskStatus(d),
		},
		Raw:        d,
		ConsoleURL: diskConsoleURL(p, scope, name),
	}
}

// diskConsoleURL addresses a disk by zone or by region depending on which it
// is. Getting it wrong produces a link to a page that does not exist.
func diskConsoleURL(p config.Project, scope, name string) string {
	kind := "zones"
	if isRegionScope(scope) {
		kind = "regions"
	}
	return fmt.Sprintf(
		"https://console.cloud.google.com/compute/disksDetail/%s/%s/disks/%s?project=%s",
		kind, url.PathEscape(scope), url.PathEscape(name), url.QueryEscape(p.ProjectID))
}

// isRegionScope tells a region from a zone. A zone is its region plus a single
// letter suffix, which is the only thing distinguishing "us-central1" from
// "us-central1-a" without a lookup.
func isRegionScope(scope string) bool {
	i := strings.LastIndex(scope, "-")
	if i < 0 {
		return false
	}
	return len(scope)-i != 2
}

// diskAttachment names what is using the disk, or says plainly that nothing is.
func diskAttachment(users []string) string {
	switch len(users) {
	case 0:
		return "-"
	case 1:
		return lastSegment(users[0])
	default:
		// Read-only sharing across several instances. Naming one and hiding the
		// rest would make a shared disk look exclusive.
		names := make([]string, 0, len(users))
		for _, u := range users {
			names = append(names, lastSegment(u))
		}
		return strings.Join(names, ",")
	}
}

// diskIdleFor is how long an unattached disk has been unattached.
//
// This is the number the kind exists for. "Unattached" alone invites the reply
// that it is about to be used; "unattached for 240 days" does not. A disk that
// has never been attached reports no detach timestamp at all, which is its own
// answer — it was created and forgotten.
func diskIdleFor(d *computepb.Disk) string {
	if len(d.GetUsers()) > 0 {
		return "-"
	}
	if ts := d.GetLastDetachTimestamp(); ts != "" {
		return age(ts)
	}
	if ts := d.GetCreationTimestamp(); ts != "" {
		return "never used, " + age(ts)
	}
	return "-"
}

// diskStatus reports UNATTACHED over the disk's own READY, because a ready disk
// nothing is using is the finding, and READY is what hides it.
func diskStatus(d *computepb.Disk) string {
	status := d.GetStatus()
	if status == "" {
		status = "UNKNOWN"
	}
	// Only a healthy disk can be meaningfully idle. One that is still being
	// created or is failing has a more urgent thing to say.
	if status == "READY" && len(d.GetUsers()) == 0 {
		return "UNATTACHED"
	}
	return status
}
