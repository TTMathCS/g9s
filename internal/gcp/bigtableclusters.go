package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	bigtableadmin "google.golang.org/api/bigtableadmin/v2"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// BigtableClusterLister is the clusters inside one Bigtable instance.
//
// One call, made when you open the instance. This is where everything the
// instance row cannot show lives: the nodes being paid for, the zone the data
// is in, and whether there is more than one cluster at all — which is the
// difference between a replicated instance and a single point of failure.
//
// Node count is the bill. Bigtable charges per node per hour whatever the
// throughput, so an instance sized for a launch that never came is the same
// kind of finding as an unattached disk, and it is invisible one level up.
type BigtableClusterLister struct{}

func (BigtableClusterLister) ParentKind() string { return "bigtable" }

func (BigtableClusterLister) Kind() Kind {
	return Kind{
		ID:    "bigtableclusters",
		Title: "Clusters",
		Columns: []Column{
			{Title: "NAME", Width: 3},
			{Title: "LOCATION", Width: 3},
			{Title: "NODES", Width: 2},
			{Title: "AUTOSCALING", Width: 3},
			{Title: "STORAGE", Width: 2},
			{Title: "STATE", Width: 2},
		},
	}
}

func (BigtableClusterLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	instance, ok := parent.Raw.(*bigtableadmin.Instance)
	if !ok || instance.Name == "" {
		return Result{}, fmt.Errorf("no Bigtable instance data for %s", parent.Name)
	}

	svc, err := bigtableadmin.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("bigtable client: %w", err)
	}

	var (
		result Result
		failed = map[string]bool{}
	)
	err = svc.Projects.Instances.Clusters.List(instance.Name).
		Pages(ctx, func(page *bigtableadmin.ListClustersResponse) error {
			for _, loc := range page.FailedLocations {
				failed[loc] = true
			}
			for _, c := range page.Clusters {
				if c != nil {
					result.Resources = append(result.Resources, bigtableClusterResource(p, parent.Name, c))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	for _, loc := range sortedKeys(failed) {
		if w := describeFailure(lastSegment(loc), fmt.Errorf("location unreachable")); w != "" {
			result.Warnings = append(result.Warnings, w)
		}
	}

	sortResources(result.Resources)
	return result, nil
}

func bigtableClusterResource(p config.Project, instanceName string, c *bigtableadmin.Cluster) Resource {
	name := lastSegment(c.Name)
	location := lastSegment(c.Location)

	return Resource{
		Name:     name,
		Location: location,
		Status:   bigtableClusterState(c),
		Row: []string{
			name,
			location,
			clusterNodes(c),
			clusterAutoscaling(c),
			clusterStorage(c),
			bigtableClusterState(c),
		},
		Raw: c,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/bigtable/instances/%s/clusters?project=%s",
			url.PathEscape(instanceName), url.QueryEscape(p.ProjectID)),
	}
}

func bigtableClusterState(c *bigtableadmin.Cluster) string {
	switch c.State {
	case "", "STATE_NOT_KNOWN":
		return "UNKNOWN"
	default:
		return c.State
	}
}

// clusterNodes is the number being billed by the hour.
//
// Zero is not "unknown" here: an autoscaled cluster reports the nodes it is
// currently running, and a cluster that reports none is one the API did not
// answer for.
func clusterNodes(c *bigtableadmin.Cluster) string {
	if c.ServeNodes <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", c.ServeNodes)
}

// clusterAutoscaling says whether the node count is a setting or a reading.
func clusterAutoscaling(c *bigtableadmin.Cluster) string {
	if c.ClusterConfig == nil || c.ClusterConfig.ClusterAutoscalingConfig == nil {
		return "off"
	}
	limits := c.ClusterConfig.ClusterAutoscalingConfig.AutoscalingLimits
	if limits == nil {
		return "on"
	}
	return fmt.Sprintf("%d–%d nodes", limits.MinServeNodes, limits.MaxServeNodes)
}

// clusterStorage distinguishes SSD from HDD, which is a tenfold price
// difference per gigabyte and cannot be changed after the cluster is created.
func clusterStorage(c *bigtableadmin.Cluster) string {
	switch strings.ToUpper(c.DefaultStorageType) {
	case "SSD":
		return "SSD"
	case "HDD":
		return "HDD"
	default:
		return "-"
	}
}
