package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	"github.com/TTMathCS/g9s/internal/config"
)

// CloudRunServiceLister lists Cloud Run services.
//
// Regional, and unlike GKE there is no wildcard: the v2 API documents that the
// parent location "cannot be the '-' wildcard", so this fans out over the
// configured regions the way Dataproc and Composer do. A project with no
// regions configured is told so rather than shown an empty table.
type CloudRunServiceLister struct{}

func (CloudRunServiceLister) Kind() Kind {
	return Kind{
		ID:    "run",
		Title: "Cloud Run Services",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "REGION", Width: 2},
			{Title: "URL", Width: 5},
			{Title: "INGRESS", Width: 3},
			{Title: "LATEST READY", Width: 4},
			{Title: "STATE", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (CloudRunServiceLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	regions := cfg.Regions(p)
	if len(regions) == 0 {
		return Result{Warnings: []string{
			"no regions configured — set projects[].regions or defaults.regions",
		}}, nil
	}

	svc, err := run.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("cloud run client: %w", err)
	}

	return fanOut(ctx, regions, func(ctx context.Context, region string) (Result, error) {
		parent := fmt.Sprintf("projects/%s/locations/%s", p.ProjectID, region)

		var out Result
		err := svc.Projects.Locations.Services.List(parent).Pages(ctx, func(page *run.GoogleCloudRunV2ListServicesResponse) error {
			for _, s := range page.Services {
				out.Resources = append(out.Resources, runServiceResource(p, region, s))
			}
			return nil
		})
		return out, err
	}), nil
}

func runServiceResource(p config.Project, region string, s *run.GoogleCloudRunV2Service) Resource {
	name := lastSegment(s.Name)

	uri := s.Uri
	if uri == "" {
		// A service with no URL is one that has never successfully deployed, or
		// has its default URL disabled on purpose.
		uri = "-"
	}

	ingress := strings.TrimPrefix(s.Ingress, "INGRESS_TRAFFIC_")
	if ingress == "" {
		ingress = "-"
	}

	ready := lastSegment(s.LatestReadyRevision)
	if ready == "" {
		ready = "-"
	}

	return Resource{
		Name:     name,
		Location: region,
		Status:   conditionState(s.TerminalCondition),
		Row: []string{
			name,
			region,
			uri,
			ingress,
			ready,
			conditionState(s.TerminalCondition),
			age(s.CreateTime),
		},
		Raw: s,
		// The service URL is what an operator wants from `o`, and it is a
		// plain https endpoint, so ConsoleURL stays the Console page and the
		// URL column carries the other one.
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/run/detail/%s/%s/metrics?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// CloudRunJobLister lists Cloud Run jobs.
//
// The other half of Cloud Run, and the one a service list cannot answer for:
// a job that exists says nothing about whether its last execution worked.
type CloudRunJobLister struct{}

func (CloudRunJobLister) Kind() Kind {
	return Kind{
		ID:    "runjobs",
		Title: "Cloud Run Jobs",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "REGION", Width: 2},
			{Title: "EXECUTIONS", Width: 2},
			{Title: "LAST EXECUTION", Width: 4},
			{Title: "LAST RESULT", Width: 3},
			{Title: "STATE", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (CloudRunJobLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	regions := cfg.Regions(p)
	if len(regions) == 0 {
		return Result{Warnings: []string{
			"no regions configured — set projects[].regions or defaults.regions",
		}}, nil
	}

	svc, err := run.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("cloud run client: %w", err)
	}

	return fanOut(ctx, regions, func(ctx context.Context, region string) (Result, error) {
		parent := fmt.Sprintf("projects/%s/locations/%s", p.ProjectID, region)

		var out Result
		err := svc.Projects.Locations.Jobs.List(parent).Pages(ctx, func(page *run.GoogleCloudRunV2ListJobsResponse) error {
			for _, j := range page.Jobs {
				out.Resources = append(out.Resources, runJobResource(p, region, j))
			}
			return nil
		})
		return out, err
	}), nil
}

func runJobResource(p config.Project, region string, j *run.GoogleCloudRunV2Job) Resource {
	name := lastSegment(j.Name)

	lastRun, lastResult := "-", "-"
	if exec := j.LatestCreatedExecution; exec != nil {
		lastRun = lastSegment(exec.Name)
		// CompletionStatus is the answer to "did the nightly job work",
		// which the job's own state does not carry: a job whose executions
		// all fail is still a perfectly healthy job resource.
		if exec.CompletionStatus != "" {
			lastResult = strings.TrimPrefix(exec.CompletionStatus, "EXECUTION_")
		} else if exec.CompletionTime == "" {
			lastResult = "RUNNING"
		}
	}

	return Resource{
		Name:     name,
		Location: region,
		// The execution result leads, because a job that cannot run is what
		// the table is for; the job's own condition is the fallback.
		Status: firstNonEmptyString(lastResult, conditionState(j.TerminalCondition)),
		Row: []string{
			name,
			region,
			fmt.Sprintf("%d", j.ExecutionCount),
			lastRun,
			lastResult,
			conditionState(j.TerminalCondition),
			age(j.CreateTime),
		},
		Raw: j,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/run/jobs/details/%s/%s/executions?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// conditionState reads a Cloud Run terminal condition as one word.
//
// The API models readiness as a condition with a state rather than a status
// field, and CONDITION_SUCCEEDED is its way of saying Ready — which reads as
// neither healthy nor unhealthy in a table, hence the translation.
func conditionState(c *run.GoogleCloudRunV2Condition) string {
	if c == nil {
		return "UNKNOWN"
	}
	switch c.State {
	case "CONDITION_SUCCEEDED":
		return "READY"
	case "CONDITION_FAILED":
		return "FAILED"
	case "CONDITION_RECONCILING":
		return "RECONCILING"
	case "CONDITION_PENDING":
		return "PENDING"
	case "":
		return "UNKNOWN"
	default:
		return strings.TrimPrefix(c.State, "CONDITION_")
	}
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" && v != "-" {
			return v
		}
	}
	return "UNKNOWN"
}
