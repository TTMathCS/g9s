package ui

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

// --- the detail pane describes what it says it describes ---

func TestDetailPaneStaysOnItsOwnResourceWhileTheTableMoves(t *testing.T) {
	// The merged table grows as each kind lands, so a row that was third when
	// the pane opened is somewhere else moments later. Reading the row back out
	// of the table would leave the header naming one resource and the body
	// describing another.
	m := populatedModel(t)
	m.width, m.height = 132, 24
	m.kindIdx = m.allTabIdx()
	m.screen = screenResources
	m.cursor = 0

	opened, _ := m.handleResourcesKey(keyRunes("d"))
	m = opened.(Model)
	described := m.detailRes.Name

	if m.screen != screenDetail {
		t.Fatalf("d left the screen at %v", m.screen)
	}
	if !strings.Contains(m.detailView(), described) {
		t.Fatalf("the pane does not name the resource it opened on (%q)", described)
	}

	// A fetch for another kind lands and reshuffles the merged table.
	landed, _ := m.handleResources(resourcesMsg{
		project: m.active.Name,
		kind:    "gke",
		token:   m.refreshToken["gke"],
		result: gcp.Result{Resources: []gcp.Resource{
			{Name: "cluster-a", Location: "us-central1", Status: "RUNNING", Row: []string{"cluster-a", "us-central1", "Standard", "3", "1.29", "RUNNING", "5d"}},
		}},
	})
	m = landed.(Model)

	if m.detailRes.Name != described {
		t.Errorf("the pane switched resources under the user: %q became %q", described, m.detailRes.Name)
	}
	if !strings.Contains(m.detailView(), described) {
		t.Errorf("the pane header no longer names %q:\n%s", described, m.detailView())
	}
}

func TestDetailPaneRendersNothingWithoutAResource(t *testing.T) {
	m := populatedModel(t)
	m.width, m.height = 132, 24
	m.screen = screenDetail

	// A notice, not an empty body: an empty one collapses the layout and pulls
	// the footer up under the header.
	if got := m.detailView(); !strings.Contains(got, "no resource selected") {
		t.Errorf("detailView with no resource rendered %q", got)
	}
	// And yanking must not reach for a row that was never chosen.
	if _, cmd := m.handleDetailKey(keyRunes("y")); cmd != nil {
		t.Error("y with no described resource should do nothing")
	}
}

// --- a new identity invalidates the old one's data ---

func TestLoginSupersedesFetchesStartedWithTheOldIdentity(t *testing.T) {
	// Clearing the cache is not enough on its own: a fetch that started before
	// the login is still running, and its result would land in the freshly
	// cleared cache and be shown as belonging to the new identity.
	m := populatedModel(t)
	m.screen = screenResources

	inFlight := m.refreshToken["vm"]
	m.loading["vm"] = true

	after, _ := m.handleLoginFinished(loginFinishedMsg{project: "prod"})
	m = after.(Model)

	if len(m.cache) != 0 {
		t.Errorf("login left %d cached kinds", len(m.cache))
	}
	if m.refreshToken["vm"] == inFlight {
		t.Error("login did not supersede the in-flight fetch of vm")
	}

	// The old fetch comes back. It must be dropped, not cached.
	landed, _ := m.handleResources(resourcesMsg{
		project: "prod",
		kind:    "vm",
		token:   inFlight,
		result:  gcp.Result{Resources: []gcp.Resource{{Name: "stale-vm", Row: []string{"stale-vm"}}}},
	})
	m = landed.(Model)

	if _, cached := m.cache["vm"]; cached {
		t.Error("a result fetched with the previous identity was cached after login")
	}
}

func TestLoginFailureKeepsTheCache(t *testing.T) {
	// A login that failed changed nothing, so throwing away good data would
	// just cost a round of API calls.
	m := populatedModel(t)
	after, _ := m.handleLoginFinished(loginFinishedMsg{project: "prod", err: errTest{}})

	if len(after.(Model).cache) == 0 {
		t.Error("a failed login cleared the cache")
	}
}

// --- switching kinds ---

func TestSwitchingKindsClearsTheFilter(t *testing.T) {
	// A query typed against VM names matches nothing in DNS zones, and a table
	// that silently hides every row reads as an empty project. Every route into
	// a kind has to behave the same way here.
	base := populatedModel(t)
	base.screen = screenResources
	base.filter.SetValue("web")

	for _, tt := range []struct {
		name string
		key  string
	}{
		{"tab", "]"},
		{"shift+tab", "["},
		{"hotkey", kindKey(1)},
		{"merged view", "a"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			next, _ := base.handleResourcesKey(keyRunes(tt.key))
			m := next.(Model)
			if got := m.filter.Value(); got != "" {
				t.Errorf("filter survived the switch as %q", got)
			}
			if m.cursor != 0 {
				t.Errorf("cursor left at %d, want the top of the new table", m.cursor)
			}
		})
	}
}

