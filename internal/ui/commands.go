package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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
	token int
	// appendPage distinguishes "load more" from a refresh. A subsequent
	// object page extends the rows already visible; every ordinary listing
	// replaces its cached result.
	appendPage bool
	result     gcp.Result
	err        error
}

type loginFinishedMsg struct {
	project string
	err     error
	// output is what gcloud wrote before failing.
	//
	// It is captured rather than left on the terminal because the terminal is
	// exactly where it does not survive: gcloud prints the real reason, exits,
	// and bubbletea repaints over it within milliseconds of the resume. What
	// reaches the user is "exit status 1", which is unactionable — the failure
	// this exists for was reported as "it gives error missing params, I haven't
	// had a chance to troubleshoot".
	output string
	// cancelled marks a login the user ended on purpose, which deserves a
	// one-line acknowledgement rather than the failure pane.
	cancelled bool
}

// assistedLoginMsg is the assisted flow reporting that gcloud is up and has
// printed its authorization URL — or that it could not start. seq ties it to
// the attempt that started it, so a stale start cannot be adopted.
type assistedLoginMsg struct {
	project string
	seq     int
	login   *auth.AssistedLogin
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
	return listResourcePage(cfg, mgr, p, lister, token, false)
}

// listResourcePage is the shared fetch path for both a fresh listing and an
// explicit continuation page.
func listResourcePage(cfg *config.Config, mgr *auth.Manager, p config.Project, lister gcp.Lister, token int, appendPage bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Defaults.ListTimeout.Duration())
		defer cancel()

		result, err := lister.List(ctx, cfg, p, mgr.ClientOptions(p))
		// One place, so no lister has to remember and none can get it wrong.
		gcp.StampKind(&result, lister.Kind().ID)
		return resourcesMsg{
			project:    p.Name,
			kind:       lister.Kind().ID,
			token:      token,
			appendPage: appendPage,
			result:     result,
			err:        err,
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
	// The credential path is in the notice so the manual fallback names a real
	// destination rather than "wherever g9s keeps them".
	notice := loginNotice(p, noBrowser, mgr.ADCPath(p))
	// 16 KiB of tail is far more than any gcloud failure needs and still bounded.
	transcript := auth.NewTailBuffer(16 << 10)
	return tea.Exec(&noticeCmd{Cmd: cmd, notice: notice, transcript: transcript}, func(err error) tea.Msg {
		return loginFinishedMsg{project: p.Name, err: err, output: transcript.String()}
	})
}

// startAssisted launches the assisted browser login for a project.
//
// This replaces the terminal handover for the browser flow. The difference
// that matters: gcloud runs as a piped child, so the TUI stays alive to offer
// the rescue — pasting the localhost address the browser got stuck on — that
// a suspended UI cannot.
func startAssisted(mgr *auth.Manager, p config.Project, seq int) tea.Cmd {
	return func() tea.Msg {
		login, err := mgr.StartAssistedLogin(p)
		return assistedLoginMsg{project: p.Name, seq: seq, login: login, err: err}
	}
}

// awaitAssisted resolves when gcloud exits, however that happens: the browser
// completed the redirect itself, a pasted address was delivered, the flow
// failed, or the user cancelled.
func awaitAssisted(login *auth.AssistedLogin) tea.Cmd {
	return func() tea.Msg {
		<-login.Done()
		err := login.Err()
		if err == nil {
			// gcloud can exit 0 without writing a credential in odd corners;
			// the auth re-check that follows the success path catches that.
			return loginFinishedMsg{project: login.Project(), output: login.Output()}
		}
		return loginFinishedMsg{
			project:   login.Project(),
			err:       err,
			output:    login.Output(),
			cancelled: login.Cancelled(),
		}
	}
}

