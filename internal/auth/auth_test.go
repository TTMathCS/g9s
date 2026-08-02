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

func TestNewManagerRejectsCredentialDirCollisions(t *testing.T) {
	// "prod/data" sanitizes to "prod-data" — two distinct projects sharing a
	// credential directory means logging into one re-identifies the other.
	cfg := &config.Config{Projects: []config.Project{
		{Name: "prod-data", ProjectID: "p-1"},
		{Name: "prod/data", ProjectID: "p-2"},
	}}

	if _, err := NewManager(cfg); err == nil {
		t.Fatal("colliding credential dirs should be refused")
	} else if !strings.Contains(err.Error(), "prod-data") {
		t.Errorf("error should name the colliding directory: %v", err)
	}
}

func TestNewManagerRejectsCollisionsThatOnlyDifferInCase(t *testing.T) {
	// macOS and Windows treat "Prod" and "prod" as one directory, so a check
	// that only caught exact collisions would pass on Linux and hand two
	// projects the same credentials on a laptop.
	cfg := &config.Config{Projects: []config.Project{
		{Name: "Prod", ProjectID: "p-1"},
		{Name: "prod", ProjectID: "p-2"},
	}}

	if _, err := NewManager(cfg); err == nil {
		t.Fatal("names differing only in case should be refused")
	} else if !strings.Contains(err.Error(), "case") {
		t.Errorf("error should explain the case collision: %v", err)
	}
}

