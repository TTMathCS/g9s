package gcp

import (
	"context"
	"fmt"

	dataproc "cloud.google.com/go/dataproc/v2/apiv1"
	"cloud.google.com/go/dataproc/v2/apiv1/dataprocpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// maxClusterJobs bounds one cluster's history. Smaller than the per-region cap
// because this is one cluster's worth: past a couple of hundred rows nobody is
// reading, they are searching, and `/` does that better than scrolling.
const maxClusterJobs = 200

// ClusterJobLister is the jobs that ran on one Dataproc cluster.
//
// The jobs kind lists them per region, which is the right axis when a job is
// the thing you are chasing. This is the other one, and it is the axis you are
// already on when a *cluster* is behaving oddly — the region's other clusters
// are noise then, and on a busy region they are most of the table.
//
// Cheaper than the parent kind, not more expensive: ListJobs takes a
// ClusterName filter, so this is one call to one region rather than a fan-out
// across every configured one.
type ClusterJobLister struct{}

func (ClusterJobLister) ParentKind() string { return "dataproc" }

func (ClusterJobLister) Kind() Kind {
	return Kind{
		ID:    "clusterjobs",
		Title: "Jobs",
		Columns: []Column{
			// No CLUSTER column: it is the same on every row here. REGION goes
			// too — a cluster lives in one.
			{Title: "JOB ID", Width: 6},
			{Title: "TYPE", Width: 2},
			{Title: "STATE", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (ClusterJobLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	cluster, ok := parent.Raw.(*dataprocpb.Cluster)
	if !ok {
		return Result{}, fmt.Errorf("no cluster data for %s", parent.Name)
	}

	// The cluster's own region, which the parent row carries. Dataproc's
	// endpoint is regional and a request sent to the wrong one returns nothing
	// rather than an error.
	region := parent.Location
	if region == "" {
		return Result{}, fmt.Errorf("no region for cluster %s", parent.Name)
	}

	regionOpts := append([]option.ClientOption{option.WithEndpoint(dataprocEndpoint(region))}, opts...)
	client, err := dataproc.NewJobControllerClient(ctx, regionOpts...)
	if err != nil {
		return Result{}, fmt.Errorf("dataproc client: %w", err)
	}
	defer client.Close()

	it := client.ListJobs(ctx, &dataprocpb.ListJobsRequest{
		ProjectId: p.ProjectID,
		Region:    region,
		// Server-side, so the cap below applies to this cluster's jobs rather
		// than being spent on other clusters' rows before reaching them.
		ClusterName: cluster.GetClusterName(),
		// Every state: a job that failed twenty minutes ago is usually why
		// someone opened this table.
		JobStateMatcher: dataprocpb.ListJobsRequest_ALL,
	})

	var result Result
	for {
		if len(result.Resources) >= maxClusterJobs {
			// The API documents no ordering, so this is honestly the first N it
			// returned rather than the newest N.
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"stopped after %d jobs — this cluster has more history than the table shows",
				maxClusterJobs))
			break
		}

		job, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			// Whatever arrived before the failure is still real.
			return result, err
		}
		result.Resources = append(result.Resources, clusterJobResource(p, region, job))
	}

	sortDataprocJobsByRecency(result.Resources)
	return result, nil
}

// clusterJobResource is the region-wide row without the two cells that repeat
// under one cluster.
//
// Built from the same function so a job cannot read differently depending on
// which table it is seen from — the type, state and age rules are shared rather
// than reimplemented.
func clusterJobResource(p config.Project, region string, j *dataprocpb.Job) Resource {
	full := dataprocJobResource(p, region, j)

	// Row is JOB ID REGION TYPE CLUSTER STATE AGE; drop region and cluster.
	full.Row = []string{
		full.Row[0],
		full.Row[2],
		full.Row[4],
		full.Row[5],
	}
	return full
}
