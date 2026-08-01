package gcp

import (
	"strings"
	"testing"

	cloudscheduler "google.golang.org/api/cloudscheduler/v1"
)

func schedRow(j *cloudscheduler.Job) Resource {
	return schedulerJobResource(testProject(), "us-central1", j)
}

func TestSchedulerJobResourceShape(t *testing.T) {
	r := schedRow(testSchedulerJob())

	if r.Name != "nightly-rollup" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "us-central1" {
		t.Errorf("Location = %q", r.Location)
	}
	if r.Row[2] != "0 3 * * *" {
		t.Errorf("schedule cell = %q", r.Row[2])
	}
	if r.Row[4] != "9h" {
		t.Errorf("last cell = %q, want 9h", r.Row[4])
	}
	if r.Row[5] != "ok" {
		t.Errorf("result cell = %q, want ok", r.Row[5])
	}
	if r.Status != "ENABLED" {
		t.Errorf("Status = %q", r.Status)
	}
}

// TestPausedJobIsTheQuietFailure is one of the two things this kind exists for.
// A paused job errors nothing and alerts nothing — the work just stops, and
// every other column still looks correct.
func TestPausedJobIsTheQuietFailure(t *testing.T) {
	j := testSchedulerJob()
	j.State = "PAUSED"

	r := schedRow(j)
	if r.Status != "PAUSED" {
		t.Errorf("Status = %q, want PAUSED", r.Status)
	}
	if r.Row[6] != "PAUSED" {
		t.Errorf("state cell = %q", r.Row[6])
	}
}

// TestFailingJobOutranksItsOwnState is the other one. ENABLED is perfectly true
// of a job that has errored every night for a week.
func TestFailingJobOutranksItsOwnState(t *testing.T) {
	j := testSchedulerJob()
	j.Status = &cloudscheduler.Status{Code: 7, Message: "PERMISSION_DENIED: caller lacks run.invoker"}

	r := schedRow(j)
	if r.Status != "LAST_RUN_FAILED" {
		t.Errorf("Status = %q, want the failure to outrank ENABLED", r.Status)
	}
	if !strings.Contains(r.Row[5], "PERMISSION_DENIED") {
		t.Errorf("result cell = %q, want the error message", r.Row[5])
	}
	// The state column still tells the truth about the job itself.
	if r.Row[6] != "ENABLED" {
		t.Errorf("state cell = %q, want the job's own state kept", r.Row[6])
	}
}

// TestPausedBeatsFailed: a paused job cannot have failed recently, and if both
// are set the pause is the actionable one — resuming it is the fix, chasing the
// stale error is not.
func TestPausedBeatsFailed(t *testing.T) {
	j := testSchedulerJob()
	j.State = "PAUSED"
	j.Status = &cloudscheduler.Status{Code: 7, Message: "stale error from before the pause"}

	if got := schedRow(j).Status; got != "PAUSED" {
		t.Errorf("Status = %q, want PAUSED", got)
	}
}

func TestJobThatNeverRan(t *testing.T) {
	// No attempt time and no status is not a success. Reporting "ok" there
	// invents a run that never happened.
	j := testSchedulerJob()
	j.LastAttemptTime = ""

	r := schedRow(j)
	if r.Row[4] != "never" {
		t.Errorf("last cell = %q, want never", r.Row[4])
	}
	if r.Row[5] != "-" {
		t.Errorf("result cell = %q, want a dash rather than ok", r.Row[5])
	}
}

func TestSchedulerTargets(t *testing.T) {
	// Which target type it is changes what a failure means, so the type leads.
	tests := []struct {
		name string
		job  *cloudscheduler.Job
		want string
	}{
		{"http", &cloudscheduler.Job{HttpTarget: &cloudscheduler.HttpTarget{
			Uri: "https://api.example.com/run", HttpMethod: "GET"}}, "GET https://api.example.com/run"},
		// The API omits the method when it is the default.
		{"http default method", &cloudscheduler.Job{HttpTarget: &cloudscheduler.HttpTarget{
			Uri: "https://api.example.com/run"}}, "POST https://api.example.com/run"},
		{"pubsub", &cloudscheduler.Job{PubsubTarget: &cloudscheduler.PubsubTarget{
			TopicName: "projects/sandbox-123/topics/rollup"}}, "pubsub: rollup"},
		{"appengine", &cloudscheduler.Job{AppEngineHttpTarget: &cloudscheduler.AppEngineHttpTarget{
			RelativeUri: "/cron/rollup"}}, "appengine /cron/rollup"},
		{"appengine default path", &cloudscheduler.Job{
			AppEngineHttpTarget: &cloudscheduler.AppEngineHttpTarget{}}, "appengine /"},
		// Nothing set is not a shape the API produces, and a blank cell in the
		// column saying what runs is worse than a dash.
		{"none", &cloudscheduler.Job{}, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := schedulerTarget(tt.job); got != tt.want {
				t.Errorf("schedulerTarget = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLongTargetURLIsClipped(t *testing.T) {
	// A signed URL with a page of query string must not push every other column
	// off the terminal, but the host has to survive the cut.
	j := &cloudscheduler.Job{HttpTarget: &cloudscheduler.HttpTarget{
		HttpMethod: "POST",
		Uri:        "https://api.example.com/run?" + strings.Repeat("token=abcdef&", 40),
	}}

	got := schedulerTarget(j)
	if !strings.HasPrefix(got, "POST https://api.example.com/run") {
		t.Errorf("target = %q, want the host kept", got)
	}
	if len([]rune(got)) > 70 {
		t.Errorf("target is %d runes, want it clipped", len([]rune(got)))
	}
}

func TestStatusWithoutAMessage(t *testing.T) {
	// A bare code is still more use than an empty cell on a row that failed.
	j := testSchedulerJob()
	j.Status = &cloudscheduler.Status{Code: 4}

	if got := schedulerResult(j); got != "code 4" {
		t.Errorf("schedulerResult = %q, want the code", got)
	}
}

func TestJobWithoutAScheduleOrState(t *testing.T) {
	j := &cloudscheduler.Job{Name: "projects/p/locations/us-central1/jobs/orphan"}

	r := schedRow(j)
	if r.Row[2] != "-" {
		t.Errorf("schedule cell = %q, want a dash", r.Row[2])
	}
	if r.Row[6] != "UNKNOWN" {
		t.Errorf("state cell = %q, want UNKNOWN", r.Row[6])
	}
}

func TestSchedulerConsoleURLAddressesTheJob(t *testing.T) {
	r := schedRow(testSchedulerJob())
	if !strings.Contains(r.ConsoleURL, "/us-central1/nightly-rollup") {
		t.Errorf("console URL = %q, want region and job in it", r.ConsoleURL)
	}
}

func TestSchedulerJobsAreNotSSHOrAirflowTargets(t *testing.T) {
	// Scheduler jobs often trigger Composer DAGs, so this is worth pinning: the
	// Airflow link belongs to the environment, not to whatever pokes it.
	r := schedRow(testSchedulerJob())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a Scheduler job is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a Scheduler job has an Airflow URI")
	}
}
