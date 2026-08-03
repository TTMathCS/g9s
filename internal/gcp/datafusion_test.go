package gcp

import (
	"testing"

	datafusion "google.golang.org/api/datafusion/v1"
)

func fusionRow(i *datafusion.Instance) Resource {
	return dataFusionResource(testProject(), i)
}

func TestDataFusionResourceShape(t *testing.T) {
	r := fusionRow(testDataFusionInstance())

	if r.Name != "etl-fusion" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "us-central1" {
		t.Errorf("Location = %q", r.Location)
	}
	if r.Row[2] != "enterprise" {
		t.Errorf("edition cell = %q", r.Row[2])
	}
	if r.Row[3] != "6.10.1" {
		t.Errorf("version cell = %q", r.Row[3])
	}
	if r.Row[4] != "private" {
		t.Errorf("private cell = %q", r.Row[4])
	}
	if r.Status != "RUNNING" {
		t.Errorf("Status = %q", r.Status)
	}
}

// TestDeveloperEditionIsFlagged: it has no SLA and is not meant to carry
// production pipelines, and it reports the same RUNNING as an Enterprise
// instance costing far more.
func TestDeveloperEditionIsFlagged(t *testing.T) {
	i := testDataFusionInstance()
	i.Type = "DEVELOPER"

	r := fusionRow(i)
	if r.Status != "DEVELOPER_EDITION" {
		t.Errorf("Status = %q, want the edition to outrank RUNNING", r.Status)
	}
	if r.Row[5] != "RUNNING" {
		t.Errorf("state cell = %q, want the instance's own state kept", r.Row[5])
	}
}

func TestInstanceNotRunningIsNotAnEditionFinding(t *testing.T) {
	// A stopped or failed instance has a more urgent thing to say, and it is
	// also not costing the same as a running one.
	i := testDataFusionInstance()
	i.Type = "DEVELOPER"
	i.State = "FAILED"

	if got := fusionRow(i).Status; got != "FAILED" {
		t.Errorf("Status = %q, want the state to win", got)
	}
}

// TestPublicInstanceIsVisibleOnTheRow: a non-private instance has an endpoint
// reachable from outside the VPC, which is otherwise three clicks into the
// network config.
func TestPublicInstanceIsVisibleOnTheRow(t *testing.T) {
	i := testDataFusionInstance()
	i.PrivateInstance = false

	if got := fusionRow(i).Row[4]; got != "public" {
		t.Errorf("private cell = %q, want public", got)
	}
}

func TestDataFusionEditions(t *testing.T) {
	tests := map[string]string{
		"BASIC":            "basic",
		"ENTERPRISE":       "enterprise",
		"DEVELOPER":        "developer",
		"TYPE_UNSPECIFIED": "-",
		"":                 "-",
		// A new edition is worth showing rather than hiding.
		"SOMETHING_NEW": "something_new",
	}
	for typ, want := range tests {
		if got := dataFusionEdition(&datafusion.Instance{Type: typ}); got != want {
			t.Errorf("dataFusionEdition(%q) = %q, want %q", typ, got, want)
		}
	}
}

func TestDataFusionStateAndVersionFallbacks(t *testing.T) {
	for _, state := range []string{"", "STATE_UNSPECIFIED"} {
		if got := dataFusionState(&datafusion.Instance{State: state}); got != "UNKNOWN" {
			t.Errorf("dataFusionState(%q) = %q, want UNKNOWN", state, got)
		}
	}
	if got := dataFusionVersion(&datafusion.Instance{}); got != "-" {
		t.Errorf("dataFusionVersion = %q, want a dash", got)
	}
}

func TestDataFusionInstancesAreNotSSHOrAirflowTargets(t *testing.T) {
	// Data Fusion has a service endpoint and runs Dataproc underneath, so both
	// guards are worth pinning.
	r := fusionRow(testDataFusionInstance())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a Data Fusion instance is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a Data Fusion instance has an Airflow URI")
	}
}
