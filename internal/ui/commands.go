package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

// --- messages ---

type authCheckedMsg struct {
	project string
	status  auth.Status
}

type resourcesMsg struct {
	project string
	kind    string
	// token guards against a slow refresh overwriting a newer one.
	token  int
	result gcp.Result
	err    error
}

type loginFinishedMsg struct {
	project string
	err     error
}

type flashMsg struct {
	text  string
	level flashLevel
}

type clearFlashMsg struct{ id int }

type flashLevel int

const (
	flashInfo flashLevel = iota
	flashWarn
	flashError
)

// --- commands ---

// checkAuth verifies a project's credentials in the background.
func checkAuth(mgr *auth.Manager, p config.Project) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return authCheckedMsg{project: p.Name, status: mgr.Check(ctx, p)}
	}
}

// listResources fetches one resource kind for one project.
func listResources(cfg *config.Config, mgr *auth.Manager, p config.Project, lister gcp.Lister, token int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Defaults.ListTimeout.Duration())
		defer cancel()

		result, err := lister.List(ctx, cfg, p, mgr.ClientOptions(p))
		return resourcesMsg{
			project: p.Name,
			kind:    lister.Kind().ID,
			token:   token,
			result:  result,
			err:     err,
		}
	}
}

// login suspends the TUI and hands the terminal to gcloud.
//
// This is the whole reason the auth story is simple: gcloud owns stdin and
// stdout for the duration, so the browser handoff, the IdP password prompt and
// the MFA challenge all happen in gcloud's and the browser's own UI. g9s never
// touches a credential.
func login(mgr *auth.Manager, p config.Project, noBrowser bool) tea.Cmd {
	cmd, err := mgr.LoginCmd(p, noBrowser)
	if err != nil {
		return func() tea.Msg { return loginFinishedMsg{project: p.Name, err: err} }
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return loginFinishedMsg{project: p.Name, err: err}
	})
}

// sshTo suspends the TUI and opens an interactive SSH session to a VM.
func sshTo(mgr *auth.Manager, p config.Project, name, zone string) tea.Cmd {
	cmd := mgr.GcloudCmd(p, "compute", "ssh", name, "--zone="+zone)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return flashMsg{text: fmt.Sprintf("ssh %s: %v", name, err), level: flashError}
		}
		return flashMsg{text: "ssh session closed", level: flashInfo}
	})
}

// safeToOpen reports whether a URL is one we are willing to hand to the
// platform opener.
//
// Console links are built by g9s, but the Airflow URI comes back from the
// Composer API, and `open` on macOS will launch whatever application claims a
// scheme. Restricting to http(s) means a surprising value in an API response
// cannot turn `o` into "launch an arbitrary handler". Anything else is shown
// rather than opened, so the user can look at it and decide.
func safeToOpen(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.Host == "" {
		return false
	}
	// url.Parse rejects ASCII control characters but not DEL or the C1 range,
	// and this string is handed to a platform opener and echoed into the status
	// line. See sanitizeLine for why that matters.
	return strings.IndexFunc(raw, isControl) < 0
}

// openURL launches the system browser without blocking the TUI.
func openURL(target string) tea.Cmd {
	return func() tea.Msg {
		if target == "" {
			return flashMsg{text: "no URL for this resource", level: flashWarn}
		}
		if !safeToOpen(target) {
			return flashMsg{
				text:  "refusing to open non-http(s) URL: " + truncate(target, 60),
				level: flashWarn,
			}
		}

		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", target)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
		default:
			cmd = exec.Command("xdg-open", target)
		}
		// Detach: the browser outliving the TUI is the point.
		cmd.Stdout, cmd.Stderr = nil, nil
		if err := cmd.Start(); err != nil {
			return flashMsg{text: "could not open browser: " + err.Error(), level: flashError}
		}
		go cmd.Wait()
		return flashMsg{text: "opened " + truncate(target, 60), level: flashInfo}
	}
}

// maxClipboardBytes bounds an OSC 52 payload. Terminals cap the sequence — the
// limit is 8KB in a stock xterm — and drop anything longer without saying so.
// Refusing loudly beats reporting a copy that never reached the clipboard.
const maxClipboardBytes = 64 * 1024

