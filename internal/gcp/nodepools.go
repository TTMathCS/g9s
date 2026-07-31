package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"cloud.google.com/go/container/apiv1/containerpb"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// NodePoolLister is the node pools inside one GKE cluster.
//
// Costs no API call. ListClusters already returns each cluster's node pools
// inline, so the cluster row is carrying them; this reads what is there rather
// than asking again. That is also why node pools are a drill-down and not a
// twenty-fourth top-level kind — the data arrives with the clusters either way,
// and "every node pool in the project", stripped of which cluster each belongs
// to, is not a question anyone asks.
type NodePoolLister struct{}

func (NodePoolLister) ParentKind() string { return "gke" }

func (NodePoolLister) Kind() Kind {
	return Kind{
		ID:    "nodepools",
		Title: "Node Pools",
		Columns: []Column{
			{Title: "NAME", Width: 3},
			{Title: "MACHINE TYPE", Width: 3},
			{Title: "NODES", Width: 1},
			{Title: "AUTOSCALE", Width: 2},
			{Title: "VERSION", Width: 3},
			// "upgrade+repair" is the widest value here and the one worth
			// reading in full — a pool that upgrades but does not repair is a
			// different answer from one that does neither.
			{Title: "UPGRADE", Width: 3},
			{Title: "STATUS", Width: 2},
		},
	}
}

func (NodePoolLister) List(_ context.Context, _ *config.Config, p config.Project, parent Resource, _ []option.ClientOption) (Result, error) {
	cluster, ok := parent.Raw.(*containerpb.Cluster)
	if !ok {
		return Result{}, fmt.Errorf("no cluster data for %s", parent.Name)
	}

	var result Result
	for _, np := range cluster.GetNodePools() {
		result.Resources = append(result.Resources, nodePoolResource(p, cluster, np))
	}
	sortResources(result.Resources)
	return result, nil
}

func nodePoolResource(p config.Project, c *containerpb.Cluster, np *containerpb.NodePool) Resource {
	name := np.GetName()
	status := np.GetStatus().String()

	machine := np.GetConfig().GetMachineType()
	if machine == "" {
		// Autopilot clusters manage node configuration themselves, so there is
		// nothing to report rather than nothing configured.
		machine = "-"
	}
	if np.GetConfig().GetSpot() {
		machine += " (spot)"
	} else if np.GetConfig().GetPreemptible() {
		machine += " (preempt)"
	}

	// The node count is per zone: a pool of 2 across three zones runs six VMs,
	// and reading the 2 as the total is how a cluster ends up mis-sized on
	// paper. Show what is actually running.
	zones := max(1, len(np.GetLocations()))
	nodes := fmt.Sprintf("%d", int(np.GetInitialNodeCount())*zones)

	return Resource{
		Name:     name,
		Location: c.GetLocation(),
		Status:   status,
		Row: []string{
			name,
			machine,
			nodes,
			autoscaleSummary(np.GetAutoscaling(), zones),
			np.GetVersion(),
			upgradeSummary(np.GetManagement()),
			status,
		},
		Raw: np,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/kubernetes/nodepool/%s/%s/%s?project=%s",
			url.PathEscape(c.GetLocation()), url.PathEscape(c.GetName()),
			url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// autoscaleSummary renders the bounds, or says the pool is fixed.
//
// A pool pinned at one size is not a misconfiguration, but it is the reason a
// cluster stops absorbing load, and nothing else on the row says so.
func autoscaleSummary(a *containerpb.NodePoolAutoscaling, zones int) string {
	if a == nil || !a.GetEnabled() {
		return "off"
	}
	// The total_* fields are cluster-wide bounds; the plain ones are per zone.
	// Whichever is set, report the cluster-wide numbers so the column compares
	// with the node count beside it.
	minN, maxN := int(a.GetTotalMinNodeCount()), int(a.GetTotalMaxNodeCount())
	if minN == 0 && maxN == 0 {
		minN, maxN = int(a.GetMinNodeCount())*zones, int(a.GetMaxNodeCount())*zones
	}
	return fmt.Sprintf("%d-%d", minN, maxN)
}

// upgradeSummary reports the two settings that decide whether a pool looks
// after itself, as the short flags they are usually discussed as.
func upgradeSummary(m *containerpb.NodeManagement) string {
	var on []string
	if m.GetAutoUpgrade() {
		on = append(on, "upgrade")
	}
	if m.GetAutoRepair() {
		on = append(on, "repair")
	}
	if len(on) == 0 {
		return "manual"
	}
	return strings.Join(on, "+")
}
