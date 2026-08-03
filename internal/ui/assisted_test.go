package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
)

// keysFor types a string into the model one key at a time.
func typeInto(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	return m
}

// The login screen exists to take a pasted URL, and URLs contain the
// characters the rest of the UI treats as commands: `:` opens the command
// line, `?` opens help, `/` opens the filter. Any of those firing mid-paste
// destroys the paste.
func TestLoginScreenSwallowsGlobalKeys(t *testing.T) {
	cfg := &config.Config{Projects: []config.Project{{Name: "sandbox", ProjectID: "s-1"}}}
	m := New(cfg, nil)
	m.screen = screenLogin
	m.loginProject = "sandbox"
	m.loginInput.Focus()

	m = typeInto(t, m, "http://localhost:8085/?state=x")

	if m.commanding {
		t.Error("the `:` in the pasted URL opened the command line")
	}
	if m.screen != screenLogin {
		t.Errorf("a global key moved the screen to %v mid-paste", m.screen)
	}
	if got := m.loginInput.Value(); got != "http://localhost:8085/?state=x" {
		t.Errorf("input = %q — characters were stolen from the paste", got)
	}
}

func TestCancelledLoginFlashesInsteadOfFailing(t *testing.T) {
	cfg := &config.Config{Projects: []config.Project{{Name: "sandbox", ProjectID: "s-1"}}}
	m := New(cfg, nil)
	m.screen = screenLogin
	m.loginReturn = screenProjects
	m.loginProject = "sandbox"

	next, cmd := m.handleLoginFinished(loginFinishedMsg{
		project:   "sandbox",
		err:       errors.New("signal: killed"),
		cancelled: true,
	})
	m = next.(Model)

	if m.screen != screenProjects {
		t.Errorf("screen = %v, want the screen the login started from", m.screen)
	}
	if cmd == nil {
		t.Fatal("no acknowledgement of the cancel")
	}
	msg, ok := cmd().(flashMsg)
	if !ok {
		t.Fatalf("cancel produced %T, want a flash — the failure pane would dress a decision up as a problem", cmd())
	}
	if !strings.Contains(msg.text, "cancelled") {
		t.Errorf("flash = %q", msg.text)
	}
}

func TestAssistedStartFailureFallsBackToTheTerminalFlow(t *testing.T) {
	// A gcloud too old for --no-launch-browser, or output the scanner does not
	// recognise, must not strand the login: the terminal-handover flow needs
	// neither and still works.
	cfg := &config.Config{
		Defaults: config.Defaults{CredentialDir: t.TempDir(), GcloudPath: "gcloud"},
		Projects: []config.Project{{Name: "sandbox", ProjectID: "s-1"}},
	}
	mgr, err := auth.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := New(cfg, mgr)
	m.screen = screenLogin
	m.loginReturn = screenProjects
	m.loginProject = "sandbox"

	next, cmd := m.handleAssistedLogin(assistedLoginMsg{
		project: "sandbox",
		err:     errors.New("gcloud did not print a login URL within 30s"),
	})
	m = next.(Model)

	if m.screen != screenProjects {
		t.Errorf("screen = %v, want restored before the handover", m.screen)
	}
	if cmd == nil {
		t.Fatal("no fallback command — the login is stranded")
	}
}

func TestStaleAssistedStartIsDroppedNotAdopted(t *testing.T) {
	// The user pressed esc while gcloud was starting, or started again. The
	// start that then lands belongs to nobody, and adopting it would open a
	// browser with no screen explaining it. A stale *failure* is the sharper
	// case: falling back would suspend the whole TUI into a terminal handover
	// for an attempt the user already walked away from.
	cfg := &config.Config{Projects: []config.Project{{Name: "sandbox", ProjectID: "s-1"}}}
	m := New(cfg, nil)
	m.screen = screenProjects // the user already left the login screen
	m.loginSeq = 2

	next, cmd := m.handleAssistedLogin(assistedLoginMsg{
		project: "sandbox",
		seq:     1, // an older attempt
		err:     errors.New("gcloud did not print a login URL within 30s"),
	})
	m = next.(Model)

	if cmd != nil {
		t.Fatal("a stale start produced a command; it must be dropped silently")
	}
	if m.screen != screenProjects {
		t.Errorf("screen = %v, a stale start must not move the screen", m.screen)
	}
}

// The fallback has to be a flow that can finish. When the assisted start
// fails on a host whose browser proxies localhost, handing over to the
// ordinary browser flow returns the user to the original bug: the sign-in
// succeeds, the redirect never arrives, and gcloud waits forever with no way
// out but ctrl+c. The --no-browser flow needs another machine, but it ends.
func TestFallbackAvoidsTheFlowThatHangsBehindAProxy(t *testing.T) {
	cfg := &config.Config{Projects: []config.Project{{Name: "sandbox", ProjectID: "s-1"}}}
	m := New(cfg, nil)
	p := cfg.Projects[0]

	t.Run("proxy that does not exempt loopback", func(t *testing.T) {
		t.Setenv("HTTPS_PROXY", "http://proxy.corp:8080")
		t.Setenv("https_proxy", "http://proxy.corp:8080")
		t.Setenv("NO_PROXY", "")
		t.Setenv("no_proxy", "")

		if !auth.ProxyMayBlockLoopback() {
			t.Fatal("setup: the proxy hint did not fire, so this proves nothing")
		}
		if !m.fallbackNoBrowser(p) {
			t.Error("fell back to the browser flow on a host where its last step cannot complete")
		}
	})

	t.Run("loopback exempted", func(t *testing.T) {
		t.Setenv("HTTPS_PROXY", "http://proxy.corp:8080")
		t.Setenv("https_proxy", "http://proxy.corp:8080")
		t.Setenv("NO_PROXY", "localhost,127.0.0.1,::1")
		t.Setenv("no_proxy", "localhost,127.0.0.1,::1")

		// A proxy that exempts loopback breaks nothing, and forcing the
		// --no-browser flow there would demand a second machine for no reason.
		if m.fallbackNoBrowser(p) {
			t.Error("forced the no-browser flow on a host whose loopback is exempt")
		}
	})
}
