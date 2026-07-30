package gcp

import (
	"testing"
	"time"

	"cloud.google.com/go/dataproc/v2/apiv1/dataprocpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDataprocJobResourceShape(t *testing.T) {
	r := dataprocJobResource(testProject(), "us-central1", testDataprocJob())

	if r.Name != "nightly-etl-0417" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "us-central1" {
		t.Errorf("Location = %q", r.Location)
	}
	if r.Status != "RUNNING" {
		t.Errorf("Status = %q, want RUNNING", r.Status)
	}
	if r.Row[2] != "PYSPARK" {
		t.Errorf("type cell = %q, want PYSPARK", r.Row[2])
	}
	if r.Row[3] != "analytics-cluster" {
		t.Errorf("cluster cell = %q", r.Row[3])
	}
	// Age is measured from submission, not from the current state's start, so a
	// job that has been running a minute is still 25 minutes old.
	if r.Row[5] != "25m" {
		t.Errorf("age cell = %q, want 25m from the submission time", r.Row[5])
	}
}

func TestDataprocJobAgeUsesSubmissionNotStateChange(t *testing.T) {
	// Status.StateStartTime is when the *current* state began, so on a job that
	// finished a minute ago it reads as one minute old however long it ran. The
	// first status in the history is the submission, which is the age meant.
	submitted := time.Now().Add(-3 * time.Hour)

	job := &dataprocpb.Job{
		Reference: &dataprocpb.JobReference{JobId: "j"},
		Status: &dataprocpb.JobStatus{
			State:          dataprocpb.JobStatus_DONE,
			StateStartTime: timestamppb.New(time.Now().Add(-time.Minute)),
		},
		StatusHistory: []*dataprocpb.JobStatus{{
			State:          dataprocpb.JobStatus_PENDING,
			StateStartTime: timestamppb.New(submitted),
		}},
	}

	if got := dataprocJobAge(job); got != "3h" {
		t.Errorf("age = %q, want 3h from the submission", got)
	}
}

func TestDataprocJobAgeFallsBackAndDegrades(t *testing.T) {
	// No history: the current state's start is the only timestamp there is.
	withStatus := &dataprocpb.Job{Status: &dataprocpb.JobStatus{
		StateStartTime: timestamppb.New(time.Now().Add(-2 * time.Hour)),
	}}
	if got := dataprocJobAge(withStatus); got != "2h" {
		t.Errorf("age = %q, want 2h", got)
	}

	if got := dataprocJobAge(&dataprocpb.Job{}); got != "-" {
		t.Errorf("age with no timestamps = %q, want a dash", got)
	}
}

func TestDataprocJobTypeNamesEveryPayload(t *testing.T) {
	// The API models the payload as a oneof, so the field's type is the only
	// place the answer lives. The oneof interface itself is unexported, hence
	// the built jobs rather than a table of payloads.
	tests := []struct {
		job  *dataprocpb.Job
		want string
	}{
		{&dataprocpb.Job{TypeJob: &dataprocpb.Job_HadoopJob{}}, "HADOOP"},
		{&dataprocpb.Job{TypeJob: &dataprocpb.Job_SparkJob{}}, "SPARK"},
		{&dataprocpb.Job{TypeJob: &dataprocpb.Job_PysparkJob{}}, "PYSPARK"},
		{&dataprocpb.Job{TypeJob: &dataprocpb.Job_HiveJob{}}, "HIVE"},
		{&dataprocpb.Job{TypeJob: &dataprocpb.Job_PigJob{}}, "PIG"},
		{&dataprocpb.Job{TypeJob: &dataprocpb.Job_SparkRJob{}}, "SPARKR"},
		{&dataprocpb.Job{TypeJob: &dataprocpb.Job_SparkSqlJob{}}, "SPARKSQL"},
		{&dataprocpb.Job{TypeJob: &dataprocpb.Job_PrestoJob{}}, "PRESTO"},
		{&dataprocpb.Job{TypeJob: &dataprocpb.Job_TrinoJob{}}, "TRINO"},
		{&dataprocpb.Job{TypeJob: &dataprocpb.Job_FlinkJob{}}, "FLINK"},
		{&dataprocpb.Job{}, "-"},
	}

	for _, tt := range tests {
		if got := dataprocJobType(tt.job); got != tt.want {
			t.Errorf("dataprocJobType(%T) = %q, want %q", tt.job.GetTypeJob(), got, tt.want)
		}
	}
}

func TestDataprocJobFallsBackToTheUUID(t *testing.T) {
	// The job id is optional on submission; the server always assigns a uuid,
	// and an unidentifiable row is worse than an ugly one.
	r := dataprocJobResource(testProject(), "us-central1", &dataprocpb.Job{
		JobUuid: "9f8e7d6c-1234",
	})
	if r.Name != "9f8e7d6c-1234" {
		t.Errorf("Name = %q, want the uuid", r.Name)
	}
	if r.Row[3] != "-" {
		t.Errorf("cluster cell = %q, want a dash", r.Row[3])
	}
}

func TestSortDataprocJobsByRecency(t *testing.T) {
	job := func(id string, ago time.Duration) Resource {
		return dataprocJobResource(testProject(), "us-central1", &dataprocpb.Job{
			Reference: &dataprocpb.JobReference{JobId: id},
			StatusHistory: []*dataprocpb.JobStatus{{
				StateStartTime: timestamppb.New(time.Now().Add(-ago)),
			}},
		})
	}

	resources := []Resource{
		job("old", 5*time.Hour),
		job("newest", time.Minute),
		job("middle", time.Hour),
	}
	sortDataprocJobsByRecency(resources)

	for i, name := range []string{"newest", "middle", "old"} {
		if resources[i].Name != name {
			t.Errorf("position %d is %q, want %q", i, resources[i].Name, name)
		}
	}
}

func TestSortDataprocJobsIsStableWithoutTimestamps(t *testing.T) {
	resources := []Resource{
		dataprocJobResource(testProject(), "us-central1", &dataprocpb.Job{Reference: &dataprocpb.JobReference{JobId: "c"}}),
		dataprocJobResource(testProject(), "us-central1", &dataprocpb.Job{Reference: &dataprocpb.JobReference{JobId: "a"}}),
		dataprocJobResource(testProject(), "us-central1", &dataprocpb.Job{Reference: &dataprocpb.JobReference{JobId: "b"}}),
	}
	sortDataprocJobsByRecency(resources)

	for i, name := range []string{"a", "b", "c"} {
		if resources[i].Name != name {
			t.Errorf("position %d is %q, want %q", i, resources[i].Name, name)
		}
	}
}

func TestDataprocJobIsNotAnSSHOrAirflowTarget(t *testing.T) {
	r := dataprocJobResource(testProject(), "us-central1", testDataprocJob())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a Dataproc job is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a Dataproc job has an Airflow URI")
	}
}
