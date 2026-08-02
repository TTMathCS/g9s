// Package auth manages per-project GCP credentials.
//
// Every project gets its own gcloud configuration directory, so credentials
// for ten projects never share mutable global state and switching projects is
// just a matter of pointing at a different directory. The password itself
// never passes through g9s: `gcloud auth application-default login` owns the
// terminal during login and the browser handles the IdP prompt and MFA.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// adcFile is the filename gcloud uses for application default credentials
// inside a CLOUDSDK_CONFIG directory.
const adcFile = "application_default_credentials.json"

// scope is what the client libraries need. Application default credentials
// minted by gcloud carry cloud-platform, so this only affects the token check.
const scope = "https://www.googleapis.com/auth/cloud-platform"

// Manager resolves credentials for projects.
type Manager struct {
	credentialRoot string
	gcloudPath     string
	// credentialFiles holds, per project name, an existing credentials file to
	// read instead of the directory g9s would log into. Empty for the projects
	// g9s manages itself, which is the default.
	credentialFiles map[string]string
}

func NewManager(cfg *config.Config) (*Manager, error) {
	// Sanitizing project names for the filesystem can collapse two distinct
	// names into one directory ("prod/data" and "prod-data" both become
	// "prod-data"), and sharing a credential dir means logging into one
	// project silently re-identifies the other. Refuse up front.
	//
	// The comparison is case-insensitive because the filesystem may be: macOS
	// and Windows both treat "Prod" and "prod" as one directory, so a check
	// that only caught exact collisions would pass on Linux and hand two
	// projects the same credentials on a laptop.
	seen := map[string]string{}
	for _, p := range cfg.Projects {
		dir := sanitize(p.Name)
		key := strings.ToLower(dir)
		if other, dup := seen[key]; dup {
			return nil, fmt.Errorf(
				"projects %q and %q would share the credential directory %q "+
					"(names that differ only in case collide on macOS and Windows) — rename one",
				other, p.Name, dir)
		}
		seen[key] = p.Name
	}

	files := map[string]string{}
	for _, p := range cfg.Projects {
		if path := cfg.CredentialsFile(p); path != "" {
			files[p.Name] = path
		}
	}

	return &Manager{
		credentialRoot:  cfg.Defaults.CredentialDir,
		gcloudPath:      cfg.Defaults.GcloudPath,
		credentialFiles: files,
	}, nil
}

// State describes whether a project is usable right now.
type State int

const (
	// StateUnknown means the check has not run yet.
	StateUnknown State = iota
	// StateMissing means the project has never been logged in on this machine.
	StateMissing
	// StateExpired means credentials exist but no longer mint a token. With a
	// federated IdP this is the normal daily case: the session policy expired
	// and the PAM password has to be checked out again.
	StateExpired
	// StateValid means a token was minted successfully.
	StateValid
	// StateWrongAccount means the credential is live but belongs to a
	// different identity from the account configured for this project.
	StateWrongAccount
)

func (s State) String() string {
	switch s {
	case StateMissing:
		return "not logged in"
	case StateExpired:
		return "expired"
	case StateValid:
		return "ok"
	case StateWrongAccount:
		return "wrong account"
	default:
		return "unknown"
	}
}

// Status is the result of checking a project's credentials.
type Status struct {
	State State
	// ExpectedAccount is the identity configured for this project, when one
	// was specified. It is kept beside Account so a mismatch can name both.
	ExpectedAccount string
	// Account is the identity recorded in the credential file, when present.
	Account string
	// Expiry is when the current access token stops working. The refresh
	// token's own lifetime is set by the IdP session policy and is not
	// discoverable from the file, so this is a floor, not a guarantee.
	Expiry time.Time
	// Err carries the underlying failure for StateExpired.
	Err error
	// CheckedAt lets callers cache the result.
	CheckedAt time.Time
}

func (s Status) Valid() bool { return s.State == StateValid }

// Summary renders the status for a footer or picker row.
func (s Status) Summary() string {
	withAccount := func(summary string) string {
		if s.Account == "" {
			return summary
		}
		return summary + " · " + s.Account
	}
	switch s.State {
	case StateValid:
		if s.Expiry.IsZero() {
			return withAccount("ok")
		}
		mins := int(time.Until(s.Expiry).Round(time.Minute).Minutes())
		switch {
		case mins < 0:
			return withAccount("ok")
		case mins < 60:
			return withAccount(fmt.Sprintf("ok (token %dm)", mins))
		default:
			// "1h05m" reads better in a header than time.Duration's "1h5m0s".
			return withAccount(fmt.Sprintf("ok (token %dh%02dm)", mins/60, mins%60))
		}
	case StateWrongAccount:
		if s.Account == "" {
			return "wrong account — press l to re-login"
		}
		if s.ExpectedAccount == "" {
			return "wrong account: " + s.Account
		}
		return fmt.Sprintf("wrong account: %s (want %s) — press l", s.Account, s.ExpectedAccount)
	case StateExpired:
		return withAccount("expired — press l to re-login")
	case StateMissing:
		return "not logged in — press l"
	default:
		return "checking…"
	}
}

