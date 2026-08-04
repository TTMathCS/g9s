package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
	"github.com/TTMathCS/g9s/internal/tfstate"
)

func tfCfg() *config.Config {
	return &config.Config{
		Projects: []config.Project{
			{Name: "prod", ProjectID: "acme-prod-1",
				Terraform: config.Terraform{StateBucket: "acme-tfstate", StatePrefix: "prod"}},
			{Name: "nostate", ProjectID: "acme-nostate-1"},
		},
	}
}

func tfIndex(t *testing.T, doc string) *tfstate.Index {
	t.Helper()
	idx, err := tfstate.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return idx
}

// tfModel puts the overlay on a table of rows, as though `:tf` had been run and
// the state had come back.
func tfModel(t *testing.T, kindID string, rows []gcp.Resource, msg tfFinishedMsg) Model {
	t.Helper()
	m := New(tfCfg(), nil)
	m.width, m.height = 100, 24
	m.active, m.hasActive = m.cfg.Projects[0], true

	idx, ok := m.kindIndex(kindID)
	if !ok {
		t.Fatalf("no kind %q", kindID)
	}
	m.kindIdx = idx

	_, mapped := gcp.TerraformTypesFor(kindID)
	m.tf = &tfState{kind: m.listers[idx].Kind(), loading: true, mapped: mapped}
	m.tfRows = rows
	m.screen = screenTerraform

	next, _ := m.handleTerraformFinished(msg)
	return next.(Model)
}

func tfRow(name string) gcp.Resource {
	return gcp.Resource{Name: name, Location: "us-central1-a", Status: "RUNNING",
		Row: []string{name, "us-central1-a", "RUNNING"}}
}

func TestTheOverlayMarksEachRowManagedOrNot(t *testing.T) {
	idx := tfIndex(t, `{"version":4,"resources":[
	  {"mode":"managed","type":"google_compute_instance","name":"api",
	   "instances":[{"attributes":{"name":"api-01"}}]}]}`)

	m := tfModel(t, "vm", []gcp.Resource{tfRow("api-01"), tfRow("hand-made")},
		tfFinishedMsg{index: idx, objects: []string{"prod/default.tfstate"}})

	rows := m.visibleTerraformRows()
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Row[3] != "MANAGED" {
		t.Errorf("api-01 reads %q, want MANAGED", rows[0].Row[3])
	}
	if rows[1].Row[3] != "UNMANAGED" {
		t.Errorf("hand-made reads %q, want UNMANAGED", rows[1].Row[3])
	}

	// The resource's own state stays on the row: the verdict is an extra
	// column, not a replacement for what the table was already showing.
	if rows[0].Row[2] != "RUNNING" {
		t.Errorf("the overlay overwrote the resource's own status: %q", rows[0].Row[2])
	}
}

// The single most dangerous thing this table can do is tell somebody a
// resource is not in Terraform when g9s simply does not know the type name.
func TestAnUnmappedKindNeverSaysUnmanaged(t *testing.T) {
	kindID := ""
	for _, l := range gcp.Listers() {
		if _, mapped := gcp.TerraformTypesFor(l.Kind().ID); !mapped {
			kindID = l.Kind().ID
			break
		}
	}
	if kindID == "" {
		t.Skip("every kind is mapped")
	}

	m := tfModel(t, kindID, []gcp.Resource{tfRow("something")},
		tfFinishedMsg{index: tfIndex(t, `{"version":4,"resources":[]}`)})

	rows := m.visibleTerraformRows()
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].Row[3] == "UNMANAGED" {
		t.Error("an unmapped kind reported its rows as unmanaged")
	}

	// And the screen says why, rather than leaving a column of question marks
	// to be puzzled over.
	view := m.View()
	if !strings.Contains(view, "no Terraform type") {
		t.Errorf("the screen does not explain the question marks:\n%s", view)
	}
}

// A table of UNMANAGED rows most often means the wrong state file was read, so
// which one was read is on screen beside the verdict.
func TestTheSummaryNamesTheStateItRead(t *testing.T) {
	idx := tfIndex(t, `{"version":4,"resources":[
	  {"mode":"managed","type":"google_compute_instance","name":"a",
	   "instances":[{"attributes":{"name":"api-01"}}]}]}`)

	m := tfModel(t, "vm", []gcp.Resource{tfRow("api-01"), tfRow("other")},
		tfFinishedMsg{index: idx, objects: []string{"prod/default.tfstate"}})

	summary := m.terraformSummary()
	if !strings.Contains(summary, "1 of 2 managed") {
		t.Errorf("summary = %q, want the count", summary)
	}
	if !strings.Contains(summary, "prod/default.tfstate") {
		t.Errorf("summary = %q, want the state file it read", summary)
	}
}

