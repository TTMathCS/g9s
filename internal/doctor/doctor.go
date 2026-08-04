// Package doctor checks a g9s setup without launching the TUI.
//
// It exists because the failures that stop someone using g9s happen before
// there is any UI to report them in — a gcloud that is not installed, a config
// the loader refuses, a credential that mints a token for the wrong identity,
// an API nobody enabled. In a TUI those arrive as a red row or a flash that is
// gone in five seconds. Here they arrive as a list, on a terminal that keeps
// scrollback, in a form that can be pasted into a ticket.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
)

// Level is how much attention a finding deserves.
type Level int

const (
	// LevelOK is a check that passed.
	LevelOK Level = iota
	// LevelWarn is something that will not stop g9s starting but will bite.
	LevelWarn
	// LevelFail is something that stops g9s working for at least one project.
	LevelFail
)

func (l Level) String() string {
	switch l {
	case LevelOK:
		return "ok"
	case LevelWarn:
		return "warn"
	default:
		return "FAIL"
	}
}

// Finding is one check's result.
type Finding struct {
	Level Level
	// Check names what was tested, in the imperative.
	Check string
	// Detail is what was found.
	Detail string
	// Remedy is what to do. Empty when the finding needs no action.
	Remedy string
}

// Report is everything Run found.
type Report struct {
	Findings []Finding
}

func (r *Report) add(level Level, check, detail, remedy string) {
	r.Findings = append(r.Findings, Finding{Level: level, Check: check, Detail: detail, Remedy: remedy})
}

// Failed reports whether anything needs fixing before g9s will work.
func (r *Report) Failed() bool {
	for _, f := range r.Findings {
		if f.Level == LevelFail {
			return true
		}
	}
	return false
}

// Counts tallies findings by level, for the summary line.
func (r *Report) Counts() (ok, warn, fail int) {
	for _, f := range r.Findings {
		switch f.Level {
		case LevelOK:
			ok++
		case LevelWarn:
			warn++
		default:
			fail++
		}
	}
	return ok, warn, fail
}

// Options controls how much Run does.
type Options struct {
	// ConfigPath is the config to check. Empty uses the default resolution.
	ConfigPath string
	// SkipNetwork omits the live token exchange, which is the only part that
	// leaves the machine. Useful on a locked-down host, or to get a fast
	// answer about config and gcloud alone.
	SkipNetwork bool
	// Timeout bounds the whole run.
	Timeout time.Duration
}

// Run performs every check and returns what it found.
//
// It never stops at the first failure. Someone running this has a broken setup
// and wants the whole list, not the first item of an unknown number.
func Run(ctx context.Context, opts Options) *Report {
	report := &Report{}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	path := opts.ConfigPath
	if path == "" {
		path = config.DefaultPath()
	}

	cfg := checkConfig(report, path)
	checkGcloud(report, cfg)
	checkEnvironment(report)

	if cfg == nil {
		// Everything below needs projects to iterate.
		return report
	}
	checkProjects(ctx, report, cfg, opts.SkipNetwork)
	return report
}

func checkConfig(report *Report, path string) *config.Config {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			report.add(LevelFail, "config file", path+" does not exist",
				"Run `g9s -init` to write a starter config, then add your projects.")
			return nil
		}
		report.add(LevelFail, "config file", fmt.Sprintf("%s: %v", path, err), "")
		return nil
	}

	cfg, err := config.Load(path)
	if err != nil {
		report.add(LevelFail, "config file", err.Error(),
			"Fix the config and run `g9s doctor` again. Unknown keys are errors on purpose, so a typo is named rather than ignored.")
		return nil
	}

	report.add(LevelOK, "config file", fmt.Sprintf("%s — %d project(s)", path, len(cfg.Projects)), "")
	return cfg
}

func checkGcloud(report *Report, cfg *config.Config) {
	gcloudPath := "gcloud"
	if cfg != nil && cfg.Defaults.GcloudPath != "" {
		gcloudPath = cfg.Defaults.GcloudPath
	}

	resolved, err := exec.LookPath(gcloudPath)
	if err != nil {
		report.add(LevelFail, "gcloud", fmt.Sprintf("%q not found on PATH", gcloudPath),
			"Install the gcloud CLI, or set defaults.gcloud_path to its full path. g9s needs it for login and SSH.")
		return
	}
	report.add(LevelOK, "gcloud", resolved, "")

	// Version matters for one specific reason worth naming.
	version, detail := gcloudVersion(gcloudPath)
	if version == "" {
		report.add(LevelWarn, "gcloud version", "could not be determined: "+detail,
			"Only this check is affected — g9s uses gcloud the same way regardless, and every other check here still applies. "+
				"Run `gcloud version` yourself to see the same output g9s got.")
		return
	}
	report.add(LevelOK, "gcloud version", version,
		"The --no-browser login flow needs 372.0.0 or newer on both machines.")
}

