package gcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	dataflow "google.golang.org/api/dataflow/v1b3"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// maxDataflowJobs caps the listing. Dataflow keeps job records for about thirty
// days, and a project that launches a pipeline per hour accumulates hundreds in
// that window — more history than a "what is running" table can show, and more
// pages than a refresh should spend.

// DataflowLister lists Dataflow jobs.
//
// Regional service, global call: projects.jobs.aggregated sweeps every regional
// endpoint server-side, the same shape as Compute's aggregatedList, so this
// costs one paginated call rather than one per configured region. That also
// makes it honest about regions outside the config — a job running somewhere
// nobody listed still shows up, which for a pipeline someone launched by hand is
// exactly the case worth catching.
//
// Endpoints that did not answer come back in FailedLocation, the same partial
// listing story as GKE's missing zones, and are reported as warnings.
type DataflowLister struct{}

func (DataflowLister) Kind() Kind {
	return Kind{
		ID:    "dataflow",
		Title: "Dataflow Jobs",
		Columns: []Column{
			{Title: "NAME", Width: 6},
			{Title: "REGION", Width: 2},
			{Title: "TYPE", Width: 2},
			{Title: "STATUS", Width: 2},
			{Title: "AGE", Width: 2},
			{Title: "UPDATED", Width: 2},
		},
	}
}

func (DataflowLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	maxDataflowJobs := cfg.LimitDataflowJobs()
	svc, err := dataflow.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("dataflow client: %w", err)
	}

	var (
		result Result
		capped bool
		failed = map[string]bool{}
	)

	// Every state, not just the running ones: a pipeline that failed twenty
	// minutes ago is usually why someone opened this table.
	call := svc.Projects.Jobs.Aggregated(p.ProjectID).Filter("ALL").PageSize(200)

	err = call.Pages(ctx, func(page *dataflow.ListJobsResponse) error {
		for _, loc := range page.FailedLocation {
			if loc != nil && loc.Name != "" {
				failed[loc.Name] = true
			}
		}
		for _, j := range page.Jobs {
			if len(result.Resources) >= maxDataflowJobs {
				capped = true
				return errStopPaging
			}
			result.Resources = append(result.Resources, dataflowJobResource(p, j))
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopPaging) {
		return result, err
	}

	for _, name := range sortedKeys(failed) {
		if w, ok := describeFailure(name, fmt.Errorf("regional endpoint did not respond")); ok {
			result.Warnings = append(result.Warnings, w)
		}
	}
	if capped {
		// The API documents no ordering for the ALL filter, so this is honestly
		// the first N it returned rather than the newest N. Claiming otherwise
		// would be a promise the API does not make.
		result.Warnings = append(result.Warnings, cappedWarning(
			"stopped after %d jobs — the project has more history than this table shows",
			maxDataflowJobs))
	}

	sortDataflowJobsByRecency(result.Resources)
	return result, nil
}

func dataflowJobResource(p config.Project, j *dataflow.Job) Resource {
	// The region is on the job rather than in the request, which is the whole
	// point of the aggregated call.
	region := j.Location
	if region == "" {
		region = "-"
	}

	status := strings.TrimPrefix(j.CurrentState, "JOB_STATE_")
	if status == "" {
		status = "UNKNOWN"
	}

	jobType := strings.TrimPrefix(j.Type, "JOB_TYPE_")
	if jobType == "" {
		jobType = "-"
	}

	return Resource{
		Name:     j.Name,
		Location: region,
		Status:   status,
		Row: []string{
			j.Name,
			region,
			jobType,
			status,
			age(j.CreateTime),
			// When the job last changed state. For a streaming job that is
			// usually when it started; for a batch job it is when it finished,
			// which is the number you want next to FAILED.
			age(j.CurrentStateTime),
		},
		Raw: j,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/dataflow/jobs/%s/%s?project=%s",
			url.PathEscape(region), url.PathEscape(j.Id), url.QueryEscape(p.ProjectID)),
	}
}

// sortDataflowJobsByRecency puts the newest job first, the order the BigQuery
// and Dataproc job tables use and the order "what is running" is asked in. Ties
// break on the name so the table does not reshuffle between refreshes.
func sortDataflowJobsByRecency(resources []Resource) {
	sort.SliceStable(resources, func(i, j int) bool {
		ci, cj := dataflowCreateTime(resources[i]), dataflowCreateTime(resources[j])
		if ci != cj {
			return ci > cj
		}
		return resources[i].Name < resources[j].Name
	})
}

func dataflowCreateTime(r Resource) string {
	j, ok := r.Raw.(*dataflow.Job)
	if !ok {
		return ""
	}
	// RFC3339 with a fixed offset sorts correctly as a string, and every
	// timestamp in one response comes from the same service in UTC.
	return j.CreateTime
}

// sortedKeys returns a map's keys in order, so a set of failed locations turns
// into deterministic warnings.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
