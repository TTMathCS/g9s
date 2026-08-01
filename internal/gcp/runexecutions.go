package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	"github.com/TTMathCS/g9s/internal/config"
)

// CloudRunExecutionLister is the execution history of one Cloud Run job.
//
// The jobs row leads with the *last* execution's result, which is the right
// answer to "is this broken now" and no answer at all to "how long has it been
// broken". One failure is an incident; the same failure every night for a week
// is a different conversation, and only the history tells them apart.
//
// Newest first, because that is the order the question is asked in.
type CloudRunExecutionLister struct{}

func (CloudRunExecutionLister) ParentKind() string { return "runjobs" }

func (CloudRunExecutionLister) Kind() Kind {
	return Kind{
		ID:    "runexecs",
		Title: "Executions",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "TASKS", Width: 2},
			{Title: "RETRIES", Width: 1},
			{Title: "STARTED", Width: 2},
			{Title: "DURATION", Width: 2},
			{Title: "RESULT", Width: 2},
		},
	}
}

func (CloudRunExecutionLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	job, ok := parent.Raw.(*run.GoogleCloudRunV2Job)
	if !ok {
		return Result{}, fmt.Errorf("no job data for %s", parent.Name)
	}

	svc, err := run.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("cloud run client: %w", err)
	}

	var result Result
	err = svc.Projects.Locations.Jobs.Executions.List(job.Name).
		Pages(ctx, func(page *run.GoogleCloudRunV2ListExecutionsResponse) error {
			for _, e := range page.Executions {
				if e != nil {
					result.Resources = append(result.Resources, executionResource(p, parent, e))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	sortExecutionsByRecency(result.Resources)
	return result, nil
}

func executionResource(p config.Project, parent Resource, e *run.GoogleCloudRunV2Execution) Resource {
	name := lastSegment(e.Name)

	return Resource{
		Name:     name,
		Location: parent.Location,
		Status:   executionResult(e),
		Row: []string{
			name,
			taskTally(e),
			fmt.Sprintf("%d", e.RetriedCount),
			age(e.StartTime),
			executionDuration(e),
			executionResult(e),
		},
		Raw: e,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/run/jobs/executions/details/%s/%s/tasks?project=%s",
			url.PathEscape(parent.Location), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// taskTally is "succeeded/total", with failures called out separately.
//
// A parallel job that finished with 199 of 200 tasks succeeded reports itself
// as failed, and the tally is the only thing that says whether that was one bad
// shard or the whole run falling over.
func taskTally(e *run.GoogleCloudRunV2Execution) string {
	tally := fmt.Sprintf("%d/%d", e.SucceededCount, e.TaskCount)
	if e.FailedCount > 0 {
		tally += fmt.Sprintf(" (%d failed)", e.FailedCount)
	}
	return tally
}

// executionResult reads the Completed condition, falling back to the counts.
//
// An execution carries a list of conditions rather than one terminal condition,
// and a run still in flight has no completion at all — which is a different
// answer from having finished with no result reported.
func executionResult(e *run.GoogleCloudRunV2Execution) string {
	for _, c := range e.Conditions {
		if c == nil || c.Type != "Completed" {
			continue
		}
		// Not conditionState: that translates CONDITION_SUCCEEDED to READY,
		// which is the right word for a service's Ready condition and the wrong
		// one for a finished run. An execution succeeded or it did not, and the
		// jobs table one level up already says SUCCEEDED/FAILED — the two must
		// agree or the same run reads differently from either table.
		switch c.State {
		case "CONDITION_SUCCEEDED":
			return "SUCCEEDED"
		case "CONDITION_FAILED":
			return "FAILED"
		case "CONDITION_RECONCILING", "CONDITION_PENDING":
			return "RUNNING"
		case "":
			return "UNKNOWN"
		default:
			return strings.TrimPrefix(c.State, "CONDITION_")
		}
	}

	if e.CompletionTime == "" {
		return "RUNNING"
	}
	// Finished, but the condition is missing. The counts still answer it.
	if e.FailedCount > 0 || e.CancelledCount > 0 {
		return "FAILED"
	}
	return "SUCCEEDED"
}

// executionDuration is how long the run took, or how long it has been going.
func executionDuration(e *run.GoogleCloudRunV2Execution) string {
	start, err := time.Parse(time.RFC3339, e.StartTime)
	if err != nil {
		return "-"
	}

	end := time.Now()
	if e.CompletionTime != "" {
		finished, err := time.Parse(time.RFC3339, e.CompletionTime)
		if err != nil {
			return "-"
		}
		end = finished
	}

	if d := end.Sub(start); d > 0 {
		return shortDuration(d)
	}
	// Sub-second, or clocks disagreeing across the round trip. Zero is a
	// clearer answer than a negative one.
	return "0s"
}

// sortExecutionsByRecency puts the newest run first, the order the history is
// read in. Ties break on the name so the table does not reshuffle.
func sortExecutionsByRecency(resources []Resource) {
	stableSortBy(resources, func(a, b Resource) bool {
		ca, cb := executionCreateTime(a), executionCreateTime(b)
		if ca != cb {
			return ca > cb
		}
		return a.Name < b.Name
	})
}

func executionCreateTime(r Resource) string {
	e, ok := r.Raw.(*run.GoogleCloudRunV2Execution)
	if !ok {
		return ""
	}
	if e.CreateTime != "" {
		return e.CreateTime
	}
	return e.StartTime
}
