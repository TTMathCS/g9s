package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	cloudscheduler "google.golang.org/api/cloudscheduler/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// SchedulerJobLister lists Cloud Scheduler jobs.
//
// Regional: the parent is a concrete location, so this fans out the way Cloud
// Run does. Scheduler locations are a shorter list than Compute's, and a region
// with the API unused answers NOT_FOUND, which describeFailure already drops.
//
// The row leads with what happened last, not with what is configured. A cron
// entry is not interesting because it exists; it is interesting when it stopped
// working, and there are two ways for that to happen that a config-only table
// cannot tell apart:
//
//   - PAUSED — the job is fine and simply is not running. Someone paused it for
//     a deploy in March and nothing reminded them. Nothing errors, nothing
//     alerts, the work just quietly stops.
//   - a failing attempt — it runs on schedule and the target rejects it every
//     time. `Status` carries the last error, so the row can say so.
//
// Both look identical to a table showing name, schedule and target, which is
// why LAST and RESULT are here and the description is not.
type SchedulerJobLister struct{}

func (SchedulerJobLister) Kind() Kind {
	return Kind{
		ID:    "scheduler",
		Title: "Scheduler Jobs",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "REGION", Width: 2},
			{Title: "SCHEDULE", Width: 3},
			{Title: "TARGET", Width: 4},
			{Title: "LAST", Width: 2},
			{Title: "RESULT", Width: 3},
			{Title: "STATE", Width: 2},
		},
	}
}

func (SchedulerJobLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	regions := cfg.Regions(p)
	if len(regions) == 0 {
		return Result{Warnings: []Warning{narrowedWarning("no regions configured — set projects[].regions or defaults.regions")}}, nil
	}

	svc, err := cloudscheduler.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("cloud scheduler client: %w", err)
	}

	return fanOut(ctx, regions, func(ctx context.Context, region string) (Result, error) {
		parent := fmt.Sprintf("projects/%s/locations/%s", p.ProjectID, region)

		var out Result
		err := svc.Projects.Locations.Jobs.List(parent).
			Pages(ctx, func(page *cloudscheduler.ListJobsResponse) error {
				for _, j := range page.Jobs {
					if j != nil {
						out.Resources = append(out.Resources, schedulerJobResource(p, region, j))
					}
				}
				return nil
			})
		return out, err
	}), nil
}

func schedulerJobResource(p config.Project, region string, j *cloudscheduler.Job) Resource {
	name := lastSegment(j.Name)

	state := j.State
	if state == "" || state == "STATE_UNSPECIFIED" {
		state = "UNKNOWN"
	}

	schedule := j.Schedule
	if schedule == "" {
		schedule = "-"
	}

	return Resource{
		Name:     name,
		Location: region,
		Status:   schedulerStatus(j, state),
		Row: []string{
			name,
			region,
			schedule,
			schedulerTarget(j),
			schedulerLastRun(j),
			schedulerResult(j),
			state,
		},
		Raw: j,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/cloudscheduler/jobs/edit/%s/%s?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// schedulerStatus decides the row's colour.
//
// A paused job keeps PAUSED, because that is the finding. Otherwise a failing
// last attempt outranks the job's own state: ENABLED is true of a job that has
// errored every night for a week, and it is not what anyone needs to see.
func schedulerStatus(j *cloudscheduler.Job, state string) string {
	if state == "PAUSED" {
		return "PAUSED"
	}
	if j.Status != nil && j.Status.Code != 0 {
		return "LAST_RUN_FAILED"
	}
	return state
}

// schedulerTarget names what the job actually invokes.
//
// Exactly one of the three target fields is set. Which one it is changes what
// "failing" means — an HTTP target failing is the endpoint's problem, a Pub/Sub
// target failing is a permissions problem — so the type leads the cell.
func schedulerTarget(j *cloudscheduler.Job) string {
	switch {
	case j.HttpTarget != nil:
		method := j.HttpTarget.HttpMethod
		if method == "" {
			method = "POST"
		}
		return method + " " + clipTarget(j.HttpTarget.Uri)
	case j.PubsubTarget != nil:
		return "pubsub: " + lastSegment(j.PubsubTarget.TopicName)
	case j.AppEngineHttpTarget != nil:
		relative := j.AppEngineHttpTarget.RelativeUri
		if relative == "" {
			relative = "/"
		}
		return "appengine " + relative
	default:
		return "-"
	}
}

// clipTarget keeps a URL readable in a cell without hiding which host it hits.
func clipTarget(uri string) string {
	if uri == "" {
		return "-"
	}
	return clip(uri, 60)
}

// schedulerLastRun is how long ago the job last fired.
//
// A job whose schedule says hourly and whose last attempt was nine days ago is
// broken in a way neither the schedule nor the state column reports.
func schedulerLastRun(j *cloudscheduler.Job) string {
	if j.LastAttemptTime == "" {
		return "never"
	}
	return age(j.LastAttemptTime)
}

// schedulerResult reports how the last attempt ended.
//
// Status is the gRPC status of that attempt and is left unset on success, so an
// empty Status on a job that has run is the good case — but only on a job that
// has run. One that has never fired reports neither, and saying "ok" there
// would invent a success.
func schedulerResult(j *cloudscheduler.Job) string {
	if j.Status == nil || j.Status.Code == 0 {
		if j.LastAttemptTime == "" {
			return "-"
		}
		return "ok"
	}
	if msg := strings.TrimSpace(j.Status.Message); msg != "" {
		return clip(msg, 60)
	}
	return fmt.Sprintf("code %d", j.Status.Code)
}
