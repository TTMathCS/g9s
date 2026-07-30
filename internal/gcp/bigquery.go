package gcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	bigquery "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// BigQueryDatasetLister lists BigQuery datasets.
//
// Global and paginated, through the generated REST client — like Cloud SQL and
// Cloud DNS, so it costs no new dependency.
//
// Deliberately one call: the list response carries the name, location, type and
// labels, and nothing else. Table counts, sizes and creation times need a Get
// per dataset, which is one round trip per row on a screen whose whole job is
// to load fast. The dataset's own Console page is one `o` away when the detail
// matters.
type BigQueryDatasetLister struct{}

func (BigQueryDatasetLister) Kind() Kind {
	return Kind{
		ID:    "bq",
		Title: "BigQuery Datasets",
		Columns: []Column{
			{Title: "NAME", Width: 6},
			{Title: "LOCATION", Width: 3},
			{Title: "TYPE", Width: 2},
			{Title: "LABELS", Width: 4},
		},
	}
}

func (BigQueryDatasetLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := bigquery.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("bigquery client: %w", err)
	}

	var result Result
	err = svc.Datasets.List(p.ProjectID).Pages(ctx, func(page *bigquery.DatasetList) error {
		for _, d := range page.Datasets {
			result.Resources = append(result.Resources, datasetResource(p, d))
		}
		return nil
	})
	if err != nil {
		return result, err
	}

	sortResources(result.Resources)
	return result, nil
}

func datasetResource(p config.Project, d *bigquery.DatasetListDatasets) Resource {
	name := ""
	if d.DatasetReference != nil {
		name = d.DatasetReference.DatasetId
	}
	if name == "" {
		// Nothing else identifies the row, and an empty NAME cell is worse than
		// the opaque fully-qualified id.
		name = afterLast(d.Id, ":")
	}

	// A dataset's location is the one thing about it that cannot be changed
	// later, which is what makes it worth a column of its own.
	location := d.Location
	if location == "" {
		location = "-"
	}

	// DEFAULT is the ordinary case and says nothing. LINKED and EXTERNAL mean
	// the data is not really here, which changes what every other answer about
	// it means.
	datasetType := d.Type
	if datasetType == "" {
		datasetType = "DEFAULT"
	}

	return Resource{
		Name:     name,
		Location: location,
		// A dataset has no lifecycle state — existing is the healthy state.
		// See bucketResource for why a synthetic status beats an empty one.
		Status: "ACTIVE",
		Row: []string{
			name,
			location,
			datasetType,
			formatLabels(d.Labels),
		},
		Raw: d,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/bigquery?project=%s&p=%s&d=%s&page=dataset",
			url.QueryEscape(p.ProjectID), url.QueryEscape(p.ProjectID), url.QueryEscape(name)),
	}
}

// BigQueryJobLister lists recent BigQuery jobs.
//
// This is the "what is running" table, and the BigQuery question a dashboard
// can answer that a list of datasets cannot. Jobs are global to the project —
// each carries its own location — so one paginated call covers everything, with
// no fan-out.
//
// Two bounds, because "all jobs" means six months of history and a busy project
// runs thousands a day. The window comes from defaults.bigquery_job_window; the
// hard cap is maxJobs. Hitting the cap is reported as a warning rather than
// quietly handing back a list that looks complete.
type BigQueryJobLister struct{}

// maxJobs bounds one refresh. A table nobody can scroll to the end of is no
// more useful than one that says where it stopped.
const maxJobs = 500

// jobPageSize is what the API is asked for per round trip. It honours up to
// 1000, so asking for the whole cap keeps a quiet project to a single request.
const jobPageSize = 500

func (BigQueryJobLister) Kind() Kind {
	return Kind{
		ID:    "bqjobs",
		Title: "BigQuery Jobs",
		Columns: []Column{
			{Title: "JOB ID", Width: 6},
			{Title: "LOCATION", Width: 2},
			{Title: "TYPE", Width: 2},
			{Title: "USER", Width: 4},
			{Title: "STATE", Width: 2},
			{Title: "DURATION", Width: 2},
			{Title: "PROCESSED", Width: 2},
		},
	}
}

func (BigQueryJobLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := bigquery.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("bigquery client: %w", err)
	}

	since := time.Now().Add(-cfg.BigQueryJobWindow())
	// The API takes milliseconds since the epoch as an unsigned value, so a
	// window reaching back past 1970 would wrap into the far future and return
	// nothing at all. Jobs are only kept six months; anything that long is
	// already asking for everything.
	minCreation := since.UnixMilli()
	if minCreation < 0 {
		minCreation = 0
	}

	call := svc.Jobs.List(p.ProjectID).
		// Everyone's jobs, not just the caller's: asking what is running means
		// what the project is running.
		AllUsers(true).
		// Statistics — bytes and timings — are absent from the minimal
		// projection, and they are most of the table.
		Projection("full").
		MaxResults(jobPageSize).
		MinCreationTime(uint64(minCreation))

	var (
		result Result
		capped bool
	)

	err = call.Pages(ctx, func(page *bigquery.JobList) error {
		for _, j := range page.Jobs {
			if len(result.Resources) >= maxJobs {
				capped = true
				return errStopPaging
			}
			result.Resources = append(result.Resources, jobResource(p, j))
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopPaging) {
		return result, err
	}

	if capped {
		// Naming the setting rather than restating the window: shortDuration
		// renders the 24h default as "1d", which reads like a typo in a
		// sentence, and the setting is what the reader has to change anyway.
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"only the %d most recent jobs are shown — narrow defaults.bigquery_job_window for a complete list",
			maxJobs))
	}

	sortJobsByRecency(result.Resources)
	return result, nil
}