// ConfigDir is the CLOUDSDK_CONFIG directory for a project.
func (m *Manager) ConfigDir(p config.Project) string {
	return filepath.Join(m.credentialRoot, sanitize(p.Name))
}

// ADCPath is the credentials file g9s reads for a project.
//
// A configured credentials_file wins: everything downstream — the validity
// check and the client options every lister uses — goes through here, so
// pointing at an existing file is all it takes for g9s to use credentials it
// did not create.
func (m *Manager) ADCPath(p config.Project) string {
	if path := m.credentialFiles[p.Name]; path != "" {
		return path
	}
	return filepath.Join(m.ConfigDir(p), adcFile)
}

// ManagesCredentials reports whether g9s is the one that logs this project in.
//
// False when a credentials_file is configured. g9s then only reads that file,
// and refreshing it is the user's business — running gcloud against an
// isolated config directory would write somewhere the file is not.
func (m *Manager) ManagesCredentials(p config.Project) bool {
	return m.credentialFiles[p.Name] == ""
}

// ClientOptions are the options every GCP client for this project must use.
//
// WithQuotaProject matters: application default credentials minted from a user
// account have no project of their own, and most APIs reject the call outright
// without a billing/quota project attached.
func (m *Manager) ClientOptions(p config.Project) []option.ClientOption {
	return []option.ClientOption{
		option.WithCredentialsFile(m.ADCPath(p)),
		option.WithQuotaProject(p.ProjectID),
	}
}

// Check reports whether the project's credentials can currently mint a token.
//
// This deliberately performs a live token exchange rather than reading an
// expiry field. A refresh token that the IdP has invalidated looks perfectly
// healthy on disk; the only honest test is to use it.
func (m *Manager) Check(ctx context.Context, p config.Project) Status {
	status := Status{CheckedAt: time.Now(), ExpectedAccount: p.Account}

	path := m.ADCPath(p)
	raw, err := os.ReadFile(path)
	if err != nil {
		status.State = StateMissing
		if !errors.Is(err, os.ErrNotExist) {
			status.Err = err
		}
		return status
	}

	status.Account = accountFromADC(raw)

	// The token source captures this context, so the timeout has to be in
	// place before the credentials are built for it to bound the exchange.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	creds, err := google.CredentialsFromJSON(ctx, raw, scope)
	if err != nil {
		status.State = StateExpired
		status.Err = fmt.Errorf("reading %s: %w", path, err)
		return status
	}

	token, err := creds.TokenSource.Token()
	if err != nil {
		status.State = StateExpired
		status.Err = err
		return status
	}

	status.Expiry = token.Expiry
	status.State = identityState(p.Account, status.Account)
	return status
}

// identityState refuses a live credential for the wrong configured account.
// If either side is unknown there is nothing honest to compare, so a token
// that successfully minted remains valid.
func identityState(expected, actual string) State {
	if expected != "" && actual != "" && !strings.EqualFold(expected, actual) {
		return StateWrongAccount
	}
	return StateValid
}

// LoopbackUsable reports whether the browser gcloud sends you to could reach a
// server running on this machine.
//
// This decides whether the ordinary login can finish at all. gcloud's flow
// starts an HTTP server on 127.0.0.1 and points the browser at Google with
// redirect_uri=http://localhost:<port>/; signing in only completes when the
// browser's request for that URL arrives back here. Over SSH it arrives at the
// laptop you are sitting at instead, and gcloud waits forever with the sign-in
// already done — which reads as a hang with no explanation, because from the
// browser's side everything worked.
//
// X or Wayland forwarding is the exception worth encoding: the browser process
// still runs on this machine even though its pixels do not, so the redirect
// comes back here as normal.
func LoopbackUsable() bool { return loopbackUsable(runtime.GOOS, os.Getenv) }

func loopbackUsable(goos string, env func(string) string) bool {
	remote := env("SSH_CONNECTION") != "" || env("SSH_CLIENT") != "" || env("SSH_TTY") != ""
	if !remote {
		return true
	}
	switch goos {
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly":
		return env("DISPLAY") != "" || env("WAYLAND_DISPLAY") != ""
	default:
		// A macOS or Windows box reached over SSH has no session to open a
		// browser into.
		return false
	}
}