// deliverCode hands the pasted redirect address to gcloud's loopback listener.
//
// Success here is not success of the login — it only means the listener got
// the request. gcloud then exchanges the code, exits, and awaitAssisted
// reports the real outcome.
func deliverCode(login *auth.AssistedLogin, pasted string) tea.Cmd {
	return func() tea.Msg {
		if err := login.Deliver(pasted); err != nil {
			return flashMsg{text: err.Error(), level: flashError}
		}
		return flashMsg{text: "code delivered — gcloud is finishing the login", level: flashInfo}
	}
}

// loginNotice is printed above gcloud's own output, on the terminal gcloud is
// about to take over.
//
// It exists because of one specific failure: gcloud prints a URL, you sign in,
// MFA passes — and the terminal never moves. Nothing is wrong at Google's end.
// The last step of the flow is the browser fetching http://localhost:<port>/ to
// hand the authorization code back, and if the browser cannot reach this
// machine that request goes somewhere else and gcloud waits forever. From the
// browser's side it all worked, so there is nothing on screen to suggest what
// to do. This is the only moment that guidance is any use — a status line in a
// TUI that is currently suspended cannot deliver it.
func loginNotice(p config.Project, noBrowser bool, adcPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "g9s: gcloud auth application-default login for %s\n", p.Name)

	if noBrowser {
		// The trap this spells out: gcloud prints a *command* containing a URL,
		// and the URL on its own is not a valid authorization request — it has
		// no redirect_uri, because the gcloud on the other machine is what adds
		// one pointing at its own loopback. Opening it in a browser gets
		// "Error 400: invalid_request, missing required parameter:
		// redirect_uri", which reads like g9s produced a broken link.
		b.WriteString("     --no-browser flow. Run the WHOLE gcloud command printed below on a\n")
		b.WriteString("     machine that has a browser and gcloud (372.0.0+), then paste that\n")
		b.WriteString("     command's output back here.\n")
		b.WriteString("     Do not open the URL inside it in a browser — on its own it is missing\n")
		b.WriteString("     redirect_uri and Google answers 400 invalid_request.\n")
		if adcPath != "" {
			b.WriteString("     No gcloud on that machine, or its browser cannot reach its own\n")
			b.WriteString("     localhost either? Run a normal `gcloud auth application-default\n")
			b.WriteString("     login` there and copy the credentials file it writes to:\n")
			b.WriteString("       " + adcPath + "\n")
		}
		return b.String()
	}

	b.WriteString("     Your browser must be able to reach http://localhost on THIS machine —\n")
	b.WriteString("     that redirect is how the authorization code gets back to gcloud.\n")
	if auth.ProxyMayBlockLoopback() {
		b.WriteString("     A proxy is configured here and does not exempt loopback. If the browser\n")
		b.WriteString("     proxies localhost too, the code never arrives: add localhost,127.0.0.1\n")
		b.WriteString("     to its bypass list, or press L to log in without a browser.\n")
	} else {
		b.WriteString("     If it stays stuck after you have signed in, the redirect did not arrive:\n")
		b.WriteString("     ctrl+c, then press L to log in without a browser.\n")
	}
	b.WriteString("     Set defaults.login_no_browser: true to skip the browser flow for good.\n")
	return b.String()
}

// noticeCmd runs an exec.Cmd after printing a line to the terminal it is about
// to inherit.
//
// bubbletea's own ExecProcess wrapper has no hook for this, and the notice has
// to land on the real terminal rather than in the suspended UI, so this
// implements the same tiny interface and writes first. No shell is involved,
// same as everywhere else g9s runs a program.
type noticeCmd struct {
	*exec.Cmd
	notice string
	// transcript keeps a copy of what gcloud wrote while it owned the terminal,
	// so a failure can still be read after the UI has painted over it.
	transcript *auth.TailBuffer
}

func (c *noticeCmd) SetStdin(r io.Reader) {
	if c.Stdin == nil {
		c.Stdin = r
	}
}

func (c *noticeCmd) SetStdout(w io.Writer) {
	if c.Stdout == nil {
		c.Stdout = c.tee(w)
	}
}

func (c *noticeCmd) SetStderr(w io.Writer) {
	if c.Stderr == nil {
		c.Stderr = c.tee(w)
	}
}

