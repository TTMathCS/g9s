package gcp

import (
	"testing"
	"time"

	run "google.golang.org/api/run/v2"
)

func testRunParent() Resource {
	return runServiceResource(testProject(), "us-central1", testCloudRunService())
}

func TestRevisionResourceShape(t *testing.T) {
	r := revisionResource(testProject(), testRunParent(), testCloudRunRevision(),
		map[string]int64{"api-gateway-00042-xyz": 100})

	if r.Name != "api-gateway-00042-xyz" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Row[1] != "100%" {
		t.Errorf("traffic cell = %q", r.Row[1])
	}
	// The registry host and project path are the same on every row and would
	// cost most of the column; the tag is what says whether two revisions are
	// the same build.
	if r.Row[2] != "api-gateway:v2.14.0" {
		t.Errorf("image cell = %q, want the image and tag only", r.Row[2])
	}
	if r.Row[3] != "1-40" {
		t.Errorf("scaling cell = %q", r.Row[3])
	}
	if r.Status != "READY" {
		t.Errorf("Status = %q, want the Ready condition translated", r.Status)
	}
}

func TestTrafficComesFromTheServiceNotTheRevision(t *testing.T) {
	// The whole point of the join: a revision has no idea whether anything is
	// routed to it, so the split has to be read off the parent.
	service := testCloudRunService()
	service.TrafficStatuses = []*run.GoogleCloudRunV2TrafficTargetStatus{
		{Revision: "projects/p/locations/us-central1/services/s/revisions/api-gateway-00042-xyz", Percent: 90},
		{Revision: "projects/p/locations/us-central1/services/s/revisions/api-gateway-00041-abc", Percent: 10},
	}

	traffic := trafficByRevision(service)
	if traffic["api-gateway-00042-xyz"] != 90 || traffic["api-gateway-00041-abc"] != 10 {
		t.Errorf("traffic = %v", traffic)
	}
}

func TestTaggedAndMainRoutesToOneRevisionAddUp(t *testing.T) {
	// A tag and the main route can both point at the same revision, arriving as
	// two entries. Reporting only one of them understates what it serves.
	service := testCloudRunService()
	service.TrafficStatuses = []*run.GoogleCloudRunV2TrafficTargetStatus{
		{Revision: "projects/p/locations/l/services/s/revisions/rev-a", Percent: 60},
		{Revision: "projects/p/locations/l/services/s/revisions/rev-a", Tag: "canary", Percent: 40},
	}

	if got := trafficByRevision(service)["rev-a"]; got != 100 {
		t.Errorf("traffic for rev-a = %d, want the two entries summed", got)
	}
}

func TestRevisionServingNothingShowsADash(t *testing.T) {
	// The normal state for every revision except the current one, so a hard
	// zero on each would be noise in the column that matters most.
	r := revisionResource(testProject(), testRunParent(), testCloudRunRevision(), nil)
	if r.Row[1] != "-" {
		t.Errorf("traffic cell = %q, want a dash", r.Row[1])
	}

	zero := revisionResource(testProject(), testRunParent(), testCloudRunRevision(),
		map[string]int64{"api-gateway-00042-xyz": 0})
	if zero.Row[1] != "-" {
		t.Errorf("an explicit zero rendered as %q, want a dash too", zero.Row[1])
	}
}

func TestRevisionsSortServingFirstThenNewest(t *testing.T) {
	// Traffic leads because it is the answer to the question the table was
	// opened with; among revisions serving nothing, the newest is the one just
	// deployed and the one being wondered about.
	newIdle := &run.GoogleCloudRunV2Revision{
		Name:       "projects/p/locations/l/services/s/revisions/new-idle",
		CreateTime: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	}
	oldIdle := &run.GoogleCloudRunV2Revision{
		Name:       "projects/p/locations/l/services/s/revisions/old-idle",
		CreateTime: time.Now().Add(-200 * time.Hour).Format(time.RFC3339),
	}
	serving := &run.GoogleCloudRunV2Revision{
		Name:       "projects/p/locations/l/services/s/revisions/serving",
		CreateTime: time.Now().Add(-100 * time.Hour).Format(time.RFC3339),
	}

	traffic := map[string]int64{"serving": 100}
	parent := testRunParent()
	resources := []Resource{
		revisionResource(testProject(), parent, newIdle, traffic),
		revisionResource(testProject(), parent, oldIdle, traffic),
		revisionResource(testProject(), parent, serving, traffic),
	}
	sortRevisions(resources, traffic)

	want := []string{"serving", "new-idle", "old-idle"}
	for i, w := range want {
		if resources[i].Name != w {
			t.Errorf("row %d = %q, want %q", i, resources[i].Name, w)
		}
	}
}

func TestRevisionStatePicksTheReadyCondition(t *testing.T) {
	// A revision carries a list of conditions rather than one terminal
	// condition, so the one that matters has to be found by name — and it is
	// not always first.
	rev := &run.GoogleCloudRunV2Revision{
		Name: "projects/p/locations/l/services/s/revisions/r",
		Conditions: []*run.GoogleCloudRunV2Condition{
			{Type: "ResourcesAvailable", State: "CONDITION_SUCCEEDED"},
			{Type: "Ready", State: "CONDITION_FAILED"},
		},
	}
	if got := revisionState(rev); got != "FAILED" {
		t.Errorf("revisionState = %q, want the Ready condition", got)
	}

	// No conditions at all is not the same as healthy.
	if got := revisionState(&run.GoogleCloudRunV2Revision{}); got != "UNKNOWN" {
		t.Errorf("revisionState with no conditions = %q, want UNKNOWN", got)
	}
}

func TestScalingSummary(t *testing.T) {
	tests := []struct {
		name string
		in   *run.GoogleCloudRunV2RevisionScaling
		want string
	}{
		{"absent", nil, "-"},
		{"unset", &run.GoogleCloudRunV2RevisionScaling{}, "-"},
		// A minimum above zero is the difference between a service that costs
		// nothing idle and one that does not.
		{"warm", &run.GoogleCloudRunV2RevisionScaling{MinInstanceCount: 2, MaxInstanceCount: 10}, "2-10"},
		{"no ceiling", &run.GoogleCloudRunV2RevisionScaling{MinInstanceCount: 1}, "1-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scalingSummary(tt.in); got != tt.want {
				t.Errorf("scalingSummary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRevisionWithNoContainer(t *testing.T) {
	r := revisionResource(testProject(), testRunParent(),
		&run.GoogleCloudRunV2Revision{Name: "projects/p/locations/l/services/s/revisions/bare"}, nil)
	if r.Row[2] != "-" {
		t.Errorf("image cell = %q, want a dash", r.Row[2])
	}
}

func TestRevisionDrillDownRejectsAParentThatIsNotAService(t *testing.T) {
	_, err := (CloudRunRevisionLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-a-service", Raw: testInstance()}, nil)
	if err == nil {
		t.Error("a non-service parent was accepted")
	}
}

func TestRevisionsAreNotSSHOrAirflowTargets(t *testing.T) {
	r := revisionResource(testProject(), testRunParent(), testCloudRunRevision(), nil)
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a revision is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a revision has an Airflow URI")
	}
}
