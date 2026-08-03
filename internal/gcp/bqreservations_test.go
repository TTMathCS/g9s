package gcp

import (
	"testing"

	bigqueryreservation "google.golang.org/api/bigqueryreservation/v1"

	"github.com/TTMathCS/g9s/internal/config"
)

func bqResRow(r *bigqueryreservation.Reservation) Resource {
	return bqReservationResource(testProject(), "US", r)
}

func TestBQReservationResourceShape(t *testing.T) {
	r := bqResRow(testBQReservation())

	if r.Name != "analytics" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "US" {
		t.Errorf("Location = %q", r.Location)
	}
	if r.Row[2] != "500 baseline" {
		t.Errorf("slots cell = %q", r.Row[2])
	}
	if r.Row[3] != "100 now, max 1000" {
		t.Errorf("autoscale cell = %q, want the current and the ceiling", r.Row[3])
	}
	if r.Row[5] != "shared" {
		t.Errorf("idle slots cell = %q", r.Row[5])
	}
}

// TestUnsharedBaselineIsTheFinding. Slots are billed whether or not a query
// uses them; a reservation that also refuses to lend its idle capacity has
// those slots paid for and unavailable to everything else.
func TestUnsharedBaselineIsTheFinding(t *testing.T) {
	r := testBQReservation()
	r.IgnoreIdleSlots = true

	row := bqResRow(r)
	if row.Status != "IDLE_SLOTS_RESERVED" {
		t.Errorf("Status = %q, want the hoarded capacity flagged", row.Status)
	}
	if row.Row[5] != "not shared" {
		t.Errorf("idle slots cell = %q", row.Row[5])
	}
}

// TestAutoscaleOnlyReservationIsNotAFinding: no baseline means nothing is being
// paid for continuously, so refusing to share costs no one anything.
func TestAutoscaleOnlyReservationIsNotAFinding(t *testing.T) {
	r := testBQReservation()
	r.SlotCapacity = 0
	r.IgnoreIdleSlots = true

	row := bqResRow(r)
	if row.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE with no baseline to hoard", row.Status)
	}
	if row.Row[2] != "0 baseline" {
		t.Errorf("slots cell = %q, want it explicit rather than a dash", row.Row[2])
	}
}

func TestAutoscaleOffAndCeilingOnly(t *testing.T) {
	r := testBQReservation()
	r.Autoscale = nil
	if got := bqReservationAutoscale(r); got != "off" {
		t.Errorf("autoscale = %q, want off", got)
	}

	// Configured but not currently scaled up: the ceiling is still the number
	// worth knowing, and "0 now" would read as broken.
	r.Autoscale = &bigqueryreservation.Autoscale{MaxSlots: 2000}
	if got := bqReservationAutoscale(r); got != "max 2000" {
		t.Errorf("autoscale = %q", got)
	}

	// A zero ceiling is the same as no autoscaling.
	r.Autoscale = &bigqueryreservation.Autoscale{}
	if got := bqReservationAutoscale(r); got != "off" {
		t.Errorf("autoscale = %q, want off", got)
	}
}

// TestBigQueryLocationsAlwaysIncludeTheMultiRegions is the whole reason this
// lister does not use the plain region list. Reservations live in `US` or `EU`
// far more often than in a named region, and neither is in anyone's config.
func TestBigQueryLocationsAlwaysIncludeTheMultiRegions(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{Regions: []string{"us-central1"}}}
	got := bigQueryLocations(cfg, config.Project{})

	want := map[string]bool{"US": true, "EU": true, "us-central1": true}
	if len(got) != len(want) {
		t.Fatalf("locations = %v, want exactly %d", got, len(want))
	}
	for _, loc := range got {
		if !want[loc] {
			t.Errorf("unexpected location %q", loc)
		}
	}
}

func TestBigQueryLocationsWorkWithNoRegionsConfigured(t *testing.T) {
	// A project with no regions set still has reservations in the multi-regions,
	// so this must not come back empty the way the region-only listers do.
	got := bigQueryLocations(&config.Config{}, config.Project{})
	if len(got) != 2 {
		t.Fatalf("locations = %v, want the two multi-regions", got)
	}
}

func TestBigQueryLocationsDoNotDuplicate(t *testing.T) {
	// Someone who lists US explicitly must not get it swept twice — the table
	// would show every reservation in it twice over.
	cfg := &config.Config{Defaults: config.Defaults{Regions: []string{"US", "EU", "us-east1"}}}
	got := bigQueryLocations(cfg, config.Project{})

	seen := map[string]int{}
	for _, loc := range got {
		seen[loc]++
	}
	for loc, n := range seen {
		if n > 1 {
			t.Errorf("%s swept %d times", loc, n)
		}
	}
	if len(got) != 3 {
		t.Errorf("locations = %v, want three distinct", got)
	}
}

func TestBQReservationEditionFallback(t *testing.T) {
	for _, edition := range []string{"", "EDITION_UNSPECIFIED"} {
		got := bqReservationEdition(&bigqueryreservation.Reservation{Edition: edition})
		if got != "-" {
			t.Errorf("bqReservationEdition(%q) = %q, want a dash", edition, got)
		}
	}
}

func TestBQReservationsAreNotSSHOrAirflowTargets(t *testing.T) {
	r := bqResRow(testBQReservation())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a reservation is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a reservation has an Airflow URI")
	}
}
