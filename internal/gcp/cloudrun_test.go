package gcp

import (
	"testing"
	"time"

	run "google.golang.org/api/run/v2"
)

func TestRunServiceResourceShape(t *testing.T) {
	r := runServiceResource(testProject(), "us-central1", testCloudRunService())

	if r.Name != "api-gateway" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "us-central1" {
		t.Errorf("Location = %q", r.Location)
	}
	// CONDITION_SUCCEEDED is the API's word for Ready, which reads as neither
	// healthy nor unhealthy in a table.
	if r.Status != "READY" {
		t.Errorf("Status = %q, want READY", r.Status)
	}
	if r.Row[2] != "https://api-gateway-abc123-uc.a.run.app" {
		t.Errorf("url cell = %q", r.Row[2])
	}
	if r.Row[3] != "ALL" {
		t.Errorf("ingress cell = %q, want the prefix stripped", r.Row[3])
	}
	if r.Row[4] != "api-gateway-00042-xyz" {
		t.Errorf("latest ready cell = %q, want the short revision name", r.Row[4])
	}
}

func TestRunServiceWithoutAUrl(t *testing.T) {
	// A service that never deployed, or one with its default URL disabled.
	r := runServiceResource(testProject(), "us-central1", &run.GoogleCloudRunV2Service{
		Name: "projects/p/locations/us-central1/services/never-deployed",
	})
	if r.Row[2] != "-" {
		t.Errorf("url cell = %q, want a dash", r.Row[2])
	}
	if r.Row[4] != "-" {
		t.Errorf("latest ready cell = %q, want a dash", r.Row[4])
	}
	if r.Status != "UNKNOWN" {
		t.Errorf("Status = %q, want UNKNOWN with no terminal condition", r.Status)
	}
}

func TestRunJobResourceShape(t *testing.T) {
	r := runJobResource(testProject(), "us-central1", testCloudRunJob())

	if r.Name != "nightly-report" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Row[2] != "128" {
		t.Errorf("executions cell = %q", r.Row[2])
	}
	if r.Row[3] != "nightly-report-9x8w7" {
		t.Errorf("last execution cell = %q", r.Row[3])
	}
	if r.Row[4] != "SUCCEEDED" {
		t.Errorf("last result cell = %q, want the prefix stripped", r.Row[4])
	}
}

func TestRunJobStatusLeadsWithTheExecutionResult(t *testing.T) {
	// The point of the kind: a job whose executions all fail is still a
	// perfectly healthy job resource, so the resource's own condition cannot be
	// what colours the row.
	failed := runJobResource(testProject(), "us-central1", &run.GoogleCloudRunV2Job{
		Name:              "projects/p/locations/us-central1/jobs/nightly",
		TerminalCondition: &run.GoogleCloudRunV2Condition{State: "CONDITION_SUCCEEDED"},
		LatestCreatedExecution: &run.GoogleCloudRunV2ExecutionReference{
			Name:             "projects/p/locations/us-central1/jobs/nightly/executions/nightly-abc",
			CompletionStatus: "EXECUTION_FAILED",
			CompletionTime:   time.Now().Add(-time.Hour).Format(time.RFC3339),
		},
	})

	if failed.Status != "FAILED" {
		t.Errorf("Status = %q, want FAILED from the execution", failed.Status)
	}
	// The job's own condition is still reported, in its own column.
	if failed.Row[5] != "READY" {
		t.Errorf("state cell = %q, want the job's own condition", failed.Row[5])
	}
}

func TestRunJobExecutionStillRunning(t *testing.T) {
	// No completion status and no completion time means it has not finished,
	// which is a different answer from "no result reported".
	r := runJobResource(testProject(), "us-central1", &run.GoogleCloudRunV2Job{
		Name: "projects/p/locations/us-central1/jobs/nightly",
		LatestCreatedExecution: &run.GoogleCloudRunV2ExecutionReference{
			Name: "projects/p/locations/us-central1/jobs/nightly/executions/nightly-abc",
		},
	})
	if r.Row[4] != "RUNNING" {
		t.Errorf("last result cell = %q, want RUNNING", r.Row[4])
	}
}

func TestRunJobNeverExecuted(t *testing.T) {
	r := runJobResource(testProject(), "us-central1", &run.GoogleCloudRunV2Job{
		Name:              "projects/p/locations/us-central1/jobs/fresh",
		TerminalCondition: &run.GoogleCloudRunV2Condition{State: "CONDITION_SUCCEEDED"},
	})
	if r.Row[3] != "-" || r.Row[4] != "-" {
		t.Errorf("last execution/result = %q/%q, want dashes", r.Row[3], r.Row[4])
	}
	// With no execution to judge, the job's own condition is what is left.
	if r.Status != "READY" {
		t.Errorf("Status = %q, want the job condition as the fallback", r.Status)
	}
}

func TestConditionState(t *testing.T) {
	tests := []struct {
		cond *run.GoogleCloudRunV2Condition
		want string
	}{
		{nil, "UNKNOWN"},
		{&run.GoogleCloudRunV2Condition{State: "CONDITION_SUCCEEDED"}, "READY"},
		{&run.GoogleCloudRunV2Condition{State: "CONDITION_FAILED"}, "FAILED"},
		{&run.GoogleCloudRunV2Condition{State: "CONDITION_RECONCILING"}, "RECONCILING"},
		{&run.GoogleCloudRunV2Condition{State: "CONDITION_PENDING"}, "PENDING"},
		{&run.GoogleCloudRunV2Condition{State: ""}, "UNKNOWN"},
		// An unrecognised state is passed through with the prefix stripped
		// rather than flattened to UNKNOWN.
		{&run.GoogleCloudRunV2Condition{State: "CONDITION_SOMETHING_NEW"}, "SOMETHING_NEW"},
	}
	for _, tt := range tests {
		if got := conditionState(tt.cond); got != tt.want {
			t.Errorf("conditionState(%v) = %q, want %q", tt.cond, got, tt.want)
		}
	}
}

func TestCloudRunResourcesAreNotSSHOrAirflowTargets(t *testing.T) {
	for _, r := range []Resource{
		runServiceResource(testProject(), "us-central1", testCloudRunService()),
		runJobResource(testProject(), "us-central1", testCloudRunJob()),
	} {
		if _, _, ok := SSHTarget(r); ok {
			t.Errorf("%s is an ssh target", r.Name)
		}
		if _, ok := AirflowURI(r); ok {
			t.Errorf("%s has an Airflow URI", r.Name)
		}
	}
}
