package auth

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TTMathCS/g9s/internal/config"
)

// --- URL scanning ---

// The output the scanner has to find the URL in, verbatim from gcloud.
const noLaunchBrowserBanner = `Go to the following link in your browser, and complete the sign-in prompts:

    https://accounts.google.com/o/oauth2/auth?response_type=code&client_id=764086051850.apps.googleusercontent.com&redirect_uri=http%3A%2F%2Flocalhost%3A8085%2F&scope=openid&state=S&code_challenge=C&code_challenge_method=S256

`

func TestFindAuthURLReadsGcloudsBanner(t *testing.T) {
	rawURL, port, ok := findAuthURL(noLaunchBrowserBanner)
	if !ok {
		t.Fatal("the authorization URL was not found in gcloud's own banner")
	}
	if port != "8085" {
		t.Errorf("port = %q, want 8085 (from the percent-encoded redirect_uri)", port)
	}
	if !strings.HasPrefix(rawURL, "https://accounts.google.com/o/oauth2/auth?") {
		t.Errorf("url = %q", rawURL)
	}
}

func TestFindAuthURLIgnoresURLsWithoutALoopbackRedirect(t *testing.T) {
	// Documentation links appear in gcloud output too; none of them carries a
	// loopback redirect_uri, which is the structural mark of the right one.
	text := "See https://cloud.google.com/docs/authentication for details.\n"
	if _, _, ok := findAuthURL(text); ok {
		t.Error("a documentation link was mistaken for the authorization URL")
	}
}

func TestFindAuthURLWaitsForATerminatedURL(t *testing.T) {
	// A pipe can hand over the banner cut anywhere, including mid-URL. Half a
	// URL must not match — the port could be missing its last digit.
	cut := strings.Index(noLaunchBrowserBanner, "8085")
	if _, _, ok := findAuthURL(noLaunchBrowserBanner[:cut+2]); ok {
		t.Error("an unterminated URL matched; it may still be arriving")
	}
}

func TestScannerFindsAURLSplitAcrossWrites(t *testing.T) {
	var (
		mu    sync.Mutex
		found string
		port  string
	)
	s := &authURLScanner{
		tail: NewTailBuffer(1 << 16),
		found: func(u, p string) {
			mu.Lock()
			defer mu.Unlock()
			found, port = u, p
		},
	}

	// Three-byte chunks: crueller than any real pipe.
	for i := 0; i < len(noLaunchBrowserBanner); i += 3 {
		end := min(i+3, len(noLaunchBrowserBanner))
		if _, err := s.Write([]byte(noLaunchBrowserBanner[i:end])); err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if found == "" {
		t.Fatal("URL not found when delivered in fragments")
	}
	if port != "8085" {
		t.Errorf("port = %q", port)
	}
}

// --- Deliver validation ---

func stuckLogin(port string) *AssistedLogin {
	return &AssistedLogin{
		project: "sandbox",
		port:    port,
		tail:    NewTailBuffer(1 << 10),
		done:    make(chan struct{}),
	}
}

func TestDeliverRejectsEverythingButTheStuckTabsAddress(t *testing.T) {
	a := stuckLogin("8085")

	tests := []struct {
		name   string
		pasted string
		want   string // fragment the error must contain, to prove it names the actual mistake
	}{
		{"empty", "", "paste the full address"},
		{"the sign-in link pasted back", "https://accounts.google.com/o/oauth2/auth?redirect_uri=x", "AFTER signing in"},
		{"https scheme", "https://localhost:8085/?code=abc", "http://localhost"},
		{"not loopback", "http://internal.corp.example:8085/?code=abc", "http://localhost"},
		{"wrong port", "http://localhost:9999/?state=s&code=abc", "port"},
		{"no code in the query", "http://localhost:8085/", "FULL address"},
		{"prose instead of a URL", "Error 400: invalid_request", "http://localhost"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := a.Deliver(tc.pasted)
			if err == nil {
				t.Fatalf("Deliver(%q) succeeded; it must refuse to send this anywhere", tc.pasted)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name the mistake (want %q in it)", err, tc.want)
			}
		})
	}
}

