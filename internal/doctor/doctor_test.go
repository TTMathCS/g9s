package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const twoProjects = `defaults:
  regions: [us-central1]
  credential_dir: %s
projects:
  - name: sandbox
    project_id: my-sandbox
  - name: prod
    project_id: my-prod
`

func findings(r *Report, checkContains string) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if strings.Contains(f.Check, checkContains) {
			out = append(out, f)
		}
	}
	return out
}

// A missing credential directory means "not logged in", which the identity
// check already says. Reporting it twice — once as a path and once as a state —
// reads like two different problems with the same project.
func TestNotLoggedInIsReportedOnce(t *testing.T) {
	credDir := t.TempDir()
	path := writeConfig(t, strings.Replace(twoProjects, "%s", credDir, 1))

	report := Run(context.Background(), Options{ConfigPath: path, SkipNetwork: true})

	for _, project := range []string{"sandbox", "prod"} {
		got := findings(report, "project "+project)
		if len(got) != 1 {
			t.Errorf("project %s produced %d findings, want 1:", project, len(got))
			for _, f := range got {
				t.Errorf("  %s %s — %s", f.Level, f.Check, f.Detail)
			}
		}
	}
}

// Skipping the network removes the check that would otherwise report it, so
// the directory becomes the only evidence there is.
func TestOfflineStillReportsThatNoProjectIsLoggedIn(t *testing.T) {
	credDir := t.TempDir()
	path := writeConfig(t, strings.Replace(twoProjects, "%s", credDir, 1))

	report := Run(context.Background(), Options{ConfigPath: path, SkipNetwork: true})

	got := findings(report, "project sandbox")
	if len(got) != 1 {
		t.Fatalf("want exactly one finding, got %d", len(got))
	}
	if !strings.Contains(got[0].Detail, "not logged in") {
		t.Errorf("offline run does not say the project is not logged in: %q", got[0].Detail)
	}
	if got[0].Level != LevelWarn {
		t.Errorf("level = %v, want warn: not being logged in yet is the expected state on a new machine", got[0].Level)
	}
}

// A credential directory others can read is a credential others can steal, and
// unlike absence it is never the expected state.
func TestWorldReadableCredentialDirectoryFails(t *testing.T) {
	credDir := t.TempDir()
	path := writeConfig(t, strings.Replace(twoProjects, "%s", credDir, 1))

	if err := os.MkdirAll(filepath.Join(credDir, "sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}

	report := Run(context.Background(), Options{ConfigPath: path, SkipNetwork: true})

	var found bool
	for _, f := range report.Findings {
		if strings.Contains(f.Check, "credential directory") {
			found = true
			if f.Level != LevelFail {
				t.Errorf("level = %v, want FAIL", f.Level)
			}
			if !strings.Contains(f.Remedy, "chmod 700") {
				t.Errorf("remedy does not say how to fix it: %q", f.Remedy)
			}
		}
	}
	if !found {
		t.Error("a mode 0755 credential directory produced no finding")
	}
	if !report.Failed() {
		t.Error("report.Failed() = false; a readable credential directory has to fail the run")
	}
}

func TestMissingConfigFailsWithTheCommandThatFixesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")

	report := Run(context.Background(), Options{ConfigPath: path, SkipNetwork: true})

	if !report.Failed() {
		t.Fatal("a missing config did not fail the run")
	}
	got := findings(report, "config file")
	if len(got) != 1 || !strings.Contains(got[0].Remedy, "g9s -init") {
		t.Errorf("missing config does not point at `g9s -init`: %+v", got)
	}
}

// The loader treats unknown keys as errors so a typo is named rather than
// ignored; doctor has to survive that and say so rather than panicking on a
// nil config.
func TestUnparseableConfigIsReportedNotFatal(t *testing.T) {
	path := writeConfig(t, "defaults:\n  regions: [us-central1]\n  nonsense_key: true\n")

	report := Run(context.Background(), Options{ConfigPath: path, SkipNetwork: true})

	if !report.Failed() {
		t.Fatal("an invalid config did not fail the run")
	}
	// Checks that do not depend on the config still have to run: someone with a
	// broken config still wants to know whether gcloud is installed.
	if len(findings(report, "platform")) != 1 {
		t.Error("checks independent of the config stopped running after the config failed")
	}
}

func TestReportCountsAndFailedAgree(t *testing.T) {
	r := &Report{}
	r.add(LevelOK, "a", "", "")
	r.add(LevelWarn, "b", "", "")
	r.add(LevelWarn, "c", "", "")
	r.add(LevelFail, "d", "", "")

	ok, warn, fail := r.Counts()
	if ok != 1 || warn != 2 || fail != 1 {
		t.Errorf("Counts() = %d, %d, %d; want 1, 2, 1", ok, warn, fail)
	}
	if !r.Failed() {
		t.Error("Failed() = false with a failure present")
	}
}

// Warnings must not fail the run: "not logged in yet" is the expected state on
// a fresh machine, and a check that cries wolf on first run gets ignored on
// the run that matters.
func TestWarningsAloneDoNotFailTheRun(t *testing.T) {
	r := &Report{}
	r.add(LevelOK, "a", "", "")
	r.add(LevelWarn, "b", "", "")

	if r.Failed() {
		t.Error("Failed() = true with only warnings")
	}
}

func TestWriteWrapsRemediesAndOmitsThemForPassingChecks(t *testing.T) {
	r := &Report{}
	r.add(LevelOK, "quiet", "fine", "this remedy should not be printed")
	r.add(LevelFail, "loud", "broken", strings.Repeat("word ", 40))

	var sb strings.Builder
	Write(&sb, r)
	out := sb.String()

	if strings.Contains(out, "should not be printed") {
		t.Error("a remedy was printed for a passing check")
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 110 {
			t.Errorf("line is %d chars, too wide to read in a terminal:\n%s", len(line), line)
		}
	}
	if !strings.Contains(out, "1 ok, 0 warning(s), 1 failure(s)") {
		t.Errorf("summary line missing or wrong:\n%s", out)
	}
}