func TestOpeningAKindLeavesTheDashboardCursorOnIt(t *testing.T) {
	// So esc from the table lands back on the category you were looking at.
	m := populatedModel(t)
	m.screen = screenResources
	m.kindIdx, m.ovCursor = 0, 0

	next, _ := m.handleResourcesKey(keyRunes(kindKey(3)))
	m = next.(Model)

	if m.ovCursor != 3 {
		t.Errorf("dashboard cursor at %d, want 3", m.ovCursor)
	}
}

// --- degenerate configuration ---

func TestProjectScreenSurvivesAnEmptyProjectList(t *testing.T) {
	// Load rejects an empty project list, but New is exported and nothing else
	// stops an empty config from indexing off the end of the slice.
	m := New(&config.Config{}, nil)
	m.width, m.height = 80, 24

	for _, key := range []string{"j", "k", "g", "G", "l", "L", "r", "enter"} {
		next, _ := m.handleProjectsKey(keyRunes(key))
		m = next.(Model)
	}
	if m.projCursor != 0 {
		t.Errorf("projCursor moved to %d with no projects", m.projCursor)
	}
	if view := m.View(); view == "" {
		t.Error("the project screen rendered nothing with an empty config")
	}
}

func TestSelectingAProjectWithoutCredentialsFlashes(t *testing.T) {
	m := New(&config.Config{Projects: []config.Project{{Name: "p", ProjectID: "p-1"}}}, nil)
	m.authStatus["p"] = auth.Status{State: auth.StateExpired}

	next, cmd := m.selectProject()
	if next.(Model).hasActive {
		t.Error("an expired project was selected anyway")
	}
	if cmd == nil {
		t.Error("no message explaining why nothing happened")
	}
}

// --- login flow ---

func TestLoginNoticeExplainsTheLoopbackRedirect(t *testing.T) {
	// The notice is the only thing that reaches the user while gcloud has the
	// terminal, and it exists for one failure: signed in, MFA passed, terminal
	// still sitting on the URL because the redirect never came back.
	p := config.Project{Name: "prod-data", ProjectID: "prod-1"}

	browser := loginNotice(p, false)
	if !strings.Contains(browser, "prod-data") {
		t.Errorf("notice does not name the project:\n%s", browser)
	}
	if !strings.Contains(browser, "http://localhost") {
		t.Errorf("notice does not say what has to be reachable:\n%s", browser)
	}
	if !strings.Contains(browser, "L") {
		t.Errorf("notice does not point at the way out:\n%s", browser)
	}

	// The no-browser flow has the opposite problem — it looks like nothing is
	// happening until you realise you are meant to run something elsewhere.
	noBrowser := loginNotice(p, true)
	if !strings.Contains(noBrowser, "--no-browser") {
		t.Errorf("notice does not name the flow:\n%s", noBrowser)
	}
	if strings.Contains(noBrowser, "http://localhost") {
		t.Errorf("the no-browser flow has no loopback redirect to explain:\n%s", noBrowser)
	}
}

func TestNoticeCmdWritesBeforeRunning(t *testing.T) {
	var out bytes.Buffer
	c := &noticeCmd{Cmd: exec.Command("true"), notice: "g9s: hello"}
	c.SetStdout(&out)
	c.SetStderr(&out)
	c.SetStdin(strings.NewReader(""))

	// `true` may not exist on every platform; the notice is what is under test.
	_ = c.Run()

	if !strings.Contains(out.String(), "g9s: hello") {
		t.Errorf("notice not written: %q", out.String())
	}
}

func TestNoticeCmdDoesNotOverrideStreamsAlreadySet(t *testing.T) {
	// Same contract as bubbletea's own wrapper: the program assigns the
	// terminal's streams, but only where the caller left them unset.
	var mine bytes.Buffer
	c := &noticeCmd{Cmd: exec.Command("true")}
	c.Stdout = &mine

	c.SetStdout(&bytes.Buffer{})
	if c.Stdout != io.Writer(&mine) {
		t.Error("SetStdout replaced a stream the caller had already chosen")
	}
}

func TestLoginNoBrowserSettingAlwaysWins(t *testing.T) {
	// The setting exists because the one thing g9s cannot detect is whether the
	// browser proxies localhost — only the person running it knows that, and
	// getting it wrong means an indefinite hang.
	cfg := &config.Config{
		Defaults: config.Defaults{LoginNoBrowser: true},
		Projects: []config.Project{{Name: "prod", ProjectID: "prod-1"}},
	}
	m := New(cfg, nil)

	if !m.cfg.Defaults.LoginNoBrowser {
		t.Fatal("the setting did not reach the model")
	}
	// The notice is the observable difference, and it must not talk about a
	// loopback redirect that this flow does not use.
	notice := loginNotice(cfg.Projects[0], true)
	if strings.Contains(notice, "localhost") {
		t.Errorf("the no-browser notice mentions the loopback redirect:\n%s", notice)
	}
	if !strings.Contains(notice, "--no-browser") {
		t.Errorf("the notice does not name the flow:\n%s", notice)
	}
}

func TestBrowserNoticePointsAtTheSetting(t *testing.T) {
	// Someone who hits the hang once should not have to hit it twice.
	notice := loginNotice(config.Project{Name: "p"}, false)
	if !strings.Contains(notice, "login_no_browser") {
		t.Errorf("the browser notice does not mention the setting:\n%s", notice)
	}
}