// gcloudVersion asks gcloud what version it is, and returns "" with a reason
// when it will not say.
//
// Two attempts, because the first one used to be a `--format=value(...)`
// projection naming a key with spaces in it — and gcloud's projection grammar
// splits on whitespace, so the key it was handed was not the key anybody
// meant. That failed on a perfectly healthy install and reported `exit status
// 1`, which is a warning nobody can act on.
//
// So: ask for JSON, whose keys are exactly the strings gcloud prints, and fall
// back to parsing the plain output that every version of gcloud has always
// printed. And keep stderr, because gcloud says why it is unhappy there and
// throwing it away is what made the original warning useless.
func gcloudVersion(gcloudPath string) (version, detail string) {
	jsonOut, jsonErr := runGcloud(gcloudPath, "version", "--format=json")
	if jsonErr == nil {
		if v := parseGcloudVersion(jsonOut); v != "" {
			return v, ""
		}
	}

	plainOut, plainErr := runGcloud(gcloudPath, "version")
	if plainErr == nil {
		if v := parseGcloudVersion(plainOut); v != "" {
			return v, ""
		}
	}

	switch {
	case plainErr != nil:
		return "", plainErr.Error()
	case jsonErr != nil:
		return "", jsonErr.Error()
	default:
		// Both ran and neither carried a version. Not a failure of gcloud, and
		// not something to imply is one.
		return "", "gcloud ran but its output carried no SDK version"
	}
}

// runGcloud runs gcloud and folds stderr into the error, since that is where
// gcloud explains itself.
func runGcloud(gcloudPath string, args ...string) ([]byte, error) {
	// Bounded: a gcloud waiting on a component update prompt or a wedged
	// network would otherwise hang `g9s doctor`, which is the one command
	// somebody runs precisely because something is already stuck.
	ctx, cancel := context.WithTimeout(context.Background(), gcloudVersionTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, gcloudPath, args...)
	// Nothing here is interactive, and a gcloud that decides to prompt should
	// get EOF and exit rather than block on a terminal g9s is not driving.
	cmd.Stdin = nil

	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("%w — %s", err, firstLine(msg))
		}
		return out, err
	}
	return out, nil
}

// gcloudVersionTimeout bounds the version check.
const gcloudVersionTimeout = 15 * time.Second

// parseGcloudVersion pulls the SDK version out of either shape of output.
//
// JSON first: `{"Google Cloud SDK": "458.0.0", …}`. Then the plain listing,
// whose first line has always been `Google Cloud SDK 458.0.0`. Both are
// handled here rather than at the call site so this is testable without a
// gcloud to run — which is the whole reason the original was wrong.
func parseGcloudVersion(out []byte) string {
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err == nil {
		if v, ok := doc[gcloudSDKKey].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		rest, found := strings.CutPrefix(line, gcloudSDKKey)
		if !found {
			continue
		}
		if v := strings.TrimSpace(rest); v != "" {
			return v
		}
	}
	return ""
}

// gcloudSDKKey is what gcloud calls the SDK itself, in both output shapes.
const gcloudSDKKey = "Google Cloud SDK"

// firstLine keeps a multi-line gcloud complaint to one report line.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

// checkEnvironment reports whether the browser login flow can complete here.
//
// The two conditions are reported as one finding on purpose. They fail the
// same flow for the same reason — the redirect to http://localhost never
// reaches this machine — and reporting them separately produced the one output
// this check must never produce: "ok, browser login" printed directly above a
// proxy warning, on a machine where browser login is exactly what hangs.
func checkEnvironment(report *Report) {
	proxied := auth.ProxyMayBlockLoopback()

	switch {
	case !auth.LoopbackUsable():
		report.add(LevelWarn, "browser login", "this looks like a remote session with no local browser",
			"The ordinary login would hang after sign-in, so g9s uses gcloud's --no-browser flow automatically here.")
	case proxied:
		report.add(LevelWarn, "browser login", "a proxy is configured here and does not exempt loopback",
			"Sign-in will appear to work and then hang: the last step is the browser fetching http://localhost:<port>/ on this machine, and a browser that proxies localhost never delivers it. Exempt localhost,127.0.0.1,::1 in the BROWSER's proxy settings — the shell's NO_PROXY does not affect it. Failing that, set defaults.login_no_browser: true and run the command it prints on a machine whose browser can reach its own localhost.")
	default:
		report.add(LevelOK, "browser login", "loopback redirect should reach this machine", "")
	}

	report.add(LevelOK, "platform", runtime.GOOS+"/"+runtime.GOARCH, "")
}

