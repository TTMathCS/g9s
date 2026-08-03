package gcp

import (
	"strings"
	"testing"

	memcache "google.golang.org/api/memcache/v1"
)

func mcRow(i *memcache.Instance) Resource {
	return memcacheResource(testProject(), i)
}

func TestMemcacheResourceShape(t *testing.T) {
	r := mcRow(testMemcacheInstance())

	if r.Name != "page-cache" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "us-central1" {
		t.Errorf("Location = %q", r.Location)
	}
	if r.Row[2] != "3" {
		t.Errorf("nodes cell = %q", r.Row[2])
	}
	if r.Row[3] != "4096MB/2vCPU" {
		t.Errorf("node size cell = %q", r.Row[3])
	}
	if r.Row[5] != "1_5" {
		t.Errorf("version cell = %q, want the MEMCACHE_ prefix stripped", r.Row[5])
	}
	if r.Status != "READY" {
		t.Errorf("Status = %q", r.Status)
	}
}

// TestTotalMemoryIsOnNeitherFieldAlone: what the instance costs is nodes times
// per-node memory, and the API reports the two separately.
func TestTotalMemoryIsOnNeitherFieldAlone(t *testing.T) {
	// 3 nodes x 4096 MB = 12 GB.
	if got := mcRow(testMemcacheInstance()).Row[4]; got != "12.0GB" {
		t.Errorf("total cell = %q, want the product of nodes and node size", got)
	}
}

// TestPartiallyDegradedInstanceStillReportsReady is the failure this kind is
// for. The instance state stays READY while individual nodes are down and
// serving nothing.
func TestPartiallyDegradedInstanceStillReportsReady(t *testing.T) {
	i := testMemcacheInstance()
	i.MemcacheNodes[1].State = "DELETING"

	r := mcRow(i)
	if r.Status != "NODES_DOWN" {
		t.Errorf("Status = %q, want the down node to outrank the instance's READY", r.Status)
	}
	if r.Row[2] != "3 (1 down)" {
		t.Errorf("nodes cell = %q, want the count of nodes not serving", r.Row[2])
	}
	// The state column still reports what the instance itself says.
	if r.Row[6] != "READY" {
		t.Errorf("state cell = %q", r.Row[6])
	}
}

func TestInstanceStateOutranksNodeState(t *testing.T) {
	// An instance mid-create has nodes that are not ready yet, and calling that
	// a degraded instance turns a normal in-flight state into an alarm.
	i := testMemcacheInstance()
	i.State = "CREATING"
	i.MemcacheNodes[0].State = "CREATING"

	if got := mcRow(i).Status; got != "CREATING" {
		t.Errorf("Status = %q, want the lifecycle state to win", got)
	}
}

func TestNodesWithNoStateReportedAreNotCountedDown(t *testing.T) {
	// An empty node state is "not reported", not "broken". Counting it as down
	// would put a false finding on every instance whose API response omits it.
	i := testMemcacheInstance()
	for _, node := range i.MemcacheNodes {
		node.State = ""
	}

	if got := nodesNotReady(i); got != 0 {
		t.Errorf("nodesNotReady = %d, want 0 when no state is reported", got)
	}
	if got := mcRow(i).Status; got != "READY" {
		t.Errorf("Status = %q, want READY", got)
	}
}

func TestMemcacheWithoutANodeConfig(t *testing.T) {
	// A nil nested struct on a generated REST type is the standard way these
	// listers panic.
	i := testMemcacheInstance()
	i.NodeConfig = nil

	r := mcRow(i)
	if r.Row[3] != "-" {
		t.Errorf("node size cell = %q, want a dash", r.Row[3])
	}
	if r.Row[4] != "-" {
		t.Errorf("total cell = %q, want a dash", r.Row[4])
	}
}

func TestMemcacheWithNoNodes(t *testing.T) {
	i := testMemcacheInstance()
	i.NodeCount = 0

	r := mcRow(i)
	if r.Row[2] != "-" {
		t.Errorf("nodes cell = %q, want a dash", r.Row[2])
	}
	if r.Row[4] != "-" {
		t.Errorf("total cell = %q, want a dash without a node count", r.Row[4])
	}
}

func TestMemcacheStateAndVersionFallbacks(t *testing.T) {
	for _, state := range []string{"", "STATE_UNSPECIFIED"} {
		if got := memcacheState(&memcache.Instance{State: state}); got != "UNKNOWN" {
			t.Errorf("memcacheState(%q) = %q, want UNKNOWN", state, got)
		}
	}
	if got := memcacheVersion(&memcache.Instance{}); got != "-" {
		t.Errorf("memcacheVersion = %q, want a dash", got)
	}
}

func TestMemcacheConsoleURLCarriesTheRegion(t *testing.T) {
	r := mcRow(testMemcacheInstance())
	if !strings.Contains(r.ConsoleURL, "/locations/us-central1/instances/page-cache") {
		t.Errorf("console URL = %q", r.ConsoleURL)
	}
}

func TestMemcacheInstancesAreNotSSHOrAirflowTargets(t *testing.T) {
	r := mcRow(testMemcacheInstance())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a Memcached instance is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a Memcached instance has an Airflow URI")
	}
}
