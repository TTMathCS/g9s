package gcp

import (
	"strings"
	"testing"
	"time"

	bigquery "google.golang.org/api/bigquery/v2"
)

func TestDatasetResourceShape(t *testing.T) {
	r := datasetResource(testProject(), testBigQueryDataset())

	if r.Name != "analytics" {
		t.Errorf("Name = %q, want analytics", r.Name)
	}
	if r.Location != "northamerica-northeast1" {
		t.Errorf("Location = %q", r.Location)
	}
	// Datasets have no lifecycle state, so a synthetic one keeps them out of
	// the dashboard's UNKNOWN bucket.
	if r.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE", r.Status)
	}
	// Labels are sorted, so the row does not reshuffle between refreshes that
	// returned the same data.
	if got := r.Row[3]; got != "env=prod team=dataeng" {
		t.Errorf("labels cell = %q", got)
	}
}

func TestDatasetResourceFallsBackWhenFieldsAreMissing(t *testing.T) {
	// The list response is thin and every field in it is optional.
	r := datasetResource(testProject(), &bigquery.DatasetListDatasets{Id: "sandbox-123:scratch"})

	if r.Name != "scratch" {
		t.Errorf("Name = %q, want the id's last segment", r.Name)
	}
	if r.Location != "-" {
		t.Errorf("Location = %q, want a dash", r.Location)
	}
	if r.Row[2] != "DEFAULT" {
		t.Errorf("type cell = %q, want DEFAULT", r.Row[2])
	}
	if r.Row[3] != "-" {
		t.Errorf("labels cell = %q, want a dash", r.Row[3])
	}
}

func TestJobResourceShape(t *testing.T) {
	r := jobResource(testProject(), testBigQueryJob())

	if r.Name != "bquxjob_1a2b3c" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Status != "RUNNING" {
		t.Errorf("Status = %q, want RUNNING", r.Status)
	}
	if r.Row[2] != "QUERY" {
		t.Errorf("type cell = %q, want QUERY", r.Row[2])
	}
	if r.Row[6] != "3.0GB" {
		t.Errorf("processed cell = %q, want 3.0GB", r.Row[6])
	}
	// A running job has no end time; the duration is measured to now, which is
	// the whole reason to look at this table.
	if r.Row[5] != "4m" {
		t.Errorf("duration cell = %q, want 4m for a job started four minutes ago", r.Row[5])
	}
}

