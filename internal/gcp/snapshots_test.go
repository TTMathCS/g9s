package gcp

import (
	"net/url"
	"strings"
	"testing"

	compute "google.golang.org/api/compute/v1"
)

func TestSnapshotResourceShape(t *testing.T) {
	r := snapshotResource(testProject(), testSnapshot())

	if r.Name != "orders-db-2026-07-31" || r.Location != "global" {
		t.Errorf("got name=%q location=%q", r.Name, r.Location)
	}
	if r.Row[1] != "orders-db" {
		t.Errorf("source disk = %q", r.Row[1])
	}
	if r.Row[2] != "500GB" || r.Row[3] != "64.0GB" {
		t.Errorf("size cells = %q and %q", r.Row[2], r.Row[3])
	}
	if r.Status != "READY" {
		t.Errorf("status = %q", r.Status)
	}
}

func TestSnapshotWithoutStatusIsUnknown(t *testing.T) {
	if got := snapshotResource(testProject(), &compute.Snapshot{Name: "s"}).Status; got != "UNKNOWN" {
		t.Errorf("status = %q, want UNKNOWN", got)
	}
}

func TestRegionalSnapshotKeepsItsRegion(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Region = "https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1"
	r := snapshotResource(testProject(), snapshot)
	if r.Location != "us-central1" || !strings.Contains(r.ConsoleURL, "/regions/us-central1/") {
		t.Errorf("location=%q console=%q", r.Location, r.ConsoleURL)
	}
}

func TestAggregatedSnapshotUsesResponseScope(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Region = ""
	r := snapshotResourceInScope(testProject(), "regions/us-central1", snapshot)
	if r.Location != "us-central1" || !strings.Contains(r.ConsoleURL, "/regions/us-central1/") {
		t.Errorf("location=%q console=%q", r.Location, r.ConsoleURL)
	}
}

func TestSnapshotConsoleURLCarriesTheProject(t *testing.T) {
	got := snapshotResource(testProject(), testSnapshot()).ConsoleURL
	if !strings.Contains(got, testProject().ProjectID) || strings.Contains(got, "/global/global/") {
		t.Errorf("invalid snapshot console URL: %q", got)
	}
}

func TestSnapshotAggregatedListURL(t *testing.T) {
	got, err := snapshotAggregatedListURL("https://compute.googleapis.com/compute/beta/", "sandbox-123", "next token")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/compute/beta/projects/sandbox-123/aggregated/snapshots" {
		t.Errorf("path = %q", parsed.Path)
	}
	if parsed.Query().Get("returnPartialSuccess") != "true" || parsed.Query().Get("pageToken") != "next token" {
		t.Errorf("query = %q", parsed.RawQuery)
	}
}
