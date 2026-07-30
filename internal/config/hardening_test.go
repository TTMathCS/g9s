package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadRefusesAConfigInAWorldWritableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not mean the same thing here")
	}

	// Write access to the directory is write access to the config one step
	// removed — the file can simply be replaced — and gcloud_path decides which
	// binary g9s executes.
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("projects:\n  - name: a\n    project_id: a-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted a config in a world-writable directory")
	}
	if !strings.Contains(err.Error(), "writable by group or others") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestLoadAcceptsAConfigInAStickyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not mean the same thing here")
	}

	// The sticky bit is what makes /tmp usable: it stops one user removing or
	// renaming another's files, so the config cannot be swapped out.
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("projects:\n  - name: a\n    project_id: a-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err != nil {
		t.Errorf("Load rejected a config in a sticky world-writable directory: %v", err)
	}
}

func TestLoadRefusesNonRegularFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not mean the same thing here")
	}
	// A directory is the reachable case; the check also covers a fifo, which
	// would otherwise block the read forever and read to the user as a hang
	// with no explanation.
	_, err := Load(t.TempDir())
	if err == nil {
		t.Fatal("Load accepted a directory")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestLoadRefusesAnOversizedConfig(t *testing.T) {
	huge := strings.Repeat("# padding to make this file large\n", (maxConfigBytes/34)+64)
	_, err := Load(writeConfig(t, huge))
	if err == nil {
		t.Fatal("Load accepted a file past the size bound")
	}
	if !strings.Contains(err.Error(), "larger than") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestExpandHomeOnlyExpandsTheUsersOwnHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	tests := []struct {
		in   string
		want string
	}{
		{"~", home},
		{"~/", home},
		{"~/.local/share/g9s", filepath.Join(home, ".local/share/g9s")},
		// "~other" names another user's home to a shell and cannot be resolved
		// here, so rewriting it would silently point somewhere else.
		{"~other/creds", "~other/creds"},
		{"~root", "~root"},
		{"/etc/g9s", "/etc/g9s"},
		{"relative/path", "relative/path"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := expandHome(tt.in); got != tt.want {
			t.Errorf("expandHome(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBigQueryJobWindowDefaultsAndOverrides(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
projects:
  - name: sandbox
    project_id: sandbox-123
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.BigQueryJobWindow(); got != 24*time.Hour {
		t.Errorf("default job window = %v, want 24h", got)
	}

	cfg, err = Load(writeConfig(t, `
defaults:
  bigquery_job_window: 4h
projects:
  - name: sandbox
    project_id: sandbox-123
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.BigQueryJobWindow(); got != 4*time.Hour {
		t.Errorf("configured job window = %v, want 4h", got)
	}
}

func TestNegativeDurationsAreRejected(t *testing.T) {
	// applyDefaults only fills a zero value, so a negative one would otherwise
	// reach the API as a refresh that times out before it starts, or a job
	// window that ends in the future and matches nothing.
	for _, body := range []string{
		"defaults:\n  list_timeout: -5s\nprojects:\n  - name: a\n    project_id: a-1\n",
		"defaults:\n  bigquery_job_window: -1h\nprojects:\n  - name: a\n    project_id: a-1\n",
	} {
		if _, err := Load(writeConfig(t, body)); err == nil {
			t.Errorf("a negative duration was accepted: %s", body)
		} else if !strings.Contains(err.Error(), "must be positive") {
			t.Errorf("unhelpful error: %v", err)
		}
	}
}

// A zero-valued Config is what several callers build by hand; the window
// accessor has to answer without a Load having run.
func TestBigQueryJobWindowOnAnUnloadedConfig(t *testing.T) {
	var cfg Config
	if got := cfg.BigQueryJobWindow(); got != 24*time.Hour {
		t.Errorf("window on a zero Config = %v, want the 24h default", got)
	}
}

func TestLoginNoBrowserDefaultsOffAndParses(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
projects:
  - name: sandbox
    project_id: sandbox-123
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Defaults.LoginNoBrowser {
		// Off by default: the browser flow is the better one when it works, and
		// on most machines it does.
		t.Error("login_no_browser defaulted to true")
	}

	cfg, err = Load(writeConfig(t, `
defaults:
  login_no_browser: true
projects:
  - name: sandbox
    project_id: sandbox-123
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Defaults.LoginNoBrowser {
		t.Error("login_no_browser: true did not take effect")
	}
}
