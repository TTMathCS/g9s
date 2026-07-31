package gcp

import (
	"testing"

	"cloud.google.com/go/container/apiv1/containerpb"
)

func TestNodePoolListingCostsNoAPICall(t *testing.T) {
	// The point of making node pools a drill-down rather than a kind:
	// ListClusters already returned them, so opening one asks nothing new. A
	// nil client and nil options here is the assertion.
	parent := clusterNodeResource(testProject(), testGKEClusterWithNodePools())

	result, err := (NodePoolLister{}).List(t.Context(), nil, testProject(), parent, nil)
	if err != nil {
		t.Fatalf("node pool drill-down: %v", err)
	}
	if len(result.Resources) != 2 {
		t.Fatalf("listed %d pools, want 2", len(result.Resources))
	}
}

func TestNodePoolRejectsAParentThatIsNotACluster(t *testing.T) {
	// Drilling reads Raw, and a row from another kind reaching here would
	// otherwise panic on the type assertion.
	_, err := (NodePoolLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-a-cluster", Raw: testInstance()}, nil)
	if err == nil {
		t.Error("a non-cluster parent was accepted")
	}
}

func TestNodePoolCountIsAcrossEveryZone(t *testing.T) {
	// InitialNodeCount is per zone. A pool of 2 across three zones runs six
	// VMs, and reading the 2 as the total is how a cluster ends up mis-sized
	// on paper.
	c := testGKEClusterWithNodePools()
	r := nodePoolResource(testProject(), c, c.GetNodePools()[0])

	if r.Row[2] != "6" {
		t.Errorf("node count cell = %q, want 6 — 2 per zone across three zones", r.Row[2])
	}
}

func TestNodePoolNamesSpotCapacity(t *testing.T) {
	c := testGKEClusterWithNodePools()
	spot := nodePoolResource(testProject(), c, c.GetNodePools()[1])

	if spot.Row[1] != "n2-highmem-8 (spot)" {
		t.Errorf("machine cell = %q, want the spot marker", spot.Row[1])
	}
	// Preemptible is the older name for nearly the same thing, and a pool that
	// can vanish under you is worth saying either way.
	preempt := nodePoolResource(testProject(), c, &containerpb.NodePool{
		Name:   "legacy",
		Config: &containerpb.NodeConfig{MachineType: "n1-standard-4", Preemptible: true},
	})
	if preempt.Row[1] != "n1-standard-4 (preempt)" {
		t.Errorf("machine cell = %q, want the preemptible marker", preempt.Row[1])
	}
}

func TestAutoscaleSummary(t *testing.T) {
	tests := []struct {
		name  string
		a     *containerpb.NodePoolAutoscaling
		zones int
		want  string
	}{
		// A pool pinned at one size is not a misconfiguration, but it is the
		// reason a cluster stops absorbing load, and nothing else says so.
		{"absent", nil, 3, "off"},
		{"disabled", &containerpb.NodePoolAutoscaling{Enabled: false}, 3, "off"},
		// Per-zone bounds are multiplied out so the column compares with the
		// node count beside it.
		{"per zone", &containerpb.NodePoolAutoscaling{Enabled: true, MinNodeCount: 1, MaxNodeCount: 5}, 3, "3-15"},
		{"cluster-wide wins", &containerpb.NodePoolAutoscaling{
			Enabled: true, MinNodeCount: 1, MaxNodeCount: 5,
			TotalMinNodeCount: 2, TotalMaxNodeCount: 40,
		}, 3, "2-40"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := autoscaleSummary(tt.a, tt.zones); got != tt.want {
				t.Errorf("autoscaleSummary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUpgradeSummary(t *testing.T) {
	tests := []struct {
		m    *containerpb.NodeManagement
		want string
	}{
		{nil, "manual"},
		{&containerpb.NodeManagement{}, "manual"},
		{&containerpb.NodeManagement{AutoUpgrade: true}, "upgrade"},
		{&containerpb.NodeManagement{AutoRepair: true}, "repair"},
		{&containerpb.NodeManagement{AutoUpgrade: true, AutoRepair: true}, "upgrade+repair"},
	}
	for _, tt := range tests {
		if got := upgradeSummary(tt.m); got != tt.want {
			t.Errorf("upgradeSummary(%v) = %q, want %q", tt.m, got, tt.want)
		}
	}
}

func TestAutopilotPoolReportsNoMachineType(t *testing.T) {
	// Autopilot manages node configuration itself, so there is nothing to
	// report rather than nothing configured.
	c := &containerpb.Cluster{Name: "auto", Location: "us-central1"}
	r := nodePoolResource(testProject(), c, &containerpb.NodePool{Name: "system"})

	if r.Row[1] != "-" {
		t.Errorf("machine cell = %q, want a dash", r.Row[1])
	}
	// One zone assumed rather than zero, so the count is never a bare 0 for a
	// pool that is actually running something.
	if r.Row[2] != "0" {
		t.Errorf("node count cell = %q", r.Row[2])
	}
}

func TestNodePoolsAreNotSSHOrAirflowTargets(t *testing.T) {
	c := testGKEClusterWithNodePools()
	r := nodePoolResource(testProject(), c, c.GetNodePools()[0])
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a node pool is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a node pool has an Airflow URI")
	}
}