func TestNewManagerAcceptsDistinctProjects(t *testing.T) {
	cfg := &config.Config{Projects: []config.Project{
		{Name: "prod-data", ProjectID: "p-1"},
		{Name: "prod-logs", ProjectID: "p-2"},
	}}
	if _, err := NewManager(cfg); err != nil {
		t.Fatalf("distinct names should be accepted: %v", err)
	}
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
	for _, state := range []State{StateValid, StateExpired, StateMissing, StateWrongAccount, StateUnknown} {
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

func TestIdentityStateRejectsTheWrongConfiguredAccount(t *testing.T) {
	if got := identityState("expected@example.com", "other@example.com"); got != StateWrongAccount {
		t.Errorf("identityState = %v, want StateWrongAccount", got)
	}
	if (Status{State: StateWrongAccount}).Valid() {
		t.Error("a live token for the wrong account must not authorize resource loading")
	}

	// Email addresses are case-insensitive, and an unconfigured expectation or
	// an ADC format that cannot name its principal leaves nothing to compare.
	for _, pair := range [][2]string{
		{"ME@example.com", "me@example.com"},
		{"", "me@example.com"},
		{"me@example.com", ""},
	} {
		if got := identityState(pair[0], pair[1]); got != StateValid {
			t.Errorf("identityState(%q, %q) = %v, want StateValid", pair[0], pair[1], got)
		}
	}
}

func TestStatusSummaryDisplaysTheActualIdentity(t *testing.T) {
	valid := Status{State: StateValid, Account: "actual@example.com"}.Summary()
	if !strings.Contains(valid, "actual@example.com") {
		t.Errorf("valid summary hides actual identity: %q", valid)
	}

	mismatch := Status{
		State:           StateWrongAccount,
		Account:         "actual@example.com",
		ExpectedAccount: "expected@example.com",
	}.Summary()
	for _, account := range []string{"actual@example.com", "expected@example.com"} {
		if !strings.Contains(mismatch, account) {
			t.Errorf("mismatch summary %q omits %q", mismatch, account)
		}
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

func TestLoopbackUsable(t *testing.T) {
	// Whether the ordinary login can finish at all: gcloud's flow ends with the
	// browser fetching http://localhost:<port>/ on this machine.
	env := func(pairs map[string]string) func(string) string {
		return func(k string) string { return pairs[k] }
	}

	tests := []struct {
		name string
		goos string
		vars map[string]string
		want bool
	}{
		{"local workstation", "darwin", nil, true},
		{"local linux desktop", "linux", map[string]string{"DISPLAY": ":0"}, true},
		// The reported failure: sign-in succeeds, the redirect lands on the
		// laptop instead of here, and gcloud waits forever.
		{"ssh to a linux box", "linux", map[string]string{"SSH_CONNECTION": "10.0.0.1 22"}, false},
		{"ssh to a mac", "darwin", map[string]string{"SSH_TTY": "/dev/ttys001"}, false},
		{"ssh with X forwarding", "linux", map[string]string{
			"SSH_CONNECTION": "10.0.0.1 22", "DISPLAY": "localhost:10.0",
		}, true},
		{"ssh with wayland", "linux", map[string]string{
			"SSH_CLIENT": "10.0.0.1", "WAYLAND_DISPLAY": "wayland-0",
		}, true},
		{"headless linux, no ssh", "linux", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := loopbackUsable(tt.goos, env(tt.vars)); got != tt.want {
				t.Errorf("loopbackUsable(%s, %v) = %v, want %v", tt.goos, tt.vars, got, tt.want)
			}
		})
	}
}

func TestProxyMayBlockLoopback(t *testing.T) {
	// A browser told to send everything through a proxy sends localhost there
	// too, and the proxy cannot route it back here.
	env := func(pairs map[string]string) func(string) string {
		return func(k string) string { return pairs[k] }
	}

	tests := []struct {
		name string
		vars map[string]string
		want bool
	}{
		{"no proxy at all", nil, false},
		{"proxy with no exemption", map[string]string{"HTTPS_PROXY": "http://proxy:8080"}, true},
		{"lowercase proxy var", map[string]string{"https_proxy": "http://proxy:8080"}, true},
		{"proxy exempting localhost", map[string]string{
			"HTTPS_PROXY": "http://proxy:8080", "NO_PROXY": "localhost,127.0.0.1",
		}, false},
		{"exemption in the lowercase var", map[string]string{
			"HTTP_PROXY": "http://proxy:8080", "no_proxy": "example.com, localhost",
		}, false},
		{"exemption by ipv6 loopback", map[string]string{
			"ALL_PROXY": "socks5://proxy:1080", "NO_PROXY": "::1",
		}, false},
		{"exemption that misses loopback", map[string]string{
			"HTTPS_PROXY": "http://proxy:8080", "NO_PROXY": "example.com,.internal",
		}, true},
		{"wildcard exemption", map[string]string{
			"HTTPS_PROXY": "http://proxy:8080", "NO_PROXY": "*",
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := proxyMayBlockLoopback(env(tt.vars)); got != tt.want {
				t.Errorf("proxyMayBlockLoopback(%v) = %v, want %v", tt.vars, got, tt.want)
			}
		})
	}
}

func TestCredentialsFileReplacesTheManagedPath(t *testing.T) {
	// The way in when the login handshake cannot complete at all: point g9s at
	// credentials obtained some other way and it reads those instead.
	cfg := &config.Config{
		Projects: []config.Project{
			{Name: "ny-dev", ProjectID: "ny-dev-1", CredentialsFile: "/creds/shared/adc.json"},
			{Name: "ny-prod", ProjectID: "ny-prod-1"},
		},
	}
	cfg.Defaults.CredentialDir = t.TempDir()

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if got := mgr.ADCPath(cfg.Projects[0]); got != "/creds/shared/adc.json" {
		t.Errorf("ADCPath = %q, want the configured file", got)
	}
	if mgr.ManagesCredentials(cfg.Projects[0]) {
		t.Error("g9s should not claim to manage a project that reads a file")
	}

	// The project without one is untouched.
	want := filepath.Join(mgr.ConfigDir(cfg.Projects[1]), adcFile)
	if got := mgr.ADCPath(cfg.Projects[1]); got != want {
		t.Errorf("ADCPath = %q, want %q", got, want)
	}
	if !mgr.ManagesCredentials(cfg.Projects[1]) {
		t.Error("a project with no credentials_file is managed by g9s")
	}
}

func TestCredentialsFileFlowsIntoClientOptionsAndCheck(t *testing.T) {
	// The point of resolving it in ADCPath: every lister's client options and
	// the validity check both go through there, so nothing else needs to know.
	dir := t.TempDir()
	file := filepath.Join(dir, "shared-adc.json")
	if err := os.WriteFile(file, []byte(`{"account":"me@example.com","type":"mystery"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Projects: []config.Project{{Name: "ny-dev", ProjectID: "ny-dev-1", CredentialsFile: file}}}
	cfg.Defaults.CredentialDir = t.TempDir()

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Check reads the configured file, not the per-project directory, so the
	// project is not reported as "never logged in".
	status := mgr.Check(context.Background(), cfg.Projects[0])
	if status.State == StateMissing {
		t.Error("Check looked past the configured credentials file")
	}
	if status.Account != "me@example.com" {
		t.Errorf("Account = %q, want it read from the configured file", status.Account)
	}
	if len(mgr.ClientOptions(cfg.Projects[0])) == 0 {
		t.Error("no client options built")
	}
}

func TestDefaultsCredentialsFileAppliesToEveryProject(t *testing.T) {
	cfg := &config.Config{Projects: []config.Project{
		{Name: "a", ProjectID: "a-1"},
		{Name: "b", ProjectID: "b-1", CredentialsFile: "/creds/b.json"},
	}}
	cfg.Defaults.CredentialDir = t.TempDir()
	cfg.Defaults.CredentialsFile = "/creds/shared.json"

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if got := mgr.ADCPath(cfg.Projects[0]); got != "/creds/shared.json" {
		t.Errorf("ADCPath = %q, want the default", got)
	}
	// A project's own setting still wins over the default.
	if got := mgr.ADCPath(cfg.Projects[1]); got != "/creds/b.json" {
		t.Errorf("ADCPath = %q, want the per-project override", got)
	}
}

func TestGcloudIsOnlyRequiredWhenG9sManagesALogin(t *testing.T) {
	// A machine where the handshake cannot complete may have no gcloud at all.
	// Refusing to start there would block the one setup that does work.
	managed := &config.Config{Projects: []config.Project{
		{Name: "a", ProjectID: "a-1", CredentialsFile: "/creds/a.json"},
		{Name: "b", ProjectID: "b-1"},
	}}
	managed.Defaults.CredentialDir = t.TempDir()
	mgr, err := NewManager(managed)
	if err != nil {
		t.Fatal(err)
	}
	if !mgr.ManagesAnyCredentials(managed) {
		t.Error("a project with no credentials_file still needs gcloud")
	}

	allFiles := &config.Config{Projects: []config.Project{
		{Name: "a", ProjectID: "a-1", CredentialsFile: "/creds/a.json"},
		{Name: "b", ProjectID: "b-1", CredentialsFile: "/creds/b.json"},
	}}
	allFiles.Defaults.CredentialDir = t.TempDir()
	mgr, err = NewManager(allFiles)
	if err != nil {
		t.Fatal(err)
	}
	if mgr.ManagesAnyCredentials(allFiles) {
		t.Error("no project needs a login, so gcloud is not required to start")
	}
}
