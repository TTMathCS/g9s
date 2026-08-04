package gcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	cloudbuild "google.golang.org/api/cloudbuild/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// CloudBuildLister lists recent Cloud Build builds.
//
// The question this table answers is "did the deploy run, and did it work",
// which is the first thing asked when something is missing in production and
// the second thing asked when something unexpected is there. A build list that
// showed only configuration would answer neither.
//
// Newest first and capped, because build history is unbounded and only the
// recent end of it is ever the reason someone opened this. Reaching the cap is
// reported like any other incompleteness rather than silently truncating —
// "the last 200 builds" and "every build" are different claims.
type CloudBuildLister struct{}

func (CloudBuildLister) Kind() Kind {
	return Kind{
		ID:    "builds",
		Title: "Cloud Build",
		Columns: []Column{
			{Title: "BUILD", Width: 3},
			{Title: "TRIGGER", Width: 3},
			{Title: "SOURCE", Width: 4},
			{Title: "DURATION", Width: 2},
			{Title: "FINISHED", Width: 2},
			{Title: "STATUS", Width: 2},
		},
	}
}

func (CloudBuildLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := cloudbuild.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("cloud build client: %w", err)
	}

	maxBuilds := cfg.LimitCloudBuilds()

	var (
		result Result
		capped bool
	)
	err = svc.Projects.Builds.List(p.ProjectID).
		Pages(ctx, func(page *cloudbuild.ListBuildsResponse) error {
			for _, b := range page.Builds {
				if b == nil {
					continue
				}
				if len(result.Resources) >= maxBuilds {
					capped = true
					// Stopping the page walk here rather than reading the rest
					// and discarding it: every further page is a request whose
					// result is thrown away.
					return errStopPaging
				}
				result.Resources = append(result.Resources, buildResource(p, b))
			}
			return nil
		})
	if err != nil && !errors.Is(err, errStopPaging) {
		return result, err
	}

	if capped {
		result.Warnings = append(result.Warnings, cappedWarning(
			"the %d most recent builds are shown — raise defaults.limits.cloud_builds for more history",
			maxBuilds))
	}
	// The API returns newest first; sortResources would reorder them into
	// something meaningless for a history.
	return result, nil
}

func buildResource(p config.Project, b *cloudbuild.Build) Resource {
	id := b.Id
	if len(id) > 8 {
		// The short form is what the console and logs show, and a full UUID
		// costs a third of the row for characters nobody reads.
		id = id[:8]
	}

	status := b.Status
	if status == "" || status == "STATUS_UNKNOWN" {
		status = "UNKNOWN"
	}

	return Resource{
		Name:     id,
		Location: buildLocation(b),
		Status:   status,
		Row: []string{
			id,
			buildTrigger(b),
			buildSource(b),
			buildDuration(b),
			buildFinished(b),
			status,
		},
		Raw: b,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/cloud-build/builds/%s?project=%s",
			url.PathEscape(b.Id), url.QueryEscape(p.ProjectID)),
	}
}

// buildTrigger names what started the build.
//
// A build with no trigger was started by hand, and that is worth seeing: an
// unexplained manual build in a repository that is otherwise entirely
// automated is a question, not noise.
func buildTrigger(b *cloudbuild.Build) string {
	if b.BuildTriggerId == "" {
		return "manual"
	}
	if name := b.Substitutions["TRIGGER_NAME"]; name != "" {
		return name
	}
	id := b.BuildTriggerId
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}

// buildSource is what was built, preferring the human-readable substitutions
// Cloud Build fills in for repository triggers over the raw source object.
func buildSource(b *cloudbuild.Build) string {
	repo := b.Substitutions["REPO_NAME"]
	ref := b.Substitutions["BRANCH_NAME"]
	if ref == "" {
		ref = b.Substitutions["TAG_NAME"]
	}
	switch {
	case repo != "" && ref != "":
		return repo + "@" + ref
	case repo != "":
		return repo
	case b.Source != nil && b.Source.StorageSource != nil:
		return "gs://" + b.Source.StorageSource.Bucket
	case b.Source != nil && b.Source.RepoSource != nil:
		return b.Source.RepoSource.RepoName
	default:
		return "-"
	}
}

// buildDuration is how long the build took, or how long it has been running.
//
// A build that is still WORKING after an hour is the shape of a hang, and it
// reads the same as a finished one unless the running case is measured from
// now rather than from a finish time it does not have.
func buildDuration(b *cloudbuild.Build) string {
	start, err := time.Parse(time.RFC3339, b.StartTime)
	if err != nil {
		return "-"
	}
	if b.FinishTime == "" {
		return shortDuration(time.Since(start)) + "…"
	}
	finish, err := time.Parse(time.RFC3339, b.FinishTime)
	if err != nil {
		return "-"
	}
	return shortDuration(finish.Sub(start))
}

func buildFinished(b *cloudbuild.Build) string {
	if b.FinishTime == "" {
		return "running"
	}
	return age(b.FinishTime)
}

// buildLocation reports the region a build ran in. Builds default to global,
// and a regional build pool is a deliberate choice worth showing.
func buildLocation(b *cloudbuild.Build) string {
	if loc := lastSegment(b.Name); loc != "" && strings.Contains(b.Name, "/locations/") {
		if region := segmentAfter(b.Name, "locations"); region != "" {
			return region
		}
	}
	return "global"
}