// ProxyMayBlockLoopback reports whether an HTTP proxy is configured that does
// not exempt loopback addresses.
//
// The other way the redirect goes missing: a browser told to send everything
// through a corporate proxy will send http://localhost:<port>/ there too, and
// the proxy cannot route it back to this machine. The browser's proxy settings
// are what actually matter and g9s cannot read them, but a proxy in this
// environment with no loopback exemption is a strong hint that the browser is
// configured the same way — enough to name the likely cause instead of leaving
// the user staring at a URL.
func ProxyMayBlockLoopback() bool { return proxyMayBlockLoopback(os.Getenv) }

func proxyMayBlockLoopback(env func(string) string) bool {
	proxied := false
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if env(key) != "" {
			proxied = true
			break
		}
	}
	if !proxied {
		return false
	}
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		if exemptsLoopback(env(key)) {
			return false
		}
	}
	return true
}

func exemptsLoopback(list string) bool {
	for _, entry := range strings.Split(list, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		entry = strings.TrimPrefix(entry, ".")
		switch entry {
		case "*", "localhost", "127.0.0.1", "127.0.0.0/8", "::1", "[::1]":
			return true
		}
	}
	return false
}

// LoginCmd builds the interactive login command for a project.
//
// The caller is expected to hand the terminal to this process (bubbletea's
// ExecProcess does exactly that) so gcloud can print its URL, and so the user
// can paste the authorization response back. g9s never sees the password.
func (m *Manager) LoginCmd(p config.Project, noBrowser bool) (*exec.Cmd, error) {
	dir := m.ConfigDir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating credential dir: %w", err)
	}

	args := []string{"auth", "application-default", "login"}
	if p.Account != "" {
		args = append(args, "--account="+p.Account)
	}
	if noBrowser {
		// Terminal is not on the machine with the browser. gcloud prints a
		// bootstrap command to run on a trusted machine that has one.
		args = append(args, "--no-browser")
	}

	cmd := exec.Command(m.gcloudPath, args...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_CONFIG="+dir,
		// Without this gcloud writes ADC without a quota project and every
		// subsequent API call fails with a confusing 403.
		"GOOGLE_CLOUD_QUOTA_PROJECT="+p.ProjectID,
	)
	return cmd, nil
}

// GcloudCmd builds an arbitrary gcloud invocation scoped to a project's
// credentials, for actions like `compute ssh`.
func (m *Manager) GcloudCmd(p config.Project, args ...string) *exec.Cmd {
	full := append([]string{"--project=" + p.ProjectID}, args...)
	if p.Account != "" {
		full = append([]string{"--account=" + p.Account}, full...)
	}
	cmd := exec.Command(m.gcloudPath, full...)
	cmd.Env = append(os.Environ(), "CLOUDSDK_CONFIG="+m.ConfigDir(p))
	return cmd
}

// ManagesAnyCredentials reports whether any project needs g9s to log it in.
//
// False when every project reads a credentials_file, which is the setup for a
// machine where the login handshake cannot complete. gcloud is then not needed
// to start — only for SSH, which says so when it is used.
func (m *Manager) ManagesAnyCredentials(cfg *config.Config) bool {
	for _, p := range cfg.Projects {
		if m.ManagesCredentials(p) {
			return true
		}
	}
	return false
}

// Available reports whether the configured gcloud binary can be found. Checked
// at startup so the failure is a clear message rather than a login that dies.
func (m *Manager) Available() error {
	if _, err := exec.LookPath(m.gcloudPath); err != nil {
		return fmt.Errorf("gcloud not found at %q: %w", m.gcloudPath, err)
	}
	return nil
}

// accountFromADC pulls the account out of a credential file when gcloud
// recorded one. Best effort: the field is absent in some credential types.
func accountFromADC(raw []byte) string {
	var doc struct {
		Account     string `json:"account"`
		ClientEmail string `json:"client_email"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	if doc.Account != "" {
		return doc.Account
	}
	return doc.ClientEmail
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// sanitize makes a project name safe to use as a directory component.
//
// Names come from a hand-edited config file, so a path separator or a ".."
// must not survive into the credential path in any form.
func sanitize(name string) string {
	segments := unsafeChars.Split(name, -1)

	kept := make([]string, 0, len(segments))
	for _, s := range segments {
		// Drop empty and dot-only segments: they carry no information and
		// read as traversal.
		if s == "" || strings.Trim(s, ".") == "" {
			continue
		}
		kept = append(kept, s)
	}

	cleaned := strings.Trim(strings.Join(kept, "-"), "-")
	if cleaned == "" {
		return "project"
	}
	return cleaned
}
