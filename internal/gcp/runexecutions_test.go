package gcp

import (
	"testing"
	"time"

	run "google.golang.org/api/run/v2"
)

func testRunJobParent() Resource {
	return runJobResource(testProject(), "us-central1", testCloudRunJob())
}

func TestExecutionResourceShape(t *testing.T) {
	r := executionResource(testProject(), testRunJobParent(), testCloudRunExecution())

	if r.Name != "nightly-report-9x8w7" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Row[1] != "12/12" {
		t.Errorf("tasks cell = %q", r.Row[1])
	}
	if r.Row[4] != "7m" {
		t.Errorf("duration cell = %q, want 7m", r.Row[4])
	}
	if r.Status != "SUCCEEDED" {
		t.Errorf("Status = %q", r.Status)
	}
}

func TestTaskTallySeparatesOneBadShardFromACollapse(t *testing.T) {
	// A parallel job that finished with 199 of 200 tasks succeeded reports
	// itself as failed. The tally is the only thing saying whether that was one
	// bad shard or the whole run falling over.
	partial := taskTally(&run.GoogleCloudRunV2Execution{
		TaskCount: 200, SucceededCount: 199, FailedCount: 1,
	})
	if partial != "199/200 (1 failed)" {
		t.Errorf("tally = %q", partial)
	}

	collapsed := taskTally(&run.GoogleCloudRunV2Execution{
		TaskCount: 200, SucceededCount: 0, FailedCount: 200,
	})
	if collapsed != "0/200 (200 failed)" {
		t.Errorf("tally = %q", collapsed)
	}

	// No failures at all keeps the cell quiet.
	clean := taskTally(&run.GoogleCloudRunV2Execution{TaskCount: 5, SucceededCount: 5})
	if clean != "5/5" {
		t.Errorf("tally = %q, want no failure clause", clean)
	}
}

func TestExecutionStillRunning(t *testing.T) {
	// No completion is a different answer from having finished with no result
	// reported, and the duration keeps counting.
	e := &run.GoogleCloudRunV2Execution{
		Name:      "projects/p/locations/l/jobs/j/executions/live",
		TaskCount: 10, SucceededCount: 4,
		StartTime: time.Now().Add(-90 * time.Minute).Format(time.RFC3339),
	}

	if got := executionResult(e); got != "RUNNING" {
		t.Errorf("result = %q, want RUNNING", got)
	}
	if got := executionDuration(e); got != "1h" {
		t.Errorf("duration = %q, want it counting from the start", got)
	}
}

func TestExecutionResultFallsBackToTheCounts(t *testing.T) {
	// Finished, but with no Completed condition on it. The counts still answer
	// the question, and reporting SUCCEEDED for a run with failures would be
	// the worst possible default.
	failed := &run.GoogleCloudRunV2Execution{
		StartTime:      time.Now().Add(-time.Hour).Format(time.RFC3339),
		CompletionTime: time.Now().Format(time.RFC3339),
		TaskCount:      3, SucceededCount: 2, FailedCount: 1,
	}
	if got := executionResult(failed); got != "FAILED" {
		t.Errorf("result = %q, want FAILED", got)
	}

	cancelled := &run.GoogleCloudRunV2Execution{
		StartTime:      time.Now().Add(-time.Hour).Format(time.RFC3339),
		CompletionTime: time.Now().Format(time.RFC3339),
		TaskCount:      3, CancelledCount: 3,
	}
	if got := executionResult(cancelled); got != "FAILED" {
		t.Errorf("cancelled result = %q, want FAILED", got)
	}

	clean := &run.GoogleCloudRunV2Execution{
		StartTime:      time.Now().Add(-time.Hour).Format(time.RFC3339),
		CompletionTime: time.Now().Format(time.RFC3339),
		TaskCount:      3, SucceededCount: 3,
	}
	if got := executionResult(clean); got != "SUCCEEDED" {
		t.Errorf("clean result = %q, want SUCCEEDED", got)
	}
}

func TestCompletedConditionWinsOverTheCounts(t *testing.T) {
	// When the API states the outcome, that is the outcome — the counts are the
	// fallback, not the source of truth.
	e := &run.GoogleCloudRunV2Execution{
		Conditions: []*run.GoogleCloudRunV2Condition{
			{Type: "ResourcesAvailable", State: "CONDITION_SUCCEEDED"},
			{Type: "Completed", State: "CONDITION_FAILED"},
		},
		TaskCount: 3, SucceededCount: 3,
		CompletionTime: time.Now().Format(time.RFC3339),
	}
	if got := executionResult(e); got != "FAILED" {
		t.Errorf("result = %q, want the Completed condition to win", got)
	}
}

func TestExecutionDurationEdges(t *testing.T) {
	now := time.Now()

	// No start time at all is not zero seconds, it is unknown.
	if got := executionDuration(&run.GoogleCloudRunV2Execution{}); got != "-" {
		t.Errorf("duration with no start = %q, want a dash", got)
	}

	// Clocks disagreeing across a round trip must not render a negative.
	backwards := &run.GoogleCloudRunV2Execution{
		StartTime:      now.Format(time.RFC3339),
		CompletionTime: now.Add(-time.Second).Format(time.RFC3339),
	}
	if got := executionDuration(backwards); got != "0s" {
		t.Errorf("duration = %q, want 0s rather than a negative", got)
	}
}

func TestExecutionsSortNewestFirst(t *testing.T) {
	// The history is read newest first — "has this been failing all week" is
	// answered by scrolling down, not up.
	parent := testRunJobParent()
	older := &run.GoogleCloudRunV2Execution{
		Name:       "projects/p/locations/l/jobs/j/executions/older",
		CreateTime: time.Now().Add(-48 * time.Hour).Format(time.RFC3339),
	}
	newer := &run.GoogleCloudRunV2Execution{
		Name:       "projects/p/locations/l/jobs/j/executions/newer",
		CreateTime: time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
	}

	resources := []Resource{
		executionResource(testProject(), parent, older),
		executionResource(testProject(), parent, newer),
	}
	sortExecutionsByRecency(resources)

	if resources[0].Name != "newer" {
		t.Errorf("first row = %q, want the newest run", resources[0].Name)
	}
}

func TestExecutionDrillDownRejectsAParentThatIsNotAJob(t *testing.T) {
	_, err := (CloudRunExecutionLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-a-job", Raw: testInstance()}, nil)
	if err == nil {
		t.Error("a non-job parent was accepted")
	}
}

func TestExecutionsAreNotSSHOrAirflowTargets(t *testing.T) {
	r := executionResource(testProject(), testRunJobParent(), testCloudRunExecution())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("an execution is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("an execution has an Airflow URI")
	}
}
