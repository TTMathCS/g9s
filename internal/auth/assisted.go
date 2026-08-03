package auth

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/TTMathCS/g9s/internal/config"
)

// AssistedLogin is gcloud's browser login run as a piped child instead of a
// terminal handover, so the flow can be rescued when the browser cannot reach
// loopback.
//
// This exists for one machine: a corporate laptop whose browser sends
// http://localhost through the proxy. gcloud's flow ends with the browser
// fetching http://localhost:<port>/ to hand the authorization code back; a
// proxied browser never delivers it, the sign-in succeeds, and gcloud waits
// forever. The code is not lost — it is sitting in the address bar of the
// browser tab that failed to load. Everything after that is local: the user
// pastes that address into g9s, and Deliver performs the loopback request the
// browser could not, with a client that never uses a proxy.
//
// The security posture is unchanged. gcloud still runs the OAuth flow and
// still holds the PKCE code verifier; the authorization code that passes
// through g9s is single-use and useless without that verifier. g9s sees no
// password, no token, and writes no credential — it forwards one local HTTP
// request that the browser was supposed to make.
type AssistedLogin struct {
	project string
	cmd     *exec.Cmd
	tail    *TailBuffer

	// url is the authorization link gcloud printed; port is the loopback port
	// its redirect_uri points at, which is where gcloud is listening.
	url  string
	port string

	mu        sync.Mutex
	cancelled bool
	waitErr   error

	done chan struct{}
}

// StartAssistedLogin launches gcloud and returns once it has printed the
// authorization URL.
//
// --no-launch-browser rather than the plain flow: the plain flow opens the
// browser itself from inside gcloud, and capturing the URL requires it to be
// printed. The listener behaviour is identical — gcloud still serves the
// loopback redirect — so when the browser can reach localhost the login
// completes with no help, exactly like the flow it replaces.
func (m *Manager) StartAssistedLogin(p config.Project) (*AssistedLogin, error) {
	dir := m.ConfigDir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating credential dir: %w", err)
	}

	args := []string{"auth", "application-default", "login", "--no-launch-browser"}
	if p.Account != "" {
		args = append(args, "--account="+p.Account)
	}

	cmd := exec.Command(m.gcloudPath, args...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_CONFIG="+dir,
		"GOOGLE_CLOUD_QUOTA_PROJECT="+p.ProjectID,
	)

	a := &AssistedLogin{
		project: p.Name,
		cmd:     cmd,
		tail:    NewTailBuffer(16 << 10),
		done:    make(chan struct{}),
	}

	ready := make(chan struct{})
	scanner := &authURLScanner{
		tail: a.tail,
		found: func(rawURL, port string) {
			a.url, a.port = rawURL, port
			close(ready)
		},
	}
	// Both streams: gcloud has moved this banner between them across versions.
	cmd.Stdout = scanner
	cmd.Stderr = scanner

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		err := cmd.Wait()
		a.mu.Lock()
		a.waitErr = err
		a.mu.Unlock()
		close(a.done)
	}()

	// The URL appears within a second or two; a generous bound only decides
	// how long a broken gcloud takes to report as broken.
	select {
	case <-ready:
		return a, nil
	case <-a.done:
		return nil, fmt.Errorf("gcloud exited before printing a login URL: %w\n%s", a.Err(), a.Output())
	case <-time.After(30 * time.Second):
		a.Cancel()
		return nil, errors.New("gcloud did not print a login URL within 30s")
	}
}

// StubAssistedLogin builds an AssistedLogin that can only render: it carries a
// URL and port but runs no process. It exists for fixtures — the login screen
// in the generated README screenshots, and UI tests that need the screen
// populated without a gcloud. Deliver on a stub knocks on whatever the port
// says, so fixtures should use a port nothing listens on.
func StubAssistedLogin(project, rawURL, port string) *AssistedLogin {
	return &AssistedLogin{
		project: project,
		url:     rawURL,
		port:    port,
		tail:    NewTailBuffer(1 << 10),
		done:    make(chan struct{}),
	}
}

// Project names the project this login is for.
func (a *AssistedLogin) Project() string { return a.project }

// URL is the authorization link to open in a browser.
func (a *AssistedLogin) URL() string { return a.url }

// Done is closed when gcloud exits, successfully or not.
func (a *AssistedLogin) Done() <-chan struct{} { return a.done }

// Err is how gcloud exited. Only meaningful after Done is closed.
func (a *AssistedLogin) Err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.waitErr
}

// Output is the tail of everything gcloud wrote.
func (a *AssistedLogin) Output() string { return a.tail.String() }

// Cancelled reports whether Cancel ended this login.
func (a *AssistedLogin) Cancelled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cancelled
}

// Cancel kills gcloud. Done closes shortly after.
func (a *AssistedLogin) Cancel() {
	a.mu.Lock()
	a.cancelled = true
	proc := a.cmd.Process
	a.mu.Unlock()
	if proc != nil {
		proc.Kill()
	}
}

