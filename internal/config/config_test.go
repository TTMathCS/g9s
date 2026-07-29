package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
defaults:
  regions: [us-central1]
projects:
  - name: sandbox
    project_id: sandbox-123
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Defaults.GcloudPath != "gcloud" {
		t.Errorf("GcloudPath = %q, want gcloud", cfg.Defaults.GcloudPath)
	}
	if got := cfg.Defaults.ListTimeout.Duration(); got != 90*time.Second {
		t.Errorf("ListTimeout = %v, want 90s", got)
	}
	if cfg.Defaults.CredentialDir == "" {
		t.Error("CredentialDir should default to a path under the home directory")
	}
}

func TestRegionPrecedence(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
defaults:
  regions: [default-region]
  dataproc_regions: [default-dataproc]
projects:
  - name: a
    project_id: a-1
  - name: b
    project_id: b-1
    regions: [project-region]
  - name: c
    project_id: c-1
    regions: [project-region]
    dataproc_regions: [project-dataproc]
    composer_locations: [project-composer]
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tests := []struct {
		project      string
		wantDataproc string
		wantComposer string
	}{
		// No project overrides: defaults.dataproc_regions beats defaults.regions.
		{"a", "default-dataproc", "default-region"},
		// A project's own regions beat every default, including the
		// service-specific one.
		{"b", "project-region", "project-region"},
		// Service-specific project overrides beat the project's regions.
		{"c", "project-dataproc", "project-composer"},
	}

	for _, tc := range tests {
		p, ok := cfg.Project(tc.project)
		if !ok {
			t.Fatalf("project %q not found", tc.project)
		}
		if got := cfg.DataprocRegions(p); got[0] != tc.wantDataproc {
			t.Errorf("%s: DataprocRegions = %v, want first element %q", tc.project, got, tc.wantDataproc)
		}
		if got := cfg.ComposerLocations(p); got[0] != tc.wantComposer {
			t.Errorf("%s: ComposerLocations = %v, want first element %q", tc.project, got, tc.wantComposer)
		}
	}
}

func TestDataprocRegionsAlwaysIncludeGlobal(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
defaults:
  regions: [us-central1]
projects:
  - name: a
    project_id: a-1
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	p, _ := cfg.Project("a")
	regions := cfg.DataprocRegions(p)

	if !contains(regions, "global") {
		t.Errorf("DataprocRegions = %v, want it to include global", regions)
	}

	// Composer has no global location, so it must not be injected there.
	if contains(cfg.ComposerLocations(p), "global") {
		t.Errorf("ComposerLocations should not include global, got %v", cfg.ComposerLocations(p))
	}
}

func TestDataprocRegionsDoNotDuplicateGlobal(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
defaults:
  regions: [global, us-central1]
projects:
  - name: a
    project_id: a-1
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	p, _ := cfg.Project("a")
	regions := cfg.DataprocRegions(p)

	count := 0
	for _, r := range regions {
		if r == "global" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("global appears %d times in %v, want 1", count, regions)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "no projects",
			body: "defaults:\n  regions: [us-central1]\n",
			want: "no projects defined",
		},
		{
			name: "missing project id",
			body: "defaults:\n  regions: [us-central1]\nprojects:\n  - name: a\n",
			want: "project_id is required",
		},
		{
			name: "missing name",
			body: "defaults:\n  regions: [us-central1]\nprojects:\n  - project_id: a-1\n",
			want: "name is required",
		},
		{
			name: "duplicate names",
			body: "defaults:\n  regions: [us-central1]\nprojects:\n  - name: a\n    project_id: a-1\n  - name: a\n    project_id: a-2\n",
			want: "duplicate project name",
		},
		{
			name: "no regions anywhere",
			body: "projects:\n  - name: a\n    project_id: a-1\n",
			want: "no regions configured",
		},
		{
			name: "unknown key",
			body: "defaults:\n  regions: [us-central1]\nprojects:\n  - name: a\n    project_id: a-1\n    regionz: [typo]\n",
			want: "field regionz not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestDurationParsing(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
defaults:
  regions: [us-central1]
  list_timeout: 3m
projects:
  - name: a
    project_id: a-1
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.ListTimeout.Duration(); got != 3*time.Minute {
		t.Errorf("ListTimeout = %v, want 3m", got)
	}
}

func TestDurationParsingRejectsGarbage(t *testing.T) {
	_, err := Load(writeConfig(t, `
defaults:
  regions: [us-central1]
  list_timeout: soon
projects:
  - name: a
    project_id: a-1
`))
	if err == nil || !strings.Contains(err.Error(), "invalid duration") {
		t.Fatalf("error = %v, want an invalid duration error", err)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// --- permissions ---

func writeConfigMode(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "defaults:\n  regions: [us-central1]\nprojects:\n  - name: a\n    project_id: a-1\n"
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile is subject to umask, so set the mode explicitly.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRejectsWritableConfig(t *testing.T) {
	// defaults.gcloud_path names the binary g9s executes, so a config anyone
	// else can write is code execution as the user running g9s.
	for _, mode := range []os.FileMode{0o622, 0o662, 0o666, 0o777} {
		path := writeConfigMode(t, mode)
		if _, err := Load(path); err == nil {
			t.Errorf("mode %04o was accepted, want a refusal", mode)
		} else if !strings.Contains(err.Error(), "writable by group or others") {
			t.Errorf("mode %04o: error %q does not explain the problem", mode, err)
		}
	}
}

func TestLoadAcceptsPrivateAndReadableConfig(t *testing.T) {
	// Readable-by-others is the default umask in plenty of places and the
	// contents are not secret, so only writability is refused.
	for _, mode := range []os.FileMode{0o600, 0o640, 0o644} {
		path := writeConfigMode(t, mode)
		if _, err := Load(path); err != nil {
			t.Errorf("mode %04o was refused: %v", mode, err)
		}
	}
}
