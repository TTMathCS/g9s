package auth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTMathCS/g9s/internal/config"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	return &Manager{credentialRoot: t.TempDir(), gcloudPath: "gcloud"}
}

func TestSanitize(t *testing.T) {
	// Project names come from a hand-edited YAML file and become directory
	// names, so anything that could escape the credential root must be
	// neutralised.
	tests := []struct{ in, want string }{
		{"prod-data", "prod-data"},
		{"prod data", "prod-data"},
		{"prod/../../etc", "prod-etc"},
		{"../..", "project"},
		{"", "project"},
		{"///", "project"},
		{"Prod_Data.1", "Prod_Data.1"},
	}
	for _, tc := range tests {
		if got := sanitize(tc.in); got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestConfigDirStaysInsideCredentialRoot(t *testing.T) {
	mgr := testManager(t)

	for _, name := range []string{"prod/../../etc", "../../../root", "..", "a/b/c"} {
		dir := mgr.ConfigDir(config.Project{Name: name})
		rel, err := filepath.Rel(mgr.credentialRoot, dir)
		if err != nil {
			t.Fatalf("Rel: %v", err)
		}
		if strings.HasPrefix(rel, "..") {
			t.Errorf("project %q escaped the credential root: %s", name, dir)
		}
	}
}

func TestProjectsGetDistinctCredentialDirs(t *testing.T) {
	// The whole point of the isolation: two projects must never share a
	// credential directory, or logging into one silently changes the other.
	mgr := testManager(t)

	a := mgr.ConfigDir(config.Project{Name: "prod-data", ProjectID: "p1"})
	b := mgr.ConfigDir(config.Project{Name: "prod-logs", ProjectID: "p2"})

	if a == b {
		t.Fatalf("distinct projects share a credential dir: %s", a)
	}
}

func TestADCPathIsUnderConfigDir(t *testing.T) {
	mgr := testManager(t)
	p := config.Project{Name: "sandbox", ProjectID: "sandbox-123"}

	if got, want := mgr.ADCPath(p), filepath.Join(mgr.ConfigDir(p), adcFile); got != want {
		t.Errorf("ADCPath = %q, want %q", got, want)
	}
}

func TestCheckReportsMissingCredentials(t *testing.T) {
	mgr := testManager(t)
	status := mgr.Check(context.Background(), config.Project{Name: "sandbox", ProjectID: "sandbox-123"})

	if status.State != StateMissing {
		t.Errorf("State = %v, want StateMissing", status.State)
	}
	if status.Valid() {
		t.Error("Valid() should be false with no credentials")
	}
	if status.Err != nil {
		// A plain "not logged in yet" is not an error worth showing.
		t.Errorf("Err = %v, want nil for a first run", status.Err)
	}
}

func TestCheckReportsUnreadableCredentialsAsExpired(t *testing.T) {
	mgr := testManager(t)
	p := config.Project{Name: "sandbox", ProjectID: "sandbox-123"}

	if err := os.MkdirAll(mgr.ConfigDir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mgr.ADCPath(p), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	status := mgr.Check(context.Background(), p)
	if status.State != StateExpired {
		t.Errorf("State = %v, want StateExpired", status.State)
	}
	if status.Err == nil {
		t.Error("Err should explain why the credentials are unusable")
	}
}

func TestCheckExtractsAccountFromCredentials(t *testing.T) {
	mgr := testManager(t)
	p := config.Project{Name: "sandbox", ProjectID: "sandbox-123"}

	if err := os.MkdirAll(mgr.ConfigDir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	// Valid JSON but not a credential type the library accepts, so the check
	// still fails — the account should be reported regardless, since it is
	// what tells the user which identity needs re-authenticating.
	body := `{"account":"svc-support@example.com","type":"mystery"}`
	if err := os.WriteFile(mgr.ADCPath(p), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	status := mgr.Check(context.Background(), p)
	if status.Account != "svc-support@example.com" {
		t.Errorf("Account = %q, want svc-support@example.com", status.Account)
	}
}

func TestAccountFromADC(t *testing.T) {
	tests := []struct{ body, want string }{
		{`{"account":"a@example.com"}`, "a@example.com"},
		{`{"client_email":"sa@project.iam.gserviceaccount.com"}`, "sa@project.iam.gserviceaccount.com"},
		{`{"account":"a@example.com","client_email":"sa@example.com"}`, "a@example.com"},
		{`{}`, ""},
		{`not json`, ""},
	}
	for _, tc := range tests {
		if got := accountFromADC([]byte(tc.body)); got != tc.want {
			t.Errorf("accountFromADC(%s) = %q, want %q", tc.body, got, tc.want)
		}
	}
}

func TestLoginCmdIsolatesCredentials(t *testing.T) {
	mgr := testManager(t)
	p := config.Project{Name: "prod-data", ProjectID: "prod-123", Account: "svc@example.com"}

	cmd, err := mgr.LoginCmd(p, false)
	if err != nil {
		t.Fatalf("LoginCmd: %v", err)
	}

	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{"auth", "application-default", "login", "--account=svc@example.com"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	if strings.Contains(args, "--no-browser") {
		t.Errorf("args %q should not request --no-browser", args)
	}

	assertEnv(t, cmd.Env, "CLOUDSDK_CONFIG", mgr.ConfigDir(p))
	// Without a quota project, user credentials get rejected by most APIs.
	assertEnv(t, cmd.Env, "GOOGLE_CLOUD_QUOTA_PROJECT", "prod-123")

	// The directory has to exist before gcloud writes into it.
	if _, err := os.Stat(mgr.ConfigDir(p)); err != nil {
		t.Errorf("credential dir not created: %v", err)
	}
}

func TestLoginCmdNoBrowser(t *testing.T) {
	mgr := testManager(t)
	cmd, err := mgr.LoginCmd(config.Project{Name: "a", ProjectID: "a-1"}, true)
	if err != nil {
		t.Fatalf("LoginCmd: %v", err)
	}
	if !strings.Contains(strings.Join(cmd.Args, " "), "--no-browser") {
		t.Errorf("args = %v, want --no-browser", cmd.Args)
	}
}

func TestLoginCmdOmitsAccountFlagWhenUnset(t *testing.T) {
	mgr := testManager(t)
	cmd, err := mgr.LoginCmd(config.Project{Name: "a", ProjectID: "a-1"}, false)
	if err != nil {
		t.Fatalf("LoginCmd: %v", err)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "--account") {
		t.Errorf("args = %v, want no --account flag", cmd.Args)
	}
}

func TestGcloudCmdScopesToProject(t *testing.T) {
	mgr := testManager(t)
	p := config.Project{Name: "prod-data", ProjectID: "prod-123", Account: "svc@example.com"}

	cmd := mgr.GcloudCmd(p, "compute", "ssh", "web-01", "--zone=us-central1-a")

	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{"--project=prod-123", "--account=svc@example.com", "compute ssh web-01"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	assertEnv(t, cmd.Env, "CLOUDSDK_CONFIG", mgr.ConfigDir(p))
}

func TestClientOptionsAreProjectScoped(t *testing.T) {
	mgr := testManager(t)
	a := config.Project{Name: "a", ProjectID: "a-1"}
	b := config.Project{Name: "b", ProjectID: "b-1"}

	if got := len(mgr.ClientOptions(a)); got != 2 {
		t.Fatalf("got %d client options, want credentials file and quota project", got)
	}
	// option.ClientOption values are opaque, so assert on the inputs they are
	// built from: the credential file each project's clients will read.
	if mgr.ADCPath(a) == mgr.ADCPath(b) {
		t.Errorf("two projects share a credential file: %s", mgr.ADCPath(a))
	}
}

func TestStatusSummaryDistinguishesStates(t *testing.T) {
	seen := map[string]bool{}
	for _, state := range []State{StateValid, StateExpired, StateMissing, StateUnknown} {
		summary := Status{State: state}.Summary()
		if summary == "" {
			t.Errorf("state %v has an empty summary", state)
		}
		if seen[summary] {
			t.Errorf("state %v reuses summary %q", state, summary)
		}
		seen[summary] = true
	}
}

func assertEnv(t *testing.T, env []string, key, want string) {
	t.Helper()
	prefix := key + "="

	// Later entries win in exec, so scan backwards.
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			if got := strings.TrimPrefix(env[i], prefix); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
			return
		}
	}
	t.Errorf("%s not set in command environment", key)
}