func checkProjects(ctx context.Context, report *Report, cfg *config.Config, skipNetwork bool) {
	mgr, err := auth.NewManager(cfg)
	if err != nil {
		report.add(LevelFail, "credentials", err.Error(), "")
		return
	}

	for _, p := range cfg.Projects {
		label := "project " + p.Name

		// A directory anyone else can write is a credential anyone else can
		// replace, and pre-existing directories are the ones never checked.
		//
		// Absence is deliberately not reported here. It means "not logged in",
		// which the identity check below says better — and saying it twice, once
		// as a path and once as a state, reads like two different problems.
		missingCredentialDir := false
		if mgr.ManagesCredentials(p) {
			dir := mgr.ConfigDir(p)
			switch info, statErr := os.Stat(dir); {
			case statErr != nil && os.IsNotExist(statErr):
				missingCredentialDir = true
			case statErr != nil:
				report.add(LevelWarn, label+" credentials", statErr.Error(), "")
			case info.Mode().Perm()&0o077 != 0:
				report.add(LevelFail, label+" credential directory",
					fmt.Sprintf("%s is mode %04o — readable or writable by others", dir, info.Mode().Perm()),
					"chmod 700 "+dir)
			}
		}

		if skipNetwork {
			// Without the token exchange, the directory is the only evidence
			// there is, so now it is worth saying out loud.
			if missingCredentialDir {
				report.add(LevelWarn, label, "not logged in ("+mgr.ConfigDir(p)+" does not exist)",
					"Start g9s, select "+p.Name+" and press l.")
			}
			continue
		}

		status := mgr.Check(ctx, p)
		switch status.State {
		case auth.StateValid:
			detail := "credentials valid"
			if status.Account != "" {
				detail += " as " + status.Account
			}
			if !status.Expiry.IsZero() {
				detail += fmt.Sprintf(" (token %s)", time.Until(status.Expiry).Round(time.Minute))
			}
			report.add(LevelOK, label, detail, "")
		case auth.StateWrongAccount:
			report.add(LevelFail, label,
				fmt.Sprintf("credentials are for %s but the config expects %s", status.Account, status.ExpectedAccount),
				"Log in again as the expected account, or correct `account:` for this project.")
		case auth.StateMissing:
			report.add(LevelWarn, label, "not logged in",
				"Start g9s, select "+p.Name+" and press l.")
		default:
			detail := "credentials will not mint a token"
			if status.Err != nil {
				detail += ": " + truncate(status.Err.Error(), 160)
			}
			report.add(LevelWarn, label, detail,
				"This is the normal daily state with a federated IdP. Press l in g9s to log in again.")
		}
	}
}

// Write renders a report.
func Write(w io.Writer, r *Report) {
	width := 0
	for _, f := range r.Findings {
		width = max(width, len(f.Check))
	}

	for _, f := range r.Findings {
		fmt.Fprintf(w, "  %-4s  %-*s  %s\n", f.Level, width, f.Check, f.Detail)
		if f.Remedy != "" && f.Level != LevelOK {
			for _, line := range wrapRemedy(f.Remedy, 76) {
				fmt.Fprintf(w, "        %-*s  %s\n", width, "", line)
			}
		}
	}

	ok, warn, fail := r.Counts()
	fmt.Fprintf(w, "\n%d ok, %d warning(s), %d failure(s)\n", ok, warn, fail)
	if fail == 0 && warn == 0 {
		fmt.Fprintln(w, "Setup looks good.")
	}
}

// wrapRemedy breaks a remedy onto lines that fit beside the check column.
func wrapRemedy(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}

	var (
		lines []string
		line  string
	)
	for _, w := range words {
		switch {
		case line == "":
			line = w
		case len(line)+1+len(w) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	return append(lines, line)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// DefaultConfigPath is re-exported so the command can name the file it checked
// even when the config failed to load.
func DefaultConfigPath() string { return filepath.Clean(config.DefaultPath()) }
