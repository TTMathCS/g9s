package gcp

import (
	"strings"
	"testing"
	"time"

	dataflow "google.golang.org/api/dataflow/v1b3"
)

func TestDataflowJobResourceShape(t *testing.T) {
	r := dataflowJobResource(testProject(), testDataflowJob())

	if r.Name != "orders-enrichment" {
		t.Errorf("Name = %q", r.Name)
	}
	// The region is on the job rather than in the request, which is the whole
	// point of using the aggregated call.
	if r.Location != "us-central1" {
		t.Errorf("Location = %q", r.Location)
	}
	if r.Status != "RUNNING" {
		t.Errorf("Status = %q, want the JOB_STATE_ prefix stripped", r.Status)
	}
	if r.Row[2] != "STREAMING" {
		t.Errorf("type cell = %q, want the JOB_TYPE_ prefix stripped", r.Row[2])
	}
	if r.Row[4] != "11d" {
		t.Errorf("age cell = %q, want 11d", r.Row[4])
	}
}

func TestDataflowJobWithoutARegion(t *testing.T) {
	// Nothing in the API guarantees the field is set, and a blank cell in the
	// column that says where to look is worse than a dash.
	r := dataflowJobResource(testProject(), &dataflow.Job{Name: "orphan"})
	if r.Row[1] != "-" {
		t.Errorf("region cell = %q, want a dash", r.Row[1])
	}
	if r.Status != "UNKNOWN" {
		t.Errorf("Status = %q, want UNKNOWN with no state reported", r.Status)
	}
	if r.Row[2] != "-" {
		t.Errorf("type cell = %q, want a dash", r.Row[2])
	}
}

func TestDataflowJobsSortNewestFirst(t *testing.T) {
	// "What is running" is asked newest first, the same as the BigQuery and
	// Dataproc job tables.
	older := &dataflow.Job{Name: "b-older", CreateTime: time.Now().Add(-72 * time.Hour).Format(time.RFC3339)}
	newer := &dataflow.Job{Name: "a-newer", CreateTime: time.Now().Add(-1 * time.Hour).Format(time.RFC3339)}

	resources := []Resource{
		dataflowJobResource(testProject(), older),
		dataflowJobResource(testProject(), newer),
	}
	sortDataflowJobsByRecency(resources)

	if resources[0].Name != "a-newer" {
		t.Errorf("first row = %q, want the newest job", resources[0].Name)
	}
}

func TestDataflowJobsSortIsStableWithoutTimestamps(t *testing.T) {
	// A job with no create time must not shuffle between refreshes; the name
	// breaks the tie.
	resources := []Resource{
		dataflowJobResource(testProject(), &dataflow.Job{Name: "zebra"}),
		dataflowJobResource(testProject(), &dataflow.Job{Name: "alpha"}),
	}
	sortDataflowJobsByRecency(resources)

	if resources[0].Name != "alpha" {
		t.Errorf("first row = %q, want the tie broken on name", resources[0].Name)
	}
}

func TestDataflowConsoleURLPointsAtTheJobID(t *testing.T) {
	// The Console addresses a job by its generated id, not its name — two runs
	// of the same pipeline share a name and are different jobs.
	r := dataflowJobResource(testProject(), testDataflowJob())
	if !strings.Contains(r.ConsoleURL, "2026-07-30_18_04_11-1234567890123456789") {
		t.Errorf("console URL = %q, want the job id in it", r.ConsoleURL)
	}
}

func TestSortedKeysIsDeterministic(t *testing.T) {
	// Failed regions come out of a map, and warnings that reorder between
	// refreshes read as new failures.
	got := sortedKeys(map[string]bool{"us-west1": true, "europe-west4": true, "asia-east1": true})
	want := []string{"asia-east1", "europe-west4", "us-west1"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortedKeys = %v, want %v", got, want)
		}
	}
}

func TestDataflowJobsAreNotSSHOrAirflowTargets(t *testing.T) {
	r := dataflowJobResource(testProject(), testDataflowJob())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a Dataflow job is an ssh target")
	}
	// Dataflow jobs are often launched by Composer, so this one is worth
	// pinning: the Airflow link belongs to the environment, not the job.
	if _, ok := AirflowURI(r); ok {
		t.Error("a Dataflow job has an Airflow URI")
	}
}
