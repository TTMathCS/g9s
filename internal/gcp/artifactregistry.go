package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	artifactregistry "google.golang.org/api/artifactregistry/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// ArtifactRegistryLister lists Artifact Registry repositories.
//
// Regional, with a concrete location in the parent, so this fans out over the
// configured regions the way Cloud Run and Scheduler do. The v1 API does not
// document a `-` wildcard for location and returns no unreachable list to say
// which ones it skipped, so guessing one would produce a listing that is
// silently short rather than one that says which regions it could not reach.
//
// SIZE is why this kind is here. A registry is where images pile up: every CI
// build pushes one, nothing removes them by default, and the bill grows in a
// place nobody opens. The size is on the list response, so the column that
// answers "what is this costing" is free — and a repository with no cleanup
// policy is the row that made it grow.
type ArtifactRegistryLister struct{}

func (ArtifactRegistryLister) Kind() Kind {
	return Kind{
		ID:    "artifacts",
		Title: "Artifact Repositories",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "REGION", Width: 2},
			{Title: "FORMAT", Width: 2},
			{Title: "MODE", Width: 2},
			{Title: "SIZE", Width: 2},
			{Title: "CLEANUP", Width: 3},
			{Title: "UPDATED", Width: 2},
		},
	}
}

func (ArtifactRegistryLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	regions := cfg.Regions(p)
	if len(regions) == 0 {
		return Result{Warnings: []string{
			"no regions configured — set projects[].regions or defaults.regions",
		}}, nil
	}

	svc, err := artifactregistry.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("artifact registry client: %w", err)
	}

	return fanOut(ctx, regions, func(ctx context.Context, region string) (Result, error) {
		parent := fmt.Sprintf("projects/%s/locations/%s", p.ProjectID, region)

		var out Result
		err := svc.Projects.Locations.Repositories.List(parent).
			Pages(ctx, func(page *artifactregistry.ListRepositoriesResponse) error {
				for _, repo := range page.Repositories {
					if repo != nil {
						out.Resources = append(out.Resources, repositoryResource(p, region, repo))
					}
				}
				return nil
			})
		return out, err
	}), nil
}

func repositoryResource(p config.Project, region string, r *artifactregistry.Repository) Resource {
	name := lastSegment(r.Name)

	return Resource{
		Name:     name,
		Location: region,
		Status:   repositoryStatus(r),
		Row: []string{
			name,
			region,
			repositoryFormat(r),
			repositoryMode(r),
			humanBytes(r.SizeBytes),
			repositoryCleanup(r),
			age(r.UpdateTime),
		},
		Raw: r,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/artifacts/%s/%s/%s?project=%s",
			url.PathEscape(strings.ToLower(repositoryFormat(r))),
			url.PathEscape(p.ProjectID), url.PathEscape(region),
			url.QueryEscape(p.ProjectID)),
	}
}

// repositoryStatus leads with the cleanup finding.
//
// A repository with no cleanup policy grows forever, and it reports nothing at
// all to say so — there is no state field on the resource. NO_CLEANUP is the
// row worth colouring, and DRY_RUN is the trap underneath it: a policy that is
// configured, looks configured, and deletes nothing.
func repositoryStatus(r *artifactregistry.Repository) string {
	if len(r.CleanupPolicies) == 0 {
		return "NO_CLEANUP"
	}
	if r.CleanupPolicyDryRun {
		return "CLEANUP_DRY_RUN"
	}
	return "ACTIVE"
}

// repositoryCleanup says what the cleanup policies actually do.
func repositoryCleanup(r *artifactregistry.Repository) string {
	if len(r.CleanupPolicies) == 0 {
		return "none — grows forever"
	}
	if r.CleanupPolicyDryRun {
		return fmt.Sprintf("%d, dry run only", len(r.CleanupPolicies))
	}
	return fmt.Sprintf("%d active", len(r.CleanupPolicies))
}

func repositoryFormat(r *artifactregistry.Repository) string {
	if r.Format == "" || r.Format == "FORMAT_UNSPECIFIED" {
		return "-"
	}
	return strings.ToLower(r.Format)
}

// repositoryMode distinguishes a repository that holds artifacts from one that
// proxies or aggregates others. A remote repository caching Docker Hub is not
// the same thing as a standard one, and deleting it is not the same either.
func repositoryMode(r *artifactregistry.Repository) string {
	switch r.Mode {
	case "", "MODE_UNSPECIFIED", "STANDARD_REPOSITORY":
		return "standard"
	case "VIRTUAL_REPOSITORY":
		return "virtual"
	case "REMOTE_REPOSITORY":
		return "remote"
	default:
		return strings.ToLower(r.Mode)
	}
}
