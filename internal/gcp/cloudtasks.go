package gcp

import (
	"context"
	"fmt"
	"net/url"

	cloudtasks "google.golang.org/api/cloudtasks/v2"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// CloudTaskQueueLister lists Cloud Tasks queues.
//
// The finding this exists for is a PAUSED queue. Pausing one is a normal
// incident response — stop the workers hammering a failing backend — and it is
// also the single easiest thing to forget to undo. A paused queue does not
// error, does not alert, and does not lose the tasks: it accepts them and
// holds them, so the symptom appears somewhere else entirely, hours later, as
// work that never happened.
//
// DISABLED is worse and rarer: the queue rejects new tasks outright, which
// surfaces as errors in whatever was enqueueing them.
//
// The rate limits are here because they are the other way a queue silently
// stops keeping up. A queue dispatching at one per second against a backlog
// that grows faster is functionally stopped, and nothing about its state says
// so.
type CloudTaskQueueLister struct{}

func (CloudTaskQueueLister) Kind() Kind {
	return Kind{
		ID:    "tasks",
		Title: "Task Queues",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "REGION", Width: 2},
			{Title: "TYPE", Width: 2},
			{Title: "DISPATCH/S", Width: 2},
			{Title: "CONCURRENT", Width: 2},
			{Title: "RETRIES", Width: 2},
			{Title: "STATE", Width: 2},
		},
	}
}

func (CloudTaskQueueLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	regions := cfg.Regions(p)
	if len(regions) == 0 {
		return Result{Warnings: []Warning{narrowedWarning("no regions configured — set projects[].regions or defaults.regions")}}, nil
	}

	svc, err := cloudtasks.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("cloud tasks client: %w", err)
	}

	return fanOut(ctx, regions, func(ctx context.Context, region string) (Result, error) {
		parent := fmt.Sprintf("projects/%s/locations/%s", p.ProjectID, region)

		var out Result
		err := svc.Projects.Locations.Queues.List(parent).
			Pages(ctx, func(page *cloudtasks.ListQueuesResponse) error {
				for _, q := range page.Queues {
					if q != nil {
						out.Resources = append(out.Resources, taskQueueResource(p, region, q))
					}
				}
				return nil
			})
		return out, err
	}), nil
}

func taskQueueResource(p config.Project, region string, q *cloudtasks.Queue) Resource {
	name := lastSegment(q.Name)

	state := q.State
	if state == "" || state == "STATE_UNSPECIFIED" {
		state = "UNKNOWN"
	}

	return Resource{
		Name:     name,
		Location: region,
		Status:   state,
		Row: []string{
			name,
			region,
			taskQueueType(q),
			taskDispatchRate(q),
			taskConcurrency(q),
			taskRetries(q),
			state,
		},
		Raw: q,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/cloudtasks/queue/%s/%s?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// taskQueueType distinguishes the two dispatch models, because they fail
// differently: an App Engine queue's target moves with the app's routing,
// while an HTTP queue names its own endpoint.
func taskQueueType(q *cloudtasks.Queue) string {
	if q.AppEngineRoutingOverride != nil {
		return "appengine"
	}
	return "http"
}

// taskDispatchRate is the ceiling on how fast the queue drains.
//
// Unset means Cloud Tasks' own default rather than zero, and printing "0"
// there would read as a stopped queue, which is a different and much more
// alarming fact.
func taskDispatchRate(q *cloudtasks.Queue) string {
	if q.RateLimits == nil || q.RateLimits.MaxDispatchesPerSecond == 0 {
		return "default"
	}
	return trimFloat(q.RateLimits.MaxDispatchesPerSecond)
}

func taskConcurrency(q *cloudtasks.Queue) string {
	if q.RateLimits == nil || q.RateLimits.MaxConcurrentDispatches == 0 {
		return "default"
	}
	return fmt.Sprint(q.RateLimits.MaxConcurrentDispatches)
}

// taskRetries reports the retry ceiling.
//
// -1 means unlimited in this API, and that is worth spelling out: a task that
// can never be given up on is how a queue full of permanently failing work
// stays full forever.
func taskRetries(q *cloudtasks.Queue) string {
	if q.RetryConfig == nil {
		return "default"
	}
	if q.RetryConfig.MaxAttempts < 0 {
		return "unlimited"
	}
	if q.RetryConfig.MaxAttempts == 0 {
		return "default"
	}
	return fmt.Sprint(q.RetryConfig.MaxAttempts)
}

// trimFloat renders a rate without the trailing zeros a float carries, so a
// column of them lines up and reads as numbers rather than as noise.
func trimFloat(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
