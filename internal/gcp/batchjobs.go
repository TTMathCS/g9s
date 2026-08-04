package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	batch "google.golang.org/api/batch/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// BatchJobLister lists Cloud Batch jobs.
//
// Batch is where the long, expensive, occasional work runs — the render, the
// simulation, the backfill — and its failures are quiet in a way an online
// service's are not. Nothing pages when a batch job fails at 3am; it simply is
// not finished in the morning, and the first sign is whatever depended on its
// output being missing.
//
// The row leads with state and how long ago, because that is the shape of the
// question: not "what jobs exist" but "did the thing I expected finish, and if
// not, when did it stop". Task counts are here because a job reporting RUNNING
// with every task failed and retrying is not making progress, and its state
// alone will not say so.
type BatchJobLister struct{}

func (BatchJobLister) Kind() Kind {
	return Kind{
		ID:    "batch",
		Title: "Batch Jobs",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "REGION", Width: 2},
			{Title: "TASKS", Width: 2},
			{Title: "CREATED", Width: 2},
			{Title: "STATE", Width: 2},
		},
	}
}

func (BatchJobLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	regions := cfg.Regions(p)
	if len(regions) == 0 {
		return Result{Warnings: []Warning{narrowedWarning("no regions configured — set projects[].regions or defaults.regions")}}, nil
	}

	svc, err := batch.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("batch client: %w", err)
	}

	return fanOut(ctx, regions, func(ctx context.Context, region string) (Result, error) {
		parent := fmt.Sprintf("projects/%s/locations/%s", p.ProjectID, region)

		var out Result
		err := svc.Projects.Locations.Jobs.List(parent).
			Pages(ctx, func(page *batch.ListJobsResponse) error {
				for _, j := range page.Jobs {
					if j != nil {
						out.Resources = append(out.Resources, batchJobResource(p, region, j))
					}
				}
				return nil
			})
		return out, err
	}), nil
}

func batchJobResource(p config.Project, region string, j *batch.Job) Resource {
	name := lastSegment(j.Name)

	state := "UNKNOWN"
	if j.Status != nil && j.Status.State != "" {
		state = j.Status.State
	}

	return Resource{
		Name:     name,
		Location: region,
		Status:   state,
		Row: []string{
			name,
			region,
			batchTaskCounts(j),
			batchCreated(j),
			state,
		},
		Raw: j,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/batch/jobsDetail/regions/%s/jobs/%s?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// batchTaskCounts summarises where the work actually is.
//
// A job's own state is the roll-up and hides the case worth catching: RUNNING
// with a growing failed count is a job burning quota on work that will not
// succeed, and it reads identically to a healthy one until the counts are
// visible. Only the interesting groups are shown — listing every state with a
// zero beside it would bury the one that is not zero.
func batchTaskCounts(j *batch.Job) string {
	if j.Status == nil || len(j.Status.TaskGroups) == 0 {
		return "-"
	}

	// Counts arrive as strings: the API encodes int64 as JSON strings to
	// survive languages whose numbers do not reach that far. A count that will
	// not parse is skipped rather than shown as zero, since "no failed tasks"
	// is exactly the claim this cell must not make by accident.
	counts := map[string]int64{}
	for _, group := range j.Status.TaskGroups {
		for state, raw := range group.Counts {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				continue
			}
			counts[state] += n
		}
	}
	if len(counts) == 0 {
		return "-"
	}

	var parts []string
	// Fixed order rather than map order, so the same job does not reshuffle
	// its own cell between refreshes.
	for _, state := range []string{"FAILED", "RUNNING", "PENDING", "SUCCEEDED"} {
		if n := counts[state]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, strings.ToLower(state)))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func batchCreated(j *batch.Job) string {
	t, err := time.Parse(time.RFC3339, j.CreateTime)
	if err != nil {
		return "-"
	}
	return shortDuration(time.Since(t))
}
