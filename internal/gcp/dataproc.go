package gcp

import (
	"context"
	"fmt"
	"net/url"

	dataproc "cloud.google.com/go/dataproc/v2/apiv1"
	"cloud.google.com/go/dataproc/v2/apiv1/dataprocpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// DataprocLister lists Dataproc clusters.
//
// Dataproc has no aggregated list and, unlike most GCP APIs, its endpoint is
// regional: a request for region X sent to the default endpoint returns
// nothing rather than an error. Every region therefore needs its own client
// pointed at <region>-dataproc.googleapis.com.
type DataprocLister struct{}

func (DataprocLister) Kind() Kind {
	return Kind{
		ID:    "dataproc",
		Title: "Dataproc Clusters",
		Columns: []Column{
			{Title: "NAME", Width: 6},
			{Title: "REGION", Width: 3},
			{Title: "STATE", Width: 2},
			{Title: "WORKERS", Width: 2},
			{Title: "IMAGE", Width: 3},
			{Title: "AGE", Width: 2},
		},
	}
}

func (DataprocLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	regions := cfg.DataprocRegions(p)

	result := fanOut(ctx, regions, func(ctx context.Context, region string) (Result, error) {
		regionOpts := append([]option.ClientOption{option.WithEndpoint(dataprocEndpoint(region))}, opts...)

		client, err := dataproc.NewClusterControllerClient(ctx, regionOpts...)
		if err != nil {
			return Result{}, fmt.Errorf("client: %w", err)
		}
		defer client.Close()

		it := client.ListClusters(ctx, &dataprocpb.ListClustersRequest{
			ProjectId: p.ProjectID,
			Region:    region,
		})

		var out Result
		for {
			cluster, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return out, err
			}
			out.Resources = append(out.Resources, clusterResource(p, region, cluster))
		}
		return out, nil
	})

	if !cfg.HasDataprocRegions(p) {
		// Only the implicit global sweep ran; regional clusters were never
		// looked for, and that has to be visible.
		result.Warnings = append(result.Warnings,
			"only the global region was swept — set projects[].regions or defaults.regions to cover regional clusters")
	}
	return result, nil
}

func dataprocEndpoint(region string) string {
	if region == "global" {
		return "dataproc.googleapis.com:443"
	}
	return fmt.Sprintf("%s-dataproc.googleapis.com:443", region)
}

func clusterResource(p config.Project, region string, c *dataprocpb.Cluster) Resource {
	name := c.GetClusterName()
	state := c.GetStatus().GetState().String()

	created := "-"
	if ts := c.GetStatus().GetStateStartTime(); ts != nil {
		created = shortDuration(timeSince(ts.AsTime()))
	}

	return Resource{
		Name:     name,
		Location: region,
		Status:   state,
		Row: []string{
			name,
			region,
			state,
			workerSummary(c),
			c.GetConfig().GetSoftwareConfig().GetImageVersion(),
			created,
		},
		Raw: c,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/dataproc/clusters/%s/monitoring?region=%s&project=%s",
			url.PathEscape(name), url.QueryEscape(region), url.QueryEscape(p.ProjectID)),
	}
}

// workerSummary renders primary and secondary worker counts as "8 (+4)".
func workerSummary(c *dataprocpb.Cluster) string {
	primary := c.GetConfig().GetWorkerConfig().GetNumInstances()
	secondary := c.GetConfig().GetSecondaryWorkerConfig().GetNumInstances()
	if secondary > 0 {
		return fmt.Sprintf("%d (+%d)", primary, secondary)
	}
	return fmt.Sprintf("%d", primary)
}