func TestJobStateFoldsErrorResultIntoTheState(t *testing.T) {
	// A finished job is DONE whether it worked or not. Showing the raw state
	// colours a failed job green and buries the only row worth reacting to.
	tests := []struct {
		name string
		job  *bigquery.JobListJobs
		want string
	}{
		{
			"failed job reports DONE with an error",
			&bigquery.JobListJobs{Status: &bigquery.JobStatus{
				State:       "DONE",
				ErrorResult: &bigquery.ErrorProto{Reason: "invalidQuery", Message: "Syntax error"},
			}},
			"FAILED",
		},
		{
			"successful job",
			&bigquery.JobListJobs{Status: &bigquery.JobStatus{State: "DONE"}},
			"DONE",
		},
		{
			"running job",
			&bigquery.JobListJobs{Status: &bigquery.JobStatus{State: "RUNNING"}},
			"RUNNING",
		},
		{
			"state only on the top-level field",
			&bigquery.JobListJobs{State: "PENDING"},
			"PENDING",
		},
		{
			"no state at all",
			&bigquery.JobListJobs{},
			"UNKNOWN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jobState(tt.job); got != tt.want {
				t.Errorf("jobState = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestJobDurationHandlesEveryStatisticsShape(t *testing.T) {
	start := time.Now().Add(-90 * time.Minute)

	tests := []struct {
		name string
		job  *bigquery.JobListJobs
		want string
	}{
		{
			"finished job uses its end time",
			&bigquery.JobListJobs{Statistics: &bigquery.JobStatistics{
				StartTime: start.UnixMilli(),
				EndTime:   start.Add(30 * time.Minute).UnixMilli(),
			}},
			"30m",
		},
		{
			"running job is measured to now",
			&bigquery.JobListJobs{Statistics: &bigquery.JobStatistics{StartTime: start.UnixMilli()}},
			"1h",
		},
		{"never started", &bigquery.JobListJobs{Statistics: &bigquery.JobStatistics{}}, "-"},
		{"no statistics", &bigquery.JobListJobs{}, "-"},
		{
			"end before start is not rendered as a negative age",
			&bigquery.JobListJobs{Statistics: &bigquery.JobStatistics{
				StartTime: start.UnixMilli(),
				EndTime:   start.Add(-time.Minute).UnixMilli(),
			}},
			"-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jobDuration(tt.job); got != tt.want {
				t.Errorf("jobDuration = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestJobBytesProcessedReadsTheRightStatisticsBlock(t *testing.T) {
	// Each job type fills in a different block, and only one of them.
	tests := []struct {
		name string
		job  *bigquery.JobListJobs
		want int64
	}{
		{"query", &bigquery.JobListJobs{Statistics: &bigquery.JobStatistics{
			Query: &bigquery.JobStatistics2{TotalBytesProcessed: 1234},
		}}, 1234},
		{"load", &bigquery.JobListJobs{Statistics: &bigquery.JobStatistics{
			Load: &bigquery.JobStatistics3{InputFileBytes: 99},
		}}, 99},
		{"extract", &bigquery.JobListJobs{Statistics: &bigquery.JobStatistics{
			Extract: &bigquery.JobStatistics4{InputBytes: 42},
		}}, 42},
		{"copy", &bigquery.JobListJobs{Statistics: &bigquery.JobStatistics{
			Copy: &bigquery.JobStatistics5{CopiedLogicalBytes: 7},
		}}, 7},
		// Absent is not the same answer as zero, and the table says so.
		{"nothing reported", &bigquery.JobListJobs{Statistics: &bigquery.JobStatistics{}}, -1},
		{"no statistics", &bigquery.JobListJobs{}, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jobBytesProcessed(tt.job); got != tt.want {
				t.Errorf("jobBytesProcessed = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{-1, "-"},
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{5 * 1024 * 1024, "5.0MB"},
		{3 * 1024 * 1024 * 1024, "3.0GB"},
		{2 * 1024 * 1024 * 1024 * 1024, "2.0TB"},
		{4 * 1024 * 1024 * 1024 * 1024 * 1024, "4.0PB"},
	}

	for _, tt := range tests {
		if got := humanBytes(tt.in); got != tt.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSortJobsByRecencyPutsTheNewestFirst(t *testing.T) {
	// "What is running" is asked newest-first, so this kind overrides the
	// location-then-name order the other listers use.
	now := time.Now()
	job := func(id string, ago time.Duration) Resource {
		return jobResource(testProject(), &bigquery.JobListJobs{
			JobReference: &bigquery.JobReference{JobId: id, Location: "us"},
			Statistics:   &bigquery.JobStatistics{CreationTime: now.Add(-ago).UnixMilli()},
		})
	}

	resources := []Resource{
		job("old", 3*time.Hour),
		job("newest", time.Minute),
		job("middle", time.Hour),
	}
	sortJobsByRecency(resources)

	want := []string{"newest", "middle", "old"}
	for i, name := range want {
		if resources[i].Name != name {
			t.Errorf("position %d is %q, want %q", i, resources[i].Name, name)
		}
	}
}

func TestSortJobsByRecencyIsStableWithoutStatistics(t *testing.T) {
	// Jobs with no statistics all sort as time zero, so the name is what keeps
	// the table from reshuffling between refreshes.
	resources := []Resource{
		jobResource(testProject(), &bigquery.JobListJobs{JobReference: &bigquery.JobReference{JobId: "c"}}),
		jobResource(testProject(), &bigquery.JobListJobs{JobReference: &bigquery.JobReference{JobId: "a"}}),
		jobResource(testProject(), &bigquery.JobListJobs{JobReference: &bigquery.JobReference{JobId: "b"}}),
	}
	sortJobsByRecency(resources)

	for i, name := range []string{"a", "b", "c"} {
		if resources[i].Name != name {
			t.Errorf("position %d is %q, want %q", i, resources[i].Name, name)
		}
	}
}

func TestBigQueryConsoleURLsAreDeepLinks(t *testing.T) {
	dataset := datasetResource(testProject(), testBigQueryDataset())
	if !strings.Contains(dataset.ConsoleURL, "d=analytics") {
		t.Errorf("dataset console URL does not name the dataset: %q", dataset.ConsoleURL)
	}

	job := jobResource(testProject(), testBigQueryJob())
	// bq:<location>:<job id> is the form the Console's job viewer expects.
	if !strings.Contains(job.ConsoleURL, "j=bq:northamerica-northeast1:bquxjob_1a2b3c") {
		t.Errorf("job console URL is not a job deep link: %q", job.ConsoleURL)
	}
}

func TestBigQueryResourcesAreNotSSHOrAirflowTargets(t *testing.T) {
	// Both actions type-switch on Raw, and both must decline these.
	for _, r := range []Resource{
		datasetResource(testProject(), testBigQueryDataset()),
		jobResource(testProject(), testBigQueryJob()),
	} {
		if _, _, ok := SSHTarget(r); ok {
			t.Errorf("%s is an ssh target", r.Name)
		}
		if _, ok := AirflowURI(r); ok {
			t.Errorf("%s has an Airflow URI", r.Name)
		}
	}
}

func TestFormatLabels(t *testing.T) {
	if got := formatLabels(nil); got != "-" {
		t.Errorf("formatLabels(nil) = %q, want a dash", got)
	}
	if got := formatLabels(map[string]string{"b": "2", "a": "1"}); got != "a=1 b=2" {
		t.Errorf("formatLabels = %q, want sorted pairs", got)
	}
}

func TestAfterLast(t *testing.T) {
	// BigQuery qualifies ids with punctuation, not a path, so lastSegment does
	// not apply: "sandbox-123:scratch" has no slash in it at all.
	tests := []struct{ in, sep, want string }{
		{"sandbox-123:scratch", ":", "scratch"},
		{"northamerica-northeast1.bquxjob_1a2b3c", ".", "bquxjob_1a2b3c"},
		{"no-separator", ":", "no-separator"},
		{"", ":", ""},
		{"trailing:", ":", ""},
	}
	for _, tt := range tests {
		if got := afterLast(tt.in, tt.sep); got != tt.want {
			t.Errorf("afterLast(%q, %q) = %q, want %q", tt.in, tt.sep, got, tt.want)
		}
	}
}

func TestJobResourceFallsBackToTheCompositeID(t *testing.T) {
	r := jobResource(testProject(), &bigquery.JobListJobs{
		Id: "sandbox-123:northamerica-northeast1.bquxjob_9z8y7x",
	})
	if r.Name != "bquxjob_9z8y7x" {
		t.Errorf("Name = %q, want the job id out of the composite", r.Name)
	}
	if r.Row[3] != "-" {
		t.Errorf("user cell = %q, want a dash", r.Row[3])
	}
	if r.Status != "UNKNOWN" {
		t.Errorf("Status = %q, want UNKNOWN", r.Status)
	}
}