// errStopPaging ends a Pages walk early. Returning an error is the only way to
// stop one, so this sentinel is unwrapped again at the call site rather than
// being reported as a failed listing.
var errStopPaging = errors.New("stop paging")

func jobResource(p config.Project, j *bigquery.JobListJobs) Resource {
	id := ""
	location := ""
	if j.JobReference != nil {
		id, location = j.JobReference.JobId, j.JobReference.Location
	}
	if id == "" {
		// "project:location.jobid" — take the job id off the end of both.
		id = afterLast(afterLast(j.Id, ":"), ".")
	}
	if location == "" {
		location = "-"
	}

	jobType := "-"
	if j.Configuration != nil && j.Configuration.JobType != "" {
		jobType = j.Configuration.JobType
	}

	user := j.UserEmail
	if user == "" {
		user = "-"
	}

	state := jobState(j)

	return Resource{
		Name:     id,
		Location: location,
		Status:   state,
		Row: []string{
			id,
			location,
			jobType,
			user,
			state,
			jobDuration(j),
			humanBytes(jobBytesProcessed(j)),
		},
		Raw: j,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/bigquery?project=%s&j=bq:%s:%s&page=queryresults",
			url.QueryEscape(p.ProjectID), url.QueryEscape(location), url.QueryEscape(id)),
	}
}

// jobState folds the API's state and its error result into the one word an
// operator is scanning for.
//
// A finished job is DONE whether it worked or not — the failure lives in a
// separate field — so a table showing the raw state colours a failed job green
// and buries the only row worth reacting to.
func jobState(j *bigquery.JobListJobs) string {
	if j.Status != nil {
		if j.Status.ErrorResult != nil {
			return "FAILED"
		}
		if j.Status.State != "" {
			return j.Status.State
		}
	}
	if j.State != "" {
		return j.State
	}
	return "UNKNOWN"
}

// jobDuration is how long the job ran, or has been running so far.
//
// The running case is the point: a query that has been going for forty minutes
// is the row you are looking for, and it has no end time to subtract from.
func jobDuration(j *bigquery.JobListJobs) string {
	if j.Statistics == nil || j.Statistics.StartTime == 0 {
		return "-"
	}

	start := time.UnixMilli(j.Statistics.StartTime)
	end := time.Now()
	if j.Statistics.EndTime > 0 {
		end = time.UnixMilli(j.Statistics.EndTime)
	}

	d := end.Sub(start)
	if d < 0 {
		return "-"
	}
	return shortDuration(d)
}

// jobBytesProcessed pulls the volume out of whichever statistics block this job
// type filled in, and reports -1 when there is none — which is not the same
// answer as zero bytes.
func jobBytesProcessed(j *bigquery.JobListJobs) int64 {
	stats := j.Statistics
	if stats == nil {
		return -1
	}
	switch {
	case stats.Query != nil:
		return stats.Query.TotalBytesProcessed
	case stats.Load != nil:
		return stats.Load.InputFileBytes
	case stats.Extract != nil:
		return stats.Extract.InputBytes
	case stats.Copy != nil:
		return stats.Copy.CopiedLogicalBytes
	default:
		return -1
	}
}

// sortJobsByRecency puts the newest job first, which is the order the question
// "what is running" is asked in. Ties break on the id so equal timestamps — and
// jobs with no statistics at all — keep a stable order.
func sortJobsByRecency(resources []Resource) {
	sort.SliceStable(resources, func(i, j int) bool {
		ci, cj := jobCreationMillis(resources[i]), jobCreationMillis(resources[j])
		if ci != cj {
			return ci > cj
		}
		return resources[i].Name < resources[j].Name
	})
}

func jobCreationMillis(r Resource) int64 {
	j, ok := r.Raw.(*bigquery.JobListJobs)
	if !ok || j.Statistics == nil {
		return 0
	}
	return j.Statistics.CreationTime
}

// afterLast returns what follows the last sep, or s when there is none.
//
// BigQuery qualifies its ids with punctuation rather than a path — a dataset is
// "project:dataset" and a job is "project:location.jobid" — so lastSegment,
// which splits on "/", hands back the whole string here.
func afterLast(s, sep string) string {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[i+len(sep):]
	}
	return s
}

// formatLabels renders a label map as a stable, compact k=v list.
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	return strings.Join(parts, " ")
}

// humanBytes renders a byte count the way an operator reads it. A negative
// count means the API reported none, which is a different answer from zero.
func humanBytes(n int64) string {
	if n < 0 {
		return "-"
	}
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}

	const unit = 1024
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTP"[exp])
}