// Deliver performs the loopback request the browser could not.
//
// pasted is the address bar of the browser tab that got stuck — the full
// http://localhost:<port>/?state=...&code=... redirect. Everything about it is
// validated against what gcloud itself announced before a single byte is sent:
// the scheme must be plain http, the host must be a loopback name, and the
// port must be the one from gcloud's own redirect_uri. The request is then
// pinned to 127.0.0.1 with a client that has no proxy and follows no
// redirects, so a hostile paste cannot turn this into a request to anywhere
// else. The worst a bad paste can do is knock on gcloud's own listener, which
// rejects a wrong state token itself.
func (a *AssistedLogin) Deliver(pasted string) error {
	s := strings.TrimSpace(pasted)
	if s == "" {
		return errors.New("paste the full address from the browser's address bar")
	}

	// Anything that is not an absolute URL gets the same guidance, whether the
	// parser choked on it or parsed it into something hostless — someone who
	// pasted the error page's prose needs to know what to paste, not what Go's
	// URL parser thought of their paste.
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("that is not an address — copy everything in the stuck tab's address bar, starting with http://localhost")
	}

	// The natural mistake, caught by name: pasting the sign-in link back
	// instead of the address the browser ended at.
	host := strings.ToLower(u.Hostname())
	if strings.HasSuffix(host, "google.com") {
		return errors.New("that is the sign-in link itself — paste the localhost address the browser shows AFTER signing in")
	}
	if u.Scheme != "http" || (host != "localhost" && host != "127.0.0.1" && host != "::1") {
		return errors.New("the address to paste starts with http://localhost — copy the whole address bar from the stuck tab")
	}
	if port := u.Port(); port != a.port {
		return fmt.Errorf("gcloud is listening on port %s but this address points at port %s — copy the address from the tab from THIS login attempt", a.port, port)
	}
	q := u.Query()
	if q.Get("code") == "" && q.Get("error") == "" {
		return errors.New("this address carries no authorization code — copy the FULL address, including everything after the ?")
	}

	// Pinned to the literal loopback IP: "localhost" is resolved by whatever
	// the resolver says, and this request must only ever reach this machine.
	target := &url.URL{
		Scheme:   "http",
		Host:     net.JoinHostPort("127.0.0.1", a.port),
		Path:     u.Path,
		RawQuery: u.RawQuery,
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		// No proxy, ever — a proxy swallowing exactly this request is the
		// failure this whole type exists to route around.
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(target.String())
	if err != nil {
		return fmt.Errorf("could not hand the code to gcloud's listener: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return nil
}

// authURLScanner watches gcloud's output for the authorization URL.
//
// It keeps the unmatched text across writes because a pipe hands over
// arbitrary chunks — the URL can arrive split anywhere, including mid-escape.
type authURLScanner struct {
	tail *TailBuffer

	mu      sync.Mutex
	pending []byte
	matched bool
	found   func(rawURL, port string)
}

func (s *authURLScanner) Write(p []byte) (int, error) {
	s.tail.Write(p)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.matched {
		return len(p), nil
	}
	s.pending = append(s.pending, p...)
	if rawURL, port, ok := findAuthURL(string(s.pending)); ok {
		s.matched = true
		s.pending = nil
		s.found(rawURL, port)
		return len(p), nil
	}
	// Bounded for the same reason the tail is: only the part that could still
	// contain the URL's beginning is worth holding.
	if len(s.pending) > 64<<10 {
		s.pending = s.pending[len(s.pending)-64<<10:]
	}
	return len(p), nil
}

// findAuthURL picks the authorization URL out of gcloud's banner.
//
// The match is structural rather than textual: any https URL whose
// redirect_uri query parameter points at a loopback port is the one, whatever
// prose gcloud prints around it that week. A candidate with no terminator yet
// is left for the next write, since it may still be arriving.
func findAuthURL(text string) (rawURL, port string, ok bool) {
	rest := text
	for {
		start := strings.Index(rest, "https://")
		if start < 0 {
			return "", "", false
		}
		candidate := rest[start:]
		end := strings.IndexAny(candidate, " \t\r\n\"'<>")
		if end < 0 {
			// Possibly incomplete; wait for more output.
			return "", "", false
		}
		candidate = candidate[:end]

		if u, err := url.Parse(candidate); err == nil {
			if redirect, err := url.Parse(u.Query().Get("redirect_uri")); err == nil {
				host := strings.ToLower(redirect.Hostname())
				if (host == "localhost" || host == "127.0.0.1" || host == "::1") && redirect.Port() != "" {
					return candidate, redirect.Port(), true
				}
			}
		}
		rest = rest[start+len("https://"):]
	}
}

// TailBuffer keeps the tail of a stream.
//
// Bounded because an interactive gcloud session can print a lot and only the
// end is ever the reason it failed; unbounded capture would hold megabytes for
// a message that is a few lines long. Safe for concurrent writes because a
// command's stdout and stderr can be separate goroutines writing the same
// buffer.
type TailBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

// NewTailBuffer returns a buffer that keeps the last limit bytes written.
func NewTailBuffer(limit int) *TailBuffer {
	return &TailBuffer{limit: limit}
}

func (t *TailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	return len(p), nil
}

func (t *TailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
