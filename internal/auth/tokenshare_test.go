package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/TTMathCS/g9s/internal/config"
)

// writeADC puts a syntactically valid authorized-user credential on disk. It
// will not mint a real token — no test here needs one — but it is enough for
// google.CredentialsFromJSON to build a source from it.
func writeADC(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{
		"type":          "authorized_user",
		"client_id":     "fake.apps.googleusercontent.com",
		"client_secret": "fake-secret",
		"refresh_token": "fake-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, adcFile), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func shareManager(t *testing.T) (*Manager, config.Project) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		Defaults: config.Defaults{CredentialDir: root, GcloudPath: "gcloud"},
		Projects: []config.Project{{Name: "sandbox", ProjectID: "my-sandbox"}},
	}
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Projects[0]
	writeADC(t, mgr.ConfigDir(p))
	return mgr, p
}

// Opening a project builds a client per lister — 43 of them. Before the shared
// source, each one read and parsed the ADC file again and ended up with its own
// token source that refreshed over the network on first use: one keypress
// became dozens of disk reads and dozens of concurrent token exchanges, which
// is slow anywhere and much worse through a proxy that terminates TLS.
func TestEveryClientSharesOneTokenSource(t *testing.T) {
	mgr, p := shareManager(t)

	first, ok := mgr.tokenSource(p)
	if !ok {
		t.Fatal("no token source built from a valid credential file")
	}

	for i := 0; i < 43; i++ {
		got, ok := mgr.tokenSource(p)
		if !ok {
			t.Fatalf("call %d built no source", i)
		}
		if got != first {
			t.Fatalf("call %d got a different token source; each client would refresh separately", i)
		}
	}
}

// ClientOptions is called from every lister goroutine at once.
func TestTokenSourceIsSafeUnderConcurrentClients(t *testing.T) {
	mgr, p := shareManager(t)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		sources []oauth2.TokenSource
	)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ts, ok := mgr.tokenSource(p)
			if !ok {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			sources = append(sources, ts)
		}()
	}
	wg.Wait()

	if len(sources) != 50 {
		t.Fatalf("got %d sources, want 50", len(sources))
	}
	for i, ts := range sources {
		if ts != sources[0] {
			t.Fatalf("goroutine %d got a different source; the cache raced", i)
		}
	}
}

// The cache turns a fixed credential into a stale one if a login does not drop
// it: the source built from the previous file contents would go on minting
// tokens for the identity the user just replaced.
func TestLoginInvalidatesTheCachedSource(t *testing.T) {
	mgr, p := shareManager(t)

	before, ok := mgr.tokenSource(p)
	if !ok {
		t.Fatal("no source built")
	}

	mgr.InvalidateCredentials(p.Name)

	after, ok := mgr.tokenSource(p)
	if !ok {
		t.Fatal("no source built after invalidation")
	}
	if after == before {
		t.Error("the cached source survived a login; clients would authenticate as the old identity")
	}
}

// A project with no credentials yet must not cache a nil source and then keep
// returning it once the user logs in.
func TestMissingCredentialsAreNotCachedAsAFailure(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Defaults: config.Defaults{CredentialDir: root, GcloudPath: "gcloud"},
		Projects: []config.Project{{Name: "fresh", ProjectID: "p-1"}},
	}
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Projects[0]

	if _, ok := mgr.tokenSource(p); ok {
		t.Fatal("a source was built with no credential file present")
	}

	// The login that follows writes the file; the next call has to see it.
	writeADC(t, mgr.ConfigDir(p))
	if _, ok := mgr.tokenSource(p); !ok {
		t.Error("a failed lookup was cached, so the project stays broken after logging in")
	}
}
