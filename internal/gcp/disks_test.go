package gcp

import (
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/compute/apiv1/computepb"
)

func TestDiskResourceShape(t *testing.T) {
	r := diskResource(testProject(), "us-central1-a", testDisk())

	if r.Name != "etl-scratch-old" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "us-central1-a" {
		t.Errorf("Location = %q", r.Location)
	}
	if r.Row[2] != "500GB" {
		t.Errorf("size cell = %q", r.Row[2])
	}
	// The type comes back as a full URL and the last segment is the only part
	// anyone reads.
	if r.Row[3] != "pd-ssd" {
		t.Errorf("type cell = %q, want the URL's last segment", r.Row[3])
	}
}

// TestUnattachedDiskIsCalledUnattached is the point of the whole kind. READY is
// what the API says about a disk nothing is using, and READY is what hides it
// in a table of forty disks.
func TestUnattachedDiskIsCalledUnattached(t *testing.T) {
	r := diskResource(testProject(), "us-central1-a", testDisk())

	if r.Status != "UNATTACHED" {
		t.Errorf("Status = %q, want UNATTACHED rather than the API's READY", r.Status)
	}
	if r.Row[4] != "-" {
		t.Errorf("attached-to cell = %q, want a dash", r.Row[4])
	}
	if r.Row[5] != "240d" {
		t.Errorf("idle cell = %q, want the time since it was detached", r.Row[5])
	}
}

func TestAttachedDiskKeepsItsOwnStatus(t *testing.T) {
	d := testDisk()
	d.Users = []string{"https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/instances/web-01"}

	r := diskResource(testProject(), "us-central1-a", d)
	if r.Status != "READY" {
		t.Errorf("Status = %q, want the API's own status once something uses it", r.Status)
	}
	if r.Row[4] != "web-01" {
		t.Errorf("attached-to cell = %q", r.Row[4])
	}
	if r.Row[5] != "-" {
		t.Errorf("idle cell = %q, want a dash for a disk in use", r.Row[5])
	}
}

func TestSharedDiskNamesEveryUser(t *testing.T) {
	// A read-only disk mounted by three instances is deliberate. Naming one and
	// hiding the rest makes it look exclusive, which is how someone detaches it
	// from "the" instance and breaks two others.
	d := testDisk()
	d.Users = []string{
		"projects/p/zones/us-central1-a/instances/worker-1",
		"projects/p/zones/us-central1-b/instances/worker-2",
	}

	r := diskResource(testProject(), "us-central1", d)
	if r.Row[4] != "worker-1,worker-2" {
		t.Errorf("attached-to cell = %q, want every user named", r.Row[4])
	}
}

func TestNeverAttachedDiskSaysSo(t *testing.T) {
	// No detach timestamp does not mean "just detached". It means the disk was
	// created and never used, which is a stronger finding than an idle one.
	d := testDisk()
	d.LastDetachTimestamp = nil

	r := diskResource(testProject(), "us-central1-a", d)
	if !strings.HasPrefix(r.Row[5], "never used, ") {
		t.Errorf("idle cell = %q, want it to say the disk was never used", r.Row[5])
	}
	if !strings.HasSuffix(r.Row[5], "400d") {
		t.Errorf("idle cell = %q, want the age since creation", r.Row[5])
	}
}

func TestDiskStillCreatingIsNotIdle(t *testing.T) {
	// A disk mid-creation has no users either, and calling it UNATTACHED would
	// report a normal in-flight state as a cost finding.
	d := testDisk()
	d.Status = strPtr("CREATING")

	if got := diskStatus(d); got != "CREATING" {
		t.Errorf("diskStatus = %q, want the more urgent state to win", got)
	}
}

func TestDiskWithoutAStatus(t *testing.T) {
	if got := diskStatus(&computepb.Disk{}); got != "UNKNOWN" {
		t.Errorf("diskStatus = %q, want UNKNOWN rather than a blank cell", got)
	}
}

func TestDiskIdleForWithoutAnyTimestamp(t *testing.T) {
	// Neither timestamp set is not a case the API produces, but a blank cell in
	// the column the kind exists for is worse than a dash.
	if got := diskIdleFor(&computepb.Disk{}); got != "-" {
		t.Errorf("diskIdleFor = %q, want a dash", got)
	}
}

func TestRegionalDiskLinksToTheRegionPage(t *testing.T) {
	// A regional disk is replicated across two zones and the Console addresses
	// it under /regions/. The zone URL for one is a 404.
	zonal := diskConsoleURL(testProject(), "us-central1-a", "d")
	if !strings.Contains(zonal, "/zones/us-central1-a/") {
		t.Errorf("zonal URL = %q", zonal)
	}

	regional := diskConsoleURL(testProject(), "us-central1", "d")
	if !strings.Contains(regional, "/regions/us-central1/") {
		t.Errorf("regional URL = %q", regional)
	}
}

func TestRegionAndZoneAreToldApart(t *testing.T) {
	tests := []struct {
		scope  string
		region bool
	}{
		{"us-central1", true},
		{"us-central1-a", false},
		{"europe-west4", true},
		{"europe-west4-c", false},
		{"northamerica-northeast1", true},
		{"northamerica-northeast1-b", false},
		// No hyphen at all is neither, and guessing "region" would send the
		// link somewhere wrong rather than somewhere useless.
		{"nowhere", false},
	}
	for _, tt := range tests {
		if got := isRegionScope(tt.scope); got != tt.region {
			t.Errorf("isRegionScope(%q) = %v, want %v", tt.scope, got, tt.region)
		}
	}
}

func TestDiskSizeIsReported(t *testing.T) {
	// An unattached disk costs by the gigabyte-month, so the size is the column
	// that turns the finding into a number.
	d := testDisk()
	d.SizeGb = int64Ptr(0)
	r := diskResource(testProject(), "us-central1-a", d)
	if r.Row[2] != "0GB" {
		t.Errorf("size cell = %q", r.Row[2])
	}
}

func TestDisksAreNotSSHOrAirflowTargets(t *testing.T) {
	// A disk carries a zone and a name, which is most of what an ssh target
	// looks like from the outside.
	r := diskResource(testProject(), "us-central1-a", testDisk())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a disk is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a disk has an Airflow URI")
	}
}

func TestDiskIdleAgeUsesTheDetachTimeNotCreation(t *testing.T) {
	// A long-lived disk detached yesterday is not a 400-day finding, and the
	// two timestamps are far enough apart that using the wrong one is silent.
	d := testDisk()
	d.LastDetachTimestamp = strPtr(time.Now().Add(-30 * time.Hour).Format(time.RFC3339))

	if got := diskIdleFor(d); got != "1d" {
		t.Errorf("diskIdleFor = %q, want the time since the detach", got)
	}
}
