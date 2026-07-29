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
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		credentialRoot: cfg.Defaults.CredentialDir,
		gcloudPath:     cfg.Defaults.GcloudPath,
	}
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
)

func (s State) String() string {
	switch s {
	case StateMissing:
		return "not logged in"
	case StateExpired:
		return "expired"
	case StateValid:
		return "ok"
	default:
		return "unknown"
	}
}

// Status is the result of checking a project's credentials.
type Status struct {
	State State
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
	switch s.State {
	case StateValid:
		if s.Expiry.IsZero() {
			return "ok"
		}
		remaining := time.Until(s.Expiry).Round(time.Minute)
		if remaining < 0 {
			return "ok"
		}
		return fmt.Sprintf("ok (token %s)", remaining)
	case StateExpired:
		return "expired — press l to re-login"
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

// ADCPath is the application default credentials file for a project.
func (m *Manager) ADCPath(p config.Project) string {
	return filepath.Join(m.ConfigDir(p), adcFile)
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
	status := Status{CheckedAt: time.Now()}

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

	status.State = StateValid
	status.Expiry = token.Expiry
	return status
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
