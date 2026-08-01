package gcp

import (
	"testing"

	"cloud.google.com/go/dataproc/v2/apiv1/dataprocpb"
)

func TestClusterJobDropsTheRepeatedCells(t *testing.T) {
	// Under one cluster the region and cluster name are the same on every row,
	// which is the whole point of being under one.
	r := clusterJobResource(testProject(), "us-central1", testDataprocJob())

	want := len((ClusterJobLister{}).Kind().Columns)
	if len(r.Row) != want {
		t.Fatalf("row has %d cells, want %d", len(r.Row), want)
	}

	wide := dataprocJobResource(testProject(), "us-central1", testDataprocJob())
	if r.Row[0] != wide.Row[0] {
		t.Errorf("job id = %q, want the same as the region-wide table", r.Row[0])
	}
	if r.Row[1] != wide.Row[2] {
		t.Errorf("type cell = %q, want the region and cluster cells dropped", r.Row[1])
	}
	if r.Row[2] != wide.Row[4] {
		t.Errorf("state cell = %q", r.Row[2])
	}
	if r.Row[3] != wide.Row[5] {
		t.Errorf("age cell = %q", r.Row[3])
	}
}

func TestClusterJobReadsTheSameFromEitherTable(t *testing.T) {
	// A job must not look different depending on which table it is seen from,
	// so the type, state and age rules are shared rather than reimplemented.
	failed := &dataprocpb.Job{
		Reference: &dataprocpb.JobReference{JobId: "spark-9f2"},
		Placement: &dataprocpb.JobPlacement{ClusterName: "analytics-cluster"},
		Status:    &dataprocpb.JobStatus{State: dataprocpb.JobStatus_ERROR},
	}

	wide := dataprocJobResource(testProject(), "us-central1", failed)
	under := clusterJobResource(testProject(), "us-central1", failed)

	if under.Status != wide.Status {
		t.Errorf("status differs between tables: %q vs %q", under.Status, wide.Status)
	}
	if under.Name != wide.Name {
		t.Errorf("name differs between tables: %q vs %q", under.Name, wide.Name)
	}
	if under.ConsoleURL != wide.ConsoleURL {
		t.Error("console URL differs between tables")
	}
}

func TestClusterJobDrillDownRejectsAParentThatIsNotACluster(t *testing.T) {
	_, err := (ClusterJobLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-a-cluster", Location: "us-central1", Raw: testInstance()}, nil)
	if err == nil {
		t.Error("a non-cluster parent was accepted")
	}
}

func TestClusterJobDrillDownNeedsTheRegion(t *testing.T) {
	// Dataproc's endpoint is regional, and a request sent to the wrong one
	// returns nothing rather than erroring — so a parent with no region has to
	// be refused rather than quietly listed against the default endpoint.
	_, err := (ClusterJobLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "analytics-cluster", Raw: testCluster()}, nil)
	if err == nil {
		t.Error("a cluster row with no region was accepted")
	}
}

func TestClusterJobsAreNotSSHOrAirflowTargets(t *testing.T) {
	r := clusterJobResource(testProject(), "us-central1", testDataprocJob())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a cluster job is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a cluster job has an Airflow URI")
	}
}

func TestDataprocClusterOffersItsJobs(t *testing.T) {
	children := ChildrenOf("dataproc")
	if len(children) != 1 {
		t.Fatalf("ChildrenOf(dataproc) returned %d listings, want 1", len(children))
	}
	if children[0].Kind().ID != "clusterjobs" {
		t.Errorf("child = %q", children[0].Kind().ID)
	}
}