// tee copies the stream into the transcript when there is one.
//
// The nil check is load-bearing rather than defensive: a noticeCmd is useful
// without a transcript, and io.MultiWriter over a nil buffer is a panic at the
// first byte gcloud writes — which would turn "your login failed" into "g9s
// crashed while your login failed".
func (c *noticeCmd) tee(w io.Writer) io.Writer {
	if c.transcript == nil {
		return w
	}
	return io.MultiWriter(w, c.transcript)
}

func (c *noticeCmd) Run() error {
	// Stdout is assigned by the program just before this runs.
	if c.notice != "" && c.Stdout != nil {
		fmt.Fprintln(c.Stdout, c.notice)
	}
	return c.Cmd.Run()
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

// defaultClipboardLimit is the largest OSC 52 escape sequence g9s will write
// when the config does not say otherwise.
//
// 8 KB because that is where a stock xterm stops, and being wrong in this
// direction costs a refusal the user can see and override, while being wrong
// in the other direction costs a clipboard that silently did not change.
// Modern terminals accept far more; `clipboard_limit` is how you say so.
const defaultClipboardLimit = 8 * 1024

// osc52Overhead is the length of the escape sequence around the payload:
// "\x1b]52;c;" is seven bytes and the "\x07" terminator is one.
const osc52Overhead = len("\x1b]52;c;") + len("\x07")

// clipboardLimit resolves the configured limit. Negative disables the check.
func (m Model) clipboardLimit() int {
	if n := m.cfg.Defaults.ClipboardLimit; n != 0 {
		return n
	}
	return defaultClipboardLimit
}

// copyToClipboard uses the OSC 52 terminal escape, which works over SSH and
// needs no platform clipboard binary.
//
// The size check measures the *encoded sequence*, not the text. Base64 is four
// bytes out for every three in, so a check against the raw length passes
// payloads a third larger than it thinks — which is how a copy that reports
// success reaches a terminal that drops it for being too long. The terminal
// never says it truncated, so this check is the only thing standing between
// the user and a clipboard they believe is full.
func copyToClipboard(text string, limit int) tea.Cmd {
	return func() tea.Msg {
		if text == "" {
			return flashMsg{text: "nothing to copy", level: flashWarn}
		}
		// The escape goes to stderr because bubbletea owns stdout. If stderr
		// has been redirected the sequence lands in a file, where it does
		// nothing at all — worth saying rather than claiming a copy.
		if !isTerminal(os.Stderr) {
			return flashMsg{text: "cannot copy: stderr is not a terminal", level: flashWarn}
		}

		encoded := base64.StdEncoding.EncodeToString([]byte(text))
		if refusal, tooBig := clipboardRefusal(text, encoded, limit); tooBig {
			return refusal
		}

		fmt.Fprintf(os.Stderr, "\x1b]52;c;%s\x07", encoded)
		return flashMsg{text: "copied: " + truncate(text, 50), level: flashInfo}
	}
}

// clipboardRefusal reports whether the escape sequence is too long for the
// terminal to accept, and what to say about it.
//
// Separate from copyToClipboard because it is the part worth testing and the
// rest is terminal I/O: under `go test` stderr is not a terminal, so the copy
// stops before it ever reaches a size check living inside that function.
func clipboardRefusal(text, encoded string, limit int) (flashMsg, bool) {
	sequence := len(encoded) + osc52Overhead
	if limit <= 0 || sequence <= limit {
		return flashMsg{}, false
	}
	return flashMsg{
		text: fmt.Sprintf(
			"too large to copy: %s needs a %s escape, limit %s — raise defaults.clipboard_limit if your terminal takes more",
			byteSize(len(text)), byteSize(sequence), byteSize(limit)),
		level: flashWarn,
	}, true
}

// byteSize renders a byte count the way the footer has room for.
func byteSize(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	return fmt.Sprintf("%.1fKB", float64(n)/1024)
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
