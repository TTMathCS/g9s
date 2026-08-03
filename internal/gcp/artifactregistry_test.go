package gcp

import (
	"strings"
	"testing"

	artifactregistry "google.golang.org/api/artifactregistry/v1"
)

func repoRow(r *artifactregistry.Repository) Resource {
	return repositoryResource(testProject(), "us-central1", r)
}

func TestRepositoryResourceShape(t *testing.T) {
	r := repoRow(testRepository())

	if r.Name != "service-images" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "us-central1" {
		t.Errorf("Location = %q", r.Location)
	}
	if r.Row[2] != "docker" {
		t.Errorf("format cell = %q", r.Row[2])
	}
	if r.Row[3] != "standard" {
		t.Errorf("mode cell = %q", r.Row[3])
	}
	if r.Row[4] != "412.0GB" {
		t.Errorf("size cell = %q, want the registry size", r.Row[4])
	}
}

// TestRepositoryWithNoCleanupPolicyIsTheFinding is what the kind is for. Every
// CI build pushes an image, nothing removes one by default, and the resource
// has no state field that says so.
func TestRepositoryWithNoCleanupPolicyIsTheFinding(t *testing.T) {
	r := repoRow(testRepository())

	if r.Status != "NO_CLEANUP" {
		t.Errorf("Status = %q, want NO_CLEANUP", r.Status)
	}
	if !strings.Contains(r.Row[5], "grows forever") {
		t.Errorf("cleanup cell = %q, want it to say the repository is unpruned", r.Row[5])
	}
}

// TestDryRunCleanupIsWorseThanNone: a policy that is configured, reads as
// configured, and deletes nothing is the trap under the obvious finding.
func TestDryRunCleanupIsWorseThanNone(t *testing.T) {
	repo := testRepository()
	repo.CleanupPolicies = map[string]artifactregistry.CleanupPolicy{
		"keep-recent": {Id: "keep-recent"},
	}
	repo.CleanupPolicyDryRun = true

	r := repoRow(repo)
	if r.Status != "CLEANUP_DRY_RUN" {
		t.Errorf("Status = %q, want the dry run flagged", r.Status)
	}
	if !strings.Contains(r.Row[5], "dry run") {
		t.Errorf("cleanup cell = %q, want it to say the policy does not delete", r.Row[5])
	}
}

func TestRepositoryWithLiveCleanupIsOrdinary(t *testing.T) {
	repo := testRepository()
	repo.CleanupPolicies = map[string]artifactregistry.CleanupPolicy{
		"keep-recent": {Id: "keep-recent"},
		"delete-old":  {Id: "delete-old"},
	}

	r := repoRow(repo)
	if r.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE", r.Status)
	}
	if r.Row[5] != "2 active" {
		t.Errorf("cleanup cell = %q", r.Row[5])
	}
}

func TestRepositoryModes(t *testing.T) {
	// A remote repository proxying Docker Hub is not a place artifacts live,
	// and deleting it is not the same action as deleting a standard one.
	tests := map[string]string{
		"STANDARD_REPOSITORY": "standard",
		"VIRTUAL_REPOSITORY":  "virtual",
		"REMOTE_REPOSITORY":   "remote",
		"MODE_UNSPECIFIED":    "standard",
		"":                    "standard",
	}
	for mode, want := range tests {
		if got := repositoryMode(&artifactregistry.Repository{Mode: mode}); got != want {
			t.Errorf("repositoryMode(%q) = %q, want %q", mode, got, want)
		}
	}
}

func TestRepositoryWithoutAFormat(t *testing.T) {
	for _, format := range []string{"", "FORMAT_UNSPECIFIED"} {
		if got := repositoryFormat(&artifactregistry.Repository{Format: format}); got != "-" {
			t.Errorf("repositoryFormat(%q) = %q, want a dash", format, got)
		}
	}
}

func TestRepositoryWithNoSizeReported(t *testing.T) {
	// Zero is a real answer for an empty repository and must not read as "-".
	repo := testRepository()
	repo.SizeBytes = 0

	if got := repoRow(repo).Row[4]; got != "0B" {
		t.Errorf("size cell = %q, want 0B", got)
	}
}

func TestRepositoriesAreNotSSHOrAirflowTargets(t *testing.T) {
	r := repoRow(testRepository())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a repository is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a repository has an Airflow URI")
	}
}
