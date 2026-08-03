package gcp

import (
	"context"
	"fmt"
	"net/url"
	"sort"

	dataproc "cloud.google.com/go/dataproc/v2/apiv1"
	"cloud.google.com/go/dataproc/v2/apiv1/dataprocpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// DataprocJobLister lists Dataproc jobs.
//
// A cluster list says what exists; this says what it is doing, which is the
// half of the story a running cluster with nothing on it does not tell.
//
// Same regional endpoint problem as the cluster lister — a request for region X
// sent to the default endpoint returns nothing rather than an error — so it
// sweeps the same region list, one client each.
type DataprocJobLister struct{}

// maxDataprocJobsPerRegion bounds one region's listing. Job history is kept
// long after the cluster is gone and there is no time filter on the API, so
// without a bound one busy region can stall the whole refresh.

func (DataprocJobLister) Kind() Kind {
	return Kind{
		ID:    "dataprocjobs",
		Title: "Dataproc Jobs",
		Columns: []Column{
			{Title: "JOB ID", Width: 6},
			{Title: "REGION", Width: 2},
			{Title: "TYPE", Width: 2},
			{Title: "CLUSTER", Width: 4},
			{Title: "STATE", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (DataprocJobLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	maxDataprocJobsPerRegion := cfg.LimitDataprocJobsPerRegion()
	regions := cfg.DataprocRegions(p)

	result := fanOut(ctx, regions, func(ctx context.Context, region string) (Result, error) {
		regionOpts := append([]option.ClientOption{option.WithEndpoint(dataprocEndpoint(region))}, opts...)

		client, err := dataproc.NewJobControllerClient(ctx, regionOpts...)
		if err != nil {
			return Result{}, fmt.Errorf("client: %w", err)
		}
		defer client.Close()

		it := client.ListJobs(ctx, &dataprocpb.ListJobsRequest{
			ProjectId: p.ProjectID,
			Region:    region,
			// Every state, not just the running ones: a job that failed twenty
			// minutes ago is usually why someone opened this table.
			JobStateMatcher: dataprocpb.ListJobsRequest_ALL,
		})

		var out Result
		for {
			if len(out.Resources) >= maxDataprocJobsPerRegion {
				// The API documents no ordering, so this is honestly the first
				// N it returned rather than the newest N. Saying which would be
				// a promise the API does not make.
				out.Warnings = append(out.Warnings, Warning{
					Scope:  region,
					Reason: ReasonCapped,
					Detail: fmt.Sprintf(
						"stopped after %d jobs — the region has more history than this table shows",
						maxDataprocJobsPerRegion),
				})
				break
			}

			job, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return out, err
			}
			out.Resources = append(out.Resources, dataprocJobResource(p, region, job))
		}
		return out, nil
	})

	if !cfg.HasDataprocRegions(p) {
		result.Warnings = append(result.Warnings, narrowedWarning(
			"only the global region was swept — set projects[].regions or defaults.regions to cover regional jobs"))
	}

	sortDataprocJobsByRecency(result.Resources)
	return result, nil
}

func dataprocJobResource(p config.Project, region string, j *dataprocpb.Job) Resource {
	id := j.GetReference().GetJobId()
	if id == "" {
		// A job always has a uuid even when the caller left the id to the
		// server, and an unidentifiable row is worse than an ugly one.
		id = j.GetJobUuid()
	}

	cluster := j.GetPlacement().GetClusterName()
	if cluster == "" {
		cluster = "-"
	}

	state := j.GetStatus().GetState().String()

	return Resource{
		Name:     id,
		Location: region,
		Status:   state,
		Row: []string{
			id,
			region,
			dataprocJobType(j),
			cluster,
			state,
			dataprocJobAge(j),
		},
		Raw: j,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/dataproc/jobs/%s?region=%s&project=%s",
			url.PathEscape(id), url.QueryEscape(region), url.QueryEscape(p.ProjectID)),
	}
}

// dataprocJobType names the job's payload. The API models it as a oneof, so
// the type of the field is the only place the answer lives.
func dataprocJobType(j *dataprocpb.Job) string {
	switch j.GetTypeJob().(type) {
	case *dataprocpb.Job_HadoopJob:
		return "HADOOP"
	case *dataprocpb.Job_SparkJob:
		return "SPARK"
	case *dataprocpb.Job_PysparkJob:
		return "PYSPARK"
	case *dataprocpb.Job_HiveJob:
		return "HIVE"
	case *dataprocpb.Job_PigJob:
		return "PIG"
	case *dataprocpb.Job_SparkRJob:
		return "SPARKR"
	case *dataprocpb.Job_SparkSqlJob:
		return "SPARKSQL"
	case *dataprocpb.Job_PrestoJob:
		return "PRESTO"
	case *dataprocpb.Job_TrinoJob:
		return "TRINO"
	case *dataprocpb.Job_FlinkJob:
		return "FLINK"
	default:
		return "-"
	}
}

// dataprocJobSubmitMillis is when the job was submitted, in epoch
// milliseconds, or 0 when the API said nothing.
//
// Status.StateStartTime is when the *current* state began, so on a finished job
// it reads as the moment it finished. The first entry in StatusHistory is the
// submission, which is the age an operator means.
func dataprocJobSubmitMillis(j *dataprocpb.Job) int64 {
	if history := j.GetStatusHistory(); len(history) > 0 {
		if ts := history[0].GetStateStartTime(); ts != nil {
			return ts.AsTime().UnixMilli()
		}
	}
	if ts := j.GetStatus().GetStateStartTime(); ts != nil {
		return ts.AsTime().UnixMilli()
	}
	return 0
}

func dataprocJobAge(j *dataprocpb.Job) string {
	millis := dataprocJobSubmitMillis(j)
	if millis == 0 {
		return "-"
	}
	return shortDuration(timeSince(millisToTime(millis)))
}

// sortDataprocJobsByRecency puts the newest job first, the same order the
// BigQuery jobs table uses and the order "what is running" is asked in. Ties
// break on the id so the table does not reshuffle between refreshes.
func sortDataprocJobsByRecency(resources []Resource) {
	sort.SliceStable(resources, func(i, j int) bool {
		si, sj := dataprocJobMillis(resources[i]), dataprocJobMillis(resources[j])
		if si != sj {
			return si > sj
		}
		return resources[i].Name < resources[j].Name
	})
}

func dataprocJobMillis(r Resource) int64 {
	j, ok := r.Raw.(*dataprocpb.Job)
	if !ok {
		return 0
	}
	return dataprocJobSubmitMillis(j)
}
