package ui

import (
	"strings"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

func strp(s string) *string { return &s }

func instanceRow(name, status string) gcp.Resource {
	return gcp.Resource{
		Name: name, Location: "us-central1-a", Status: status,
		Row: []string{name, "us-central1-a", "n2-standard-8", "10.0.0.12", "—", status, "14d"},
		Raw: &computepb.Instance{
			Name:   strp(name),
			Zone:   strp("https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a"),
			Status: strp(status),
		},
	}
}

func actionModel(t *testing.T, projectName, projectID, status string) Model {
	t.Helper()
	cfg := &config.Config{Projects: []config.Project{{Name: projectName, ProjectID: projectID}}}
	m := New(cfg, nil)
	m.width, m.height = 100, 24
	m.active, m.hasActive = cfg.Projects[0], true
	m.screen = screenResources
	m.cache[m.currentKind().ID] = gcp.Result{Resources: []gcp.Resource{instanceRow("etl-worker-01", status)}}
	return m
}

func key(s string) tea.KeyMsg {
	if s == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	if s == "esc" {
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// The whole safety argument rests on this: nothing changes until the exact
// instance name has been typed. A confirmation that can be cleared by pressing
// enter is a confirmation in name only.
func TestDestructiveActionRefusesToRunWithoutTheTypedName(t *testing.T) {
	m := actionModel(t, "sandbox", "acme-sandbox-dev-1204", "RUNNING")

	next, _ := m.requestAction(gcp.ActionStopVM)
	m = next.(Model)
	if m.screen != screenConfirm || m.pending == nil {
		t.Fatal("stop did not open a confirmation")
	}

	// Enter on an empty prompt must not execute.
	next, cmd := m.handleConfirmKey(key("enter"))
	m = next.(Model)
	if m.pending == nil {
		t.Fatal("the pending action was cleared without the name being typed")
	}
	msg, ok := cmd().(flashMsg)
	if !ok || !strings.Contains(msg.text, "type the instance name") {
		t.Errorf("empty confirmation produced %#v, want a refusal", cmd())
	}

	// A near miss must not execute either.
	m.confirmInput.SetValue("etl-worker-0")
	next, _ = m.handleConfirmKey(key("enter"))
	if next.(Model).pending == nil {
		t.Error("a name one character short was accepted")
	}
}

func TestExactNameExecutesAndClosesTheConfirmation(t *testing.T) {
	m := actionModel(t, "sandbox", "acme-sandbox-dev-1204", "RUNNING")
	next, _ := m.requestAction(gcp.ActionStopVM)
	m = next.(Model)

	m.confirmInput.SetValue("etl-worker-01")
	next, cmd := m.handleConfirmKey(key("enter"))
	m = next.(Model)

	if m.pending != nil {
		t.Error("the pending action survived execution")
	}
	if m.screen != screenResources {
		t.Errorf("screen = %v, want the table it was started from", m.screen)
	}
	if cmd == nil {
		t.Error("confirming produced no command, so nothing would run")
	}
}

// esc has to work from every state of the prompt, including a fully typed one.
func TestEscapeAlwaysCancels(t *testing.T) {
	m := actionModel(t, "sandbox", "acme-sandbox-dev-1204", "RUNNING")
	next, _ := m.requestAction(gcp.ActionStopVM)
	m = next.(Model)
	m.confirmInput.SetValue("etl-worker-01")

	next, cmd := m.handleConfirmKey(key("esc"))
	m = next.(Model)
	if m.pending != nil {
		t.Error("esc left a pending action")
	}
	if m.screen != screenResources {
		t.Errorf("screen = %v after esc", m.screen)
	}
	msg, ok := cmd().(flashMsg)
	if !ok || !strings.Contains(msg.text, "unchanged") {
		t.Errorf("cancel flash = %#v, want it to say nothing happened", cmd())
	}
}

// A non-destructive action in a sandbox is the only case that gets the light
// confirmation, and it still needs a deliberate key.
func TestStartInSandboxUsesTheLightConfirmation(t *testing.T) {
	m := actionModel(t, "sandbox", "acme-sandbox-dev-1204", "TERMINATED")

	next, _ := m.requestAction(gcp.ActionStartVM)
	m = next.(Model)
	if m.pending == nil {
		t.Fatal("start did not open a confirmation")
	}
	if m.pending.typed {
		t.Error("starting a stopped sandbox instance demands a typed name; that is friction with no risk behind it")
	}

	// Any key that is not enter or esc must do nothing.
	next, _ = m.handleConfirmKey(key("x"))
	if next.(Model).pending == nil {
		t.Error("a stray key confirmed the action")
	}
}

// Production raises the bar for everything, including actions that are not
// destructive anywhere else.
func TestProductionForcesTypedConfirmationEvenForStart(t *testing.T) {
	m := actionModel(t, "prod-data", "acme-dataeng-prod-4471", "TERMINATED")

	next, _ := m.requestAction(gcp.ActionStartVM)
	m = next.(Model)
	if m.pending == nil {
		t.Fatal("no confirmation")
	}
	if !m.pending.typed {
		t.Error("a production action was confirmable with one keypress")
	}
	if !strings.Contains(next.(Model).View(), "PRODUCTION") {
		t.Error("the confirmation does not say it is production")
	}
}

// Offering an action a row cannot accept is an affordance that does nothing,
// and something that silently does nothing is indistinguishable from failure.
func TestActionsAreRefusedForTheWrongState(t *testing.T) {
	m := actionModel(t, "sandbox", "acme-sandbox-dev-1204", "TERMINATED")

	next, cmd := m.requestAction(gcp.ActionStopVM)
	if next.(Model).pending != nil {
		t.Fatal("stop opened a confirmation for an already-terminated instance")
	}
	msg, ok := cmd().(flashMsg)
	if !ok || !strings.Contains(msg.text, "does not apply") {
		t.Errorf("got %#v, want an explanation", cmd())
	}
}

func TestActionsOfferedMatchTheInstanceState(t *testing.T) {
	running := gcp.ActionsFor(instanceRow("a", "RUNNING"))
	if len(running) != 2 {
		t.Errorf("a running instance offers %d actions, want stop and reset", len(running))
	}
	for _, a := range running {
		if a.ID == gcp.ActionStartVM.ID {
			t.Error("start was offered for an instance that is already running")
		}
	}

	stopped := gcp.ActionsFor(instanceRow("a", "TERMINATED"))
	if len(stopped) != 1 || stopped[0].ID != gcp.ActionStartVM.ID {
		t.Errorf("a terminated instance offers %v, want start only", stopped)
	}

	// Mid-transition, nothing: another instruction to an instance already
	// moving is how you get a state nobody asked for.
	if got := gcp.ActionsFor(instanceRow("a", "STOPPING")); len(got) != 0 {
		t.Errorf("an instance in transition offers %v, want nothing", got)
	}

	// And nothing at all for rows that are not instances.
	if got := gcp.ActionsFor(gcp.Resource{Name: "a-bucket"}); len(got) != 0 {
		t.Errorf("a non-instance row offers %v", got)
	}
}

// Prefix matching reaches every other command in g9s. It must not reach these:
// `:st` resolving to stop is an unforgivable way to lose an instance.
func TestActionCommandsRequireTheWholeWord(t *testing.T) {
	for _, verb := range []string{"st", "sto", "res", "rese", "sta", "s"} {
		if _, ok := actionByCommand(verb); ok {
			t.Errorf("%q resolved to an action; only the exact word may", verb)
		}
	}
	for _, verb := range []string{"start", "stop", "reset"} {
		if _, ok := actionByCommand(verb); !ok {
			t.Errorf("%q did not resolve to an action", verb)
		}
	}
}

// The confirmation captures its target when it opens. The table underneath
// keeps refreshing, and re-reading the selection at execution time would mean
// confirming one instance and stopping whichever had moved into that row.
func TestTheConfirmedTargetSurvivesTheTableChangingUnderneath(t *testing.T) {
	m := actionModel(t, "sandbox", "acme-sandbox-dev-1204", "RUNNING")
	next, _ := m.requestAction(gcp.ActionStopVM)
	m = next.(Model)
	confirmed := m.pending.target.Name

	// A refresh lands, replacing the table with different instances.
	kindID := m.currentKind().ID
	landed, _ := m.handleResources(resourcesMsg{
		project: m.active.Name, kind: kindID, token: m.refreshToken[kindID],
		result: gcp.Result{Resources: []gcp.Resource{
			instanceRow("something-else-entirely", "RUNNING"),
		}},
	})
	m = landed.(Model)

	if m.pending == nil {
		t.Fatal("the refresh discarded the pending action")
	}
	if m.pending.target.Name != confirmed {
		t.Errorf("target moved from %q to %q while the prompt was open",
			confirmed, m.pending.target.Name)
	}
}
