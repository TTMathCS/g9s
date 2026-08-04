package doctor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
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

// The original version check asked gcloud for `--format=value(Google Cloud
// SDK)` — a projection key with spaces in it, which gcloud's grammar splits on
// whitespace. It failed on a perfectly healthy install and reported `exit
// status 1`, a warning nobody could act on. These fixtures are what gcloud
// really prints, so the parser is held to real output rather than to a
// projection nobody could test without a gcloud to run.
func TestGcloudVersionIsParsedFromJSON(t *testing.T) {
	out := []byte(`{
  "Google Cloud SDK": "458.0.1",
  "bq": "2.0.101",
  "core": "2024.01.12",
  "gcloud-crc32c": "1.0.0",
  "gsutil": "5.27"
}`)
	if got := parseGcloudVersion(out); got != "458.0.1" {
		t.Errorf("parseGcloudVersion(json) = %q, want 458.0.1", got)
	}
}

func TestGcloudVersionIsParsedFromThePlainListing(t *testing.T) {
	out := []byte(`Google Cloud SDK 458.0.1
bq 2.0.101
core 2024.01.12
gcloud-crc32c 1.0.0
gsutil 5.27
`)
	if got := parseGcloudVersion(out); got != "458.0.1" {
		t.Errorf("parseGcloudVersion(plain) = %q, want 458.0.1", got)
	}
}

// Some installs print an update notice above the listing, and gcloud writes
// component warnings into the same stream.
func TestGcloudVersionSurvivesSurroundingNoise(t *testing.T) {
	out := []byte(`Updates are available for some Google Cloud CLI components.

Google Cloud SDK 372.0.0
bq 2.0.72
`)
	if got := parseGcloudVersion(out); got != "372.0.0" {
		t.Errorf("parseGcloudVersion = %q, want 372.0.0", got)
	}
}

// Output with no version in it must come back empty rather than as a
// plausible-looking string, or the report states a version gcloud never gave.
func TestUnrecognisedOutputYieldsNoVersion(t *testing.T) {
	for _, out := range []string{
		"",
		"ERROR: (gcloud.version) unrecognized arguments",
		`{"bq": "2.0.101"}`,
		"Google Cloud SDK",
	} {
		if got := parseGcloudVersion([]byte(out)); got != "" {
			t.Errorf("parseGcloudVersion(%q) = %q, want no version", out, got)
		}
	}
}

// A failing gcloud has to say what it said. `exit status 1` on its own is the
// warning that sent somebody looking for a broken install they did not have.
func TestAFailingGcloudReportsItsOwnComplaint(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "gcloud")
	script := "#!/bin/sh\necho 'ERROR: (gcloud.version) Unknown attribute [Cloud]' >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	version, detail := gcloudVersion(fake)
	if version != "" {
		t.Errorf("version = %q from a gcloud that failed", version)
	}
	if !strings.Contains(detail, "Unknown attribute") {
		t.Errorf("detail = %q, want gcloud's own message rather than just the exit status", detail)
	}
}

// The check has to work against a gcloud that only speaks the plain listing,
// which is the fallback the JSON attempt exists to be rescued by.
func TestAGcloudThatRefusesJSONStillYieldsAVersion(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "gcloud")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  case \"$a\" in --format=json) echo 'ERROR: unsupported' >&2; exit 1;; esac\n" +
		"done\n" +
		"echo 'Google Cloud SDK 372.0.0'\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	version, detail := gcloudVersion(fake)
	if version != "372.0.0" {
		t.Errorf("version = %q (detail %q), want the plain listing to have rescued it", version, detail)
	}
}

// -offline must reach nothing at all. Someone runs it on a locked-down host
// precisely to get an answer without the network, and a probe that fired anyway
// would hang the one command they can still use.
func TestOfflineDoctorMakesNoEgressProbe(t *testing.T) {
	path := writeConfig(t, twoProjects)
	report := Run(context.Background(), Options{ConfigPath: path, SkipNetwork: true})

	for _, f := range report.Findings {
		if f.Check == "reach google" {
			t.Errorf("offline doctor probed the network anyway: %+v", f)
		}
	}
}

// The two failures need opposite remedies — one wants a proxy address, the
// other a CA certificate — so telling them apart is the whole value of the
// check. A certificate signed by an unknown authority is what a TLS-terminating
// corporate proxy produces.
func TestTLSTrustFailuresAreDistinguishedFromUnreachableHosts(t *testing.T) {
	trust := []error{
		x509.UnknownAuthorityError{},
		x509.HostnameError{Host: "oauth2.googleapis.com"},
		x509.CertificateInvalidError{Reason: x509.Expired},
		&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
		fmt.Errorf("get %q: %w", tokenEndpoint, x509.UnknownAuthorityError{}),
	}
	for _, err := range trust {
		if !isTLSTrustFailure(err) {
			t.Errorf("%T was not recognised as a certificate problem", err)
		}
	}

	unreachable := []error{
		errors.New("dial tcp 142.250.1.95:443: connect: connection refused"),
		errors.New("proxyconnect tcp: dial tcp: lookup proxy.corp: no such host"),
		context.DeadlineExceeded,
	}
	for _, err := range unreachable {
		if isTLSTrustFailure(err) {
			t.Errorf("%v was misread as a certificate problem", err)
		}
	}
}

// The probe has to be bounded. `g9s doctor` is what somebody runs when things
// are already stuck, and a proxy that accepts connections and never answers is
// exactly the situation that produces the report.
func TestTheEgressProbeIsBounded(t *testing.T) {
	if egressTimeout <= 0 || egressTimeout > 15*time.Second {
		t.Errorf("egressTimeout = %v, want a short positive bound", egressTimeout)
	}
}