// copyToClipboard uses the OSC 52 terminal escape, which works over SSH and
// needs no platform clipboard binary.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		if text == "" {
			return flashMsg{text: "nothing to copy", level: flashWarn}
		}
		if len(text) > maxClipboardBytes {
			return flashMsg{
				text:  fmt.Sprintf("too large to copy: %dKB, limit %dKB", len(text)/1024, maxClipboardBytes/1024),
				level: flashWarn,
			}
		}
		// The escape goes to stderr because bubbletea owns stdout. If stderr
		// has been redirected the sequence lands in a file, where it does
		// nothing at all — worth saying rather than claiming a copy.
		if !isTerminal(os.Stderr) {
			return flashMsg{text: "cannot copy: stderr is not a terminal", level: flashWarn}
		}

		encoded := base64.StdEncoding.EncodeToString([]byte(text))
		fmt.Fprintf(os.Stderr, "\x1b]52;c;%s\x07", encoded)
		return flashMsg{text: "copied: " + truncate(text, 50), level: flashInfo}
	}
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// flash shows a transient message and schedules its removal.
func flash(text string, level flashLevel) tea.Cmd {
	return func() tea.Msg { return flashMsg{text: text, level: level} }
}

func clearFlashAfter(id int, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return clearFlashMsg{id: id} })
}

// renderDetail converts a resource's raw API object into YAML, the way
// `gcloud describe` presents it, with secrets removed.
//
// Everything goes through JSON on the way to a generic tree — protojson for
// protobuf messages, encoding/json for the REST types — for two reasons. It
// gives the field names the API documents rather than the generated Go struct's
// internals, and it produces a tree that redactSecrets can walk.
func renderDetail(r gcp.Resource) string {
	doc, err := detailDocument(r.Raw)
	if err != nil {
		return "could not render resource: " + sanitizeLine(err.Error())
	}

	doc = redactSecrets(doc)

	out, err := yaml.Marshal(doc)
	if err != nil {
		return "could not render resource: " + sanitizeLine(err.Error())
	}
	// The values in here are API-supplied strings being written straight to the
	// terminal by the viewport. See sanitizeLine.
	return sanitizeBlock(string(out))
}

// detailDocument converts a raw API object into maps, slices and scalars.
func detailDocument(raw any) (any, error) {
	var (
		encoded []byte
		err     error
	)
	if msg, isProto := raw.(proto.Message); isProto {
		encoded, err = protojson.Marshal(msg)
	} else {
		encoded, err = json.Marshal(raw)
	}
	if err != nil {
		return nil, err
	}

	var doc any
	if err := json.Unmarshal(encoded, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// redactedMarker replaces a secret's value. Visible rather than silent: the
// field is there, and knowing a shared secret is set is part of reading a
// tunnel's configuration.
const redactedMarker = "«redacted by g9s»"

// secretFields are the field names whose values must not reach the screen.
//
// g9s is a read-only browser, but "read" is the whole problem: the API returns
// live secrets inside otherwise ordinary objects. Describing a VPN tunnel hands
// back its IPsec pre-shared key; describing a GKE cluster hands back the
// cluster's client private key and, where basic auth still exists, its
// password. Rendering those puts them in the terminal, in the scrollback, in
// whatever is capturing the session — and `y` copies them to the clipboard.
//
// Matching is on the exact field name, normalised for case and separators, not
// on substrings. A rule that redacted anything containing "password" would also
// blank out Cloud SQL's passwordValidationPolicy, which is configuration worth
// reading and holds no secret.
var secretFields = map[string]bool{
	"password":         true, // GKE basic auth, Cloud SQL root password
	"rootpassword":     true,
	"passphrase":       true,
	"clientkey":        true, // GKE client private key
	"privatekey":       true,
	"privatekeydata":   true,
	"privatekeybytes":  true,
	"sharedsecret":     true, // Cloud VPN IPsec pre-shared key
	"sharedsecrethash": true, // a hash of the same key, offline-crackable
	"secret":           true,
	"secretkey":        true,
	"clientsecret":     true,
	"apikey":           true,
	"accesstoken":      true,
	"refreshtoken":     true,
	"idtoken":          true,
	"token":            true,
	"credentials":      true,
	"authorization":    true,
}

// redactSecrets walks a decoded API object and replaces the value of every
// secret-bearing field, however deeply nested.
func redactSecrets(node any) any {
	switch v := node.(type) {
	case map[string]any:
		for key, value := range v {
			if secretFields[normalizeFieldName(key)] {
				// Only when something is actually set: marking an absent field
				// as redacted would misreport the resource.
				if !isEmptyValue(value) {
					v[key] = redactedMarker
				}
				continue
			}
			v[key] = redactSecrets(value)
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = redactSecrets(item)
		}
		return v
	default:
		return node
	}
}

// normalizeFieldName folds the spellings the same field has across APIs:
// shared_secret, sharedSecret and SharedSecret are one name.
func normalizeFieldName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range strings.ToLower(name) {
		if r == '_' || r == '-' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isEmptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	default:
		return false
	}
}
