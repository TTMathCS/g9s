package gcp

import (
	"strings"
	"testing"

	cloudfunctions "google.golang.org/api/cloudfunctions/v2"
)

func TestFunctionResourceShape(t *testing.T) {
	r := functionResource(testProject(), testFunction())

	if r.Name != "thumbnailer" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "us-central1" {
		t.Errorf("Location = %q", r.Location)
	}
	if r.Status != "ACTIVE" {
		t.Errorf("Status = %q", r.Status)
	}
	if r.Row[3] != "python312" {
		t.Errorf("runtime cell = %q", r.Row[3])
	}
	if r.Row[6] != "16d" {
		t.Errorf("updated cell = %q, want 16d", r.Row[6])
	}
}

// TestFunctionRegionComesFromTheName pins the thing the wildcard call costs:
// asking for locations/- means the response has no location field, so the only
// place the region appears is inside the resource name.
func TestFunctionRegionComesFromTheName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"projects/p/locations/europe-west4/functions/f", "europe-west4"},
		{"projects/p/locations/asia-east1/functions/f", "asia-east1"},
		// Nothing guarantees the shape, and a blank cell in the column that
		// says where to look is worse than a dash.
		{"functions/f", "-"},
		{"", "-"},
		// "locations" as the last segment has nothing following it.
		{"projects/p/locations", "-"},
	}
	for _, tt := range tests {
		if got := functionRegion(tt.name); got != tt.want {
			t.Errorf("functionRegion(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestGenerationIsAColumn: gen 2 is Cloud Run with a build attached, gen 1 is
// not, and they scale, time out and bill differently. When one function behaves
// unlike its neighbour this is the first thing anyone asks.
func TestGenerationIsAColumn(t *testing.T) {
	tests := []struct {
		env  string
		want string
	}{
		{"GEN_1", "1"},
		{"GEN_2", "2"},
		{"ENVIRONMENT_UNSPECIFIED", "-"},
		{"", "-"},
	}
	for _, tt := range tests {
		got := functionGeneration(&cloudfunctions.Function{Environment: tt.env})
		if got != tt.want {
			t.Errorf("functionGeneration(%q) = %q, want %q", tt.env, got, tt.want)
		}
	}
}

func TestHTTPFunctionIsNamedAsSuch(t *testing.T) {
	// No event trigger means the function is invoked over HTTP, reachable by
	// whoever the IAM policy allows. That is the row worth spotting.
	f := testFunction()
	f.EventTrigger = nil

	if got := functionTrigger(f); got != "HTTP" {
		t.Errorf("functionTrigger = %q, want HTTP", got)
	}
}

func TestPubSubTriggerNamesTheTopic(t *testing.T) {
	// The topic is the answer to "what makes this run", and the event type for
	// a Pub/Sub trigger says only "a message arrived".
	f := testFunction()
	f.EventTrigger = &cloudfunctions.EventTrigger{
		EventType:   "google.cloud.pubsub.topic.v1.messagePublished",
		PubsubTopic: "projects/sandbox-123/topics/orders",
	}

	if got := functionTrigger(f); got != "pubsub: orders" {
		t.Errorf("functionTrigger = %q, want the topic named", got)
	}
}

func TestEventTypeIsShortenedToItsTail(t *testing.T) {
	// Reverse-DNS event types share a long prefix and differ at the end, so a
	// column showing the head shows the same string for every row.
	tests := []struct {
		event string
		want  string
	}{
		{"google.cloud.storage.object.v1.finalized", "object.v1.finalized"},
		{"google.cloud.audit.log.v1.written", "log.v1.written"},
		{"google.firebase.database.ref.v1.written", "ref.v1.written"},
		// Already short enough to keep whole.
		{"custom.event", "custom.event"},
		{"a.b.c", "a.b.c"},
	}
	for _, tt := range tests {
		if got := shortEventType(tt.event); got != tt.want {
			t.Errorf("shortEventType(%q) = %q, want %q", tt.event, got, tt.want)
		}
	}
}

func TestTriggerWithNeitherTopicNorType(t *testing.T) {
	f := testFunction()
	f.EventTrigger = &cloudfunctions.EventTrigger{}

	if got := functionTrigger(f); got != "event" {
		t.Errorf("functionTrigger = %q, want a generic event", got)
	}
}

func TestFunctionWithoutABuildConfig(t *testing.T) {
	// A function still deploying has no build config yet, and a nil deref on a
	// generated REST struct is the standard way these listers crash.
	f := testFunction()
	f.BuildConfig = nil

	if got := functionRuntime(f); got != "-" {
		t.Errorf("functionRuntime = %q, want a dash", got)
	}
	if r := functionResource(testProject(), f); r.Row[3] != "-" {
		t.Errorf("runtime cell = %q", r.Row[3])
	}
}

func TestFunctionWithoutAState(t *testing.T) {
	for _, state := range []string{"", "STATE_UNSPECIFIED"} {
		f := testFunction()
		f.State = state

		r := functionResource(testProject(), f)
		if r.Status != "UNKNOWN" {
			t.Errorf("Status for %q = %q, want UNKNOWN", state, r.Status)
		}
	}
}

func TestFunctionConsoleURLCarriesTheRegion(t *testing.T) {
	// The Console addresses a function by region and name; the project alone
	// lands on the list page instead of the function.
	r := functionResource(testProject(), testFunction())
	if !strings.Contains(r.ConsoleURL, "/functions/details/us-central1/thumbnailer") {
		t.Errorf("console URL = %q", r.ConsoleURL)
	}
}

func TestFunctionsAreNotSSHOrAirflowTargets(t *testing.T) {
	r := functionResource(testProject(), testFunction())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a Cloud Function is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a Cloud Function has an Airflow URI")
	}
}