// A state file that failed to load makes everything it manages look
// unmanaged. That is never a footnote.
func TestAStateFileThatCouldNotBeReadIsOnScreen(t *testing.T) {
	m := tfModel(t, "vm", []gcp.Resource{tfRow("api-01")}, tfFinishedMsg{
		index:   tfIndex(t, `{"version":4,"resources":[]}`),
		objects: []string{"prod/default.tfstate"},
		warnings: []gcp.Warning{{
			Scope: "gs://acme-tfstate/prod/other.tfstate", Reason: gcp.ReasonDenied,
			Detail: "permission denied",
		}},
	})

	view := m.View()
	if !strings.Contains(view, "other.tfstate") {
		t.Errorf("the unreadable state file is not named:\n%s", view)
	}
}

// Reading the state failing outright must not leave a table of UNMANAGED rows
// that looks like an answer.
func TestAFailedLoadShowsTheErrorRatherThanATable(t *testing.T) {
	m := tfModel(t, "vm", []gcp.Resource{tfRow("api-01")},
		tfFinishedMsg{err: errors.New("no readable state under gs://acme-tfstate/prod")})

	view := m.View()
	if !strings.Contains(view, "no readable state") {
		t.Errorf("the failure is not on screen:\n%s", view)
	}
	if strings.Contains(view, "UNMANAGED") {
		t.Errorf("a failed state read produced verdicts anyway:\n%s", view)
	}
}

// A project with no state bucket configured gets told so, rather than a screen
// that loads forever or an empty overlay.
func TestAProjectWithNoStateBucketIsToldSo(t *testing.T) {
	m := New(tfCfg(), nil)
	m.width, m.height = 100, 24
	m.active, m.hasActive = m.cfg.Projects[1], true // nostate
	m.screen = screenResources

	next, cmd := m.runCommand("tf")
	if next.(Model).screen == screenTerraform {
		t.Error("the overlay opened for a project with no state bucket")
	}
	msg, ok := cmd().(flashMsg)
	if !ok || !strings.Contains(msg.text, "state_bucket") {
		t.Errorf("the message does not say what to set: %#v", cmd())
	}
}

// The overlay marks the rows that were on screen when it was opened. A refresh
// landing underneath must not change which rows the verdict applies to.
func TestTheOverlayKeepsTheRowsItWasOpenedOn(t *testing.T) {
	idx := tfIndex(t, `{"version":4,"resources":[]}`)
	rows := []gcp.Resource{tfRow("api-01")}
	m := tfModel(t, "vm", rows, tfFinishedMsg{index: idx, objects: []string{"s.tfstate"}})

	// The underlying cache gains a row. The overlay's table does not.
	m.cache["vm"] = gcp.Result{Resources: []gcp.Resource{tfRow("api-01"), tfRow("api-02")}}
	m.cacheGen++

	if got := len(m.visibleTerraformRows()); got != 1 {
		t.Errorf("the overlay grew to %d rows after a refresh underneath it", got)
	}
}

// Leaving cancels, for the same reason the fleet sweep does.
func TestLeavingTheOverlayCancels(t *testing.T) {
	m := tfModel(t, "vm", []gcp.Resource{tfRow("api-01")},
		tfFinishedMsg{index: tfIndex(t, `{"version":4,"resources":[]}`)})
	cancelled := false
	m.tf.cancel = func() { cancelled = true }
	m.tfReturn = screenResources

	next, _ := m.handleTerraformKey(key("esc"))
	m = next.(Model)

	if !cancelled {
		t.Error("leaving the overlay did not cancel the read")
	}
	if m.tf != nil || m.tfRows != nil {
		t.Error("the overlay state survived leaving")
	}
	if m.screen != screenResources {
		t.Errorf("screen = %v, want the table it was opened from", m.screen)
	}
}

// A superseded read landing late must not replace the verdicts on screen.
func TestASupersededStateReadIsDropped(t *testing.T) {
	idx := tfIndex(t, `{"version":4,"resources":[
	  {"mode":"managed","type":"google_compute_instance","name":"a",
	   "instances":[{"attributes":{"name":"api-01"}}]}]}`)
	m := tfModel(t, "vm", []gcp.Resource{tfRow("api-01")},
		tfFinishedMsg{index: idx, objects: []string{"s.tfstate"}})
	m.tf.token = 7

	next, _ := m.handleTerraformFinished(tfFinishedMsg{token: 3, err: errors.New("stale")})
	m = next.(Model)

	if m.tf.err != nil {
		t.Error("a stale state read replaced the overlay that was on screen")
	}
	if m.visibleTerraformRows()[0].Row[3] != "MANAGED" {
		t.Error("a stale state read changed the verdicts")
	}
}
