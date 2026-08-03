package gcp

import (
	"strings"
	"testing"

	bigtableadmin "google.golang.org/api/bigtableadmin/v2"
)

func TestBigtableInstanceResourceShape(t *testing.T) {
	r := bigtableResource(testProject(), testBigtableInstance())

	if r.Name != "events-store" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Row[1] != "Events Store" {
		t.Errorf("display name cell = %q", r.Row[1])
	}
	if r.Row[2] != "production" {
		t.Errorf("type cell = %q", r.Row[2])
	}
	if r.Status != "READY" {
		t.Errorf("Status = %q", r.Status)
	}
	// An instance has no location of its own; its clusters do, and they can be
	// in different regions. Claiming one here would be a guess.
	if r.Location != "" {
		t.Errorf("Location = %q, want it left to the clusters", r.Location)
	}
}

// TestDevelopmentInstanceIsFlagged is the finding. A development instance runs
// on one node with no SLA and no replication, and reports the same READY as a
// production one.
func TestDevelopmentInstanceIsFlagged(t *testing.T) {
	i := testBigtableInstance()
	i.Type = "DEVELOPMENT"

	r := bigtableResource(testProject(), i)
	if r.Status != "DEVELOPMENT" {
		t.Errorf("Status = %q, want the instance type to outrank READY", r.Status)
	}
	if r.Row[4] != "READY" {
		t.Errorf("state cell = %q, want the instance's own state kept", r.Row[4])
	}
}

func TestInstanceBeingCreatedIsNotADevelopmentFinding(t *testing.T) {
	// Mid-create is a normal in-flight state, not a durability finding.
	i := testBigtableInstance()
	i.Type = "DEVELOPMENT"
	i.State = "CREATING"

	if got := bigtableResource(testProject(), i).Status; got != "CREATING" {
		t.Errorf("Status = %q, want the lifecycle state to win", got)
	}
}

func TestBigtableDisplayNameFallsBackToADash(t *testing.T) {
	// A display name identical to the id says nothing, and an empty column
	// reads as missing data rather than as an absent label.
	i := testBigtableInstance()
	i.DisplayName = "events-store"
	if got := bigtableResource(testProject(), i).Row[1]; got != "-" {
		t.Errorf("display name cell = %q, want a dash when it repeats the id", got)
	}

	i.DisplayName = ""
	if got := bigtableResource(testProject(), i).Row[1]; got != "-" {
		t.Errorf("display name cell = %q, want a dash", got)
	}
}

func TestBigtableStateFallbacks(t *testing.T) {
	for _, state := range []string{"", "STATE_NOT_KNOWN"} {
		if got := bigtableState(&bigtableadmin.Instance{State: state}); got != "UNKNOWN" {
			t.Errorf("bigtableState(%q) = %q, want UNKNOWN", state, got)
		}
	}
	for _, typ := range []string{"", "TYPE_UNSPECIFIED"} {
		if got := bigtableType(&bigtableadmin.Instance{Type: typ}); got != "-" {
			t.Errorf("bigtableType(%q) = %q, want a dash", typ, got)
		}
	}
}

// --- clusters drill-down ---

func btClusterRow(c *bigtableadmin.Cluster) Resource {
	return bigtableClusterResource(testProject(), "events-store", c)
}

func TestBigtableClusterResourceShape(t *testing.T) {
	r := btClusterRow(testBigtableCluster())

	if r.Name != "events-c1" {
		t.Errorf("Name = %q", r.Name)
	}
	// The location comes back as a full resource path.
	if r.Location != "us-central1-b" {
		t.Errorf("Location = %q, want the last segment", r.Location)
	}
	if r.Row[2] != "6" {
		t.Errorf("nodes cell = %q", r.Row[2])
	}
	if r.Row[4] != "SSD" {
		t.Errorf("storage cell = %q", r.Row[4])
	}
}

// TestNodeCountIsTheBill: Bigtable charges per node per hour regardless of
// throughput, so the node count is the cost and it is invisible on the
// instance row.
func TestNodeCountIsTheBill(t *testing.T) {
	c := testBigtableCluster()
	c.ServeNodes = 30

	if got := btClusterRow(c).Row[2]; got != "30" {
		t.Errorf("nodes cell = %q", got)
	}
}

func TestClusterAutoscalingChangesWhatNodesMeans(t *testing.T) {
	c := testBigtableCluster()
	if got := clusterAutoscaling(c); got != "off" {
		t.Errorf("autoscaling = %q, want off", got)
	}

	c.ClusterConfig = &bigtableadmin.ClusterConfig{
		ClusterAutoscalingConfig: &bigtableadmin.ClusterAutoscalingConfig{
			AutoscalingLimits: &bigtableadmin.AutoscalingLimits{MinServeNodes: 3, MaxServeNodes: 30},
		},
	}
	if got := clusterAutoscaling(c); got != "3–30 nodes" {
		t.Errorf("autoscaling = %q", got)
	}

	// Configured with no limits is still on, and a nil deref away.
	c.ClusterConfig.ClusterAutoscalingConfig.AutoscalingLimits = nil
	if got := clusterAutoscaling(c); got != "on" {
		t.Errorf("autoscaling = %q, want on", got)
	}
}

// TestStorageTypeIsPermanent: SSD and HDD differ roughly tenfold per gigabyte
// and cannot be changed after the cluster is created.
func TestStorageTypeIsPermanent(t *testing.T) {
	tests := map[string]string{"SSD": "SSD", "HDD": "HDD", "": "-", "STORAGE_TYPE_UNSPECIFIED": "-"}
	for storage, want := range tests {
		got := clusterStorage(&bigtableadmin.Cluster{DefaultStorageType: storage})
		if got != want {
			t.Errorf("clusterStorage(%q) = %q, want %q", storage, got, want)
		}
	}
}

func TestClusterWithNoNodesReported(t *testing.T) {
	if got := clusterNodes(&bigtableadmin.Cluster{}); got != "-" {
		t.Errorf("clusterNodes = %q, want a dash", got)
	}
}

func TestBigtableClusterDrillDownRejectsAWrongParent(t *testing.T) {
	_, err := (BigtableClusterLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-an-instance", Raw: testGKECluster()}, nil)
	if err == nil {
		t.Error("a non-Bigtable parent was accepted")
	}
}

func TestBigtableRowsAreNotSSHOrAirflowTargets(t *testing.T) {
	// A cluster carries a zone and a name, which is most of what an ssh target
	// looks like from the outside.
	for _, r := range []Resource{
		bigtableResource(testProject(), testBigtableInstance()),
		btClusterRow(testBigtableCluster()),
	} {
		if _, _, ok := SSHTarget(r); ok {
			t.Errorf("%s is an ssh target", r.Name)
		}
		if _, ok := AirflowURI(r); ok {
			t.Errorf("%s has an Airflow URI", r.Name)
		}
	}
}

func TestBigtableConsoleURLsAddressTheInstance(t *testing.T) {
	if url := bigtableResource(testProject(), testBigtableInstance()).ConsoleURL; !strings.Contains(url, "/instances/events-store/") {
		t.Errorf("instance console URL = %q", url)
	}
	if url := btClusterRow(testBigtableCluster()).ConsoleURL; !strings.Contains(url, "/instances/events-store/clusters") {
		t.Errorf("cluster console URL = %q", url)
	}
}