func TestDeliverPerformsTheLoopbackRequestTheBrowserCouldNot(t *testing.T) {
	received := make(chan *url.URL, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.URL
	}))
	defer srv.Close()

	port := srv.Listener.Addr().(*net.TCPAddr).Port
	a := stuckLogin(fmt.Sprint(port))

	// The point of the whole flow: a proxy configured in the environment must
	// not see this request. The unroutable proxy address proves it — going
	// through it would fail, and going direct succeeds.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")

	pasted := fmt.Sprintf("http://localhost:%d/?state=fake-state&code=4/0AbCdEf", port)
	if err := a.Deliver(pasted); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	select {
	case got := <-received:
		if got.Query().Get("code") != "4/0AbCdEf" {
			t.Errorf("listener received code %q", got.Query().Get("code"))
		}
		if got.Query().Get("state") != "fake-state" {
			t.Errorf("listener received state %q — the state token must arrive intact for gcloud to accept it", got.Query().Get("state"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the loopback listener never received the request")
	}
}

// --- end to end against a fake gcloud ---

var (
	fakeGcloudOnce sync.Once
	fakeGcloudPath string
	fakeGcloudErr  error
)

// buildFakeGcloud compiles the fixture once per test run.
func buildFakeGcloud(t *testing.T) string {
	t.Helper()
	fakeGcloudOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fakegcloud")
		if err != nil {
			fakeGcloudErr = err
			return
		}
		bin := filepath.Join(dir, "gcloud")
		out, err := exec.Command("go", "build", "-o", bin, "./testdata/fakegcloud").CombinedOutput()
		if err != nil {
			fakeGcloudErr = fmt.Errorf("building fake gcloud: %v\n%s", err, out)
			return
		}
		fakeGcloudPath = bin
	})
	if fakeGcloudErr != nil {
		t.Fatal(fakeGcloudErr)
	}
	return fakeGcloudPath
}

func assistedManager(t *testing.T) (*Manager, config.Project) {
	t.Helper()
	cfg := &config.Config{
		Defaults: config.Defaults{
			CredentialDir: t.TempDir(),
			GcloudPath:    buildFakeGcloud(t),
		},
		Projects: []config.Project{{Name: "sandbox", ProjectID: "my-sandbox"}},
	}
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return mgr, cfg.Projects[0]
}

// The complete corporate-laptop rescue, no browser involved: start the login,
// take the URL gcloud printed, build the redirect address a stuck browser tab
// would show, paste it back, and watch gcloud exit successfully.
func TestAssistedLoginEndToEnd(t *testing.T) {
	mgr, p := assistedManager(t)

	a, err := mgr.StartAssistedLogin(p)
	if err != nil {
		t.Fatalf("StartAssistedLogin: %v", err)
	}
	defer a.Cancel()

	authURL, err := url.Parse(a.URL())
	if err != nil {
		t.Fatalf("gcloud's URL does not parse: %v", err)
	}
	state := authURL.Query().Get("state")

	// What the browser's address bar shows when the loopback fetch fails.
	pasted := fmt.Sprintf("http://localhost:%s/?state=%s&code=4/0AfakeCode", a.port, state)
	if err := a.Deliver(pasted); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("gcloud did not exit after the code was delivered")
	}
	if err := a.Err(); err != nil {
		t.Fatalf("gcloud exited with %v\noutput:\n%s", err, a.Output())
	}
	if a.Cancelled() {
		t.Error("a completed login reports itself cancelled")
	}
}

func TestAssistedLoginCancelKillsGcloud(t *testing.T) {
	mgr, p := assistedManager(t)

	a, err := mgr.StartAssistedLogin(p)
	if err != nil {
		t.Fatal(err)
	}
	a.Cancel()

	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("gcloud still running after Cancel")
	}
	if !a.Cancelled() {
		t.Error("Cancelled() = false after Cancel()")
	}
}

func TestAssistedLoginReportsAGcloudThatDiesEarly(t *testing.T) {
	mgr, p := assistedManager(t)
	t.Setenv("FAKEGCLOUD_MODE", "no-url")

	_, err := mgr.StartAssistedLogin(p)
	if err == nil {
		t.Fatal("a gcloud that exits before printing a URL must fail the start")
	}
	// The error carries gcloud's own words so the fallback path can diagnose.
	if !strings.Contains(err.Error(), "something broke early") {
		t.Errorf("error does not carry gcloud's output: %v", err)
	}
}
