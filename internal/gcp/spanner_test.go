package gcp

import (
	"strings"
	"testing"

	spanner "google.golang.org/api/spanner/v1"
)

func TestSpannerInstanceResourceShape(t *testing.T) {
	r := spannerInstanceResource(testProject(), testSpannerInstance())

	if r.Name != "orders-prod" {
		t.Errorf("Name = %q", r.Name)
	}
	// The instance config is the closest thing a Spanner instance has to a
	// location, and the `regional-` prefix is noise in a narrow column.
	if r.Location != "us-central1" {
		t.Errorf("Location = %q", r.Location)
	}
	if r.Row[3] != "enterprise" {
		t.Errorf("edition cell = %q", r.Row[3])
	}
	if r.Status != "READY" {
		t.Errorf("Status = %q", r.Status)
	}
}

// TestCapacityNormalisesTheTwoUnits is the reason the column exists. An
// instance created with nodes reports NodeCount and one created with processing
// units reports ProcessingUnits; 1000 PU is one node. Printing whichever field
// happens to be set puts "1" and "1000" side by side for identical instances.
func TestCapacityNormalisesTheTwoUnits(t *testing.T) {
	byUnits := &spanner.Instance{ProcessingUnits: 1000}
	byNodes := &spanner.Instance{NodeCount: 1}

	got, want := spannerCapacity(byUnits), spannerCapacity(byNodes)
	if got != want {
		t.Errorf("one node reads as %q by units and %q by nodes — same size, two answers", got, want)
	}
	if !strings.Contains(got, "1 node") {
		t.Errorf("capacity = %q, want the node equivalent alongside the units", got)
	}
}

func TestCapacityBelowOneNode(t *testing.T) {
	// The smallest instances are sized in hundreds of PU, where the node
	// equivalent is a fraction and says less than the raw number.
	if got := spannerCapacity(&spanner.Instance{ProcessingUnits: 100}); got != "100 PU" {
		t.Errorf("capacity = %q, want plain processing units", got)
	}
}

func TestCapacityWithAFractionalNodeCount(t *testing.T) {
	got := spannerCapacity(&spanner.Instance{ProcessingUnits: 2500})
	if !strings.Contains(got, "2500 PU") || !strings.Contains(got, "2.5 nodes") {
		t.Errorf("capacity = %q, want both units", got)
	}
}

func TestCapacityPluralisesNodes(t *testing.T) {
	if got := spannerCapacity(&spanner.Instance{ProcessingUnits: 3000}); !strings.Contains(got, "3 nodes") {
		t.Errorf("capacity = %q", got)
	}
	if got := spannerCapacity(&spanner.Instance{ProcessingUnits: 1000}); strings.Contains(got, "nodes") {
		t.Errorf("capacity = %q, want the singular", got)
	}
}

func TestCapacityWithNothingReported(t *testing.T) {
	if got := spannerCapacity(&spanner.Instance{}); got != "-" {
		t.Errorf("capacity = %q, want a dash", got)
	}
}

// TestAutoscalingChangesWhatCapacityMeans: with autoscaling on, the capacity
// column is wherever the instance happens to be right now rather than a setting.
func TestAutoscalingLimits(t *testing.T) {
	byUnits := &spanner.Instance{AutoscalingConfig: &spanner.AutoscalingConfig{
		AutoscalingLimits: &spanner.AutoscalingLimits{MinProcessingUnits: 1000, MaxProcessingUnits: 5000},
	}}
	if got := spannerAutoscaling(byUnits); got != "1000–5000 PU" {
		t.Errorf("autoscaling = %q", got)
	}

	byNodes := &spanner.Instance{AutoscalingConfig: &spanner.AutoscalingConfig{
		AutoscalingLimits: &spanner.AutoscalingLimits{MinNodes: 1, MaxNodes: 10},
	}}
	if got := spannerAutoscaling(byNodes); got != "1–10 nodes" {
		t.Errorf("autoscaling = %q", got)
	}

	if got := spannerAutoscaling(&spanner.Instance{}); got != "off" {
		t.Errorf("autoscaling = %q, want off", got)
	}
	// Configured with no limits is still on, and a nil deref away.
	if got := spannerAutoscaling(&spanner.Instance{AutoscalingConfig: &spanner.AutoscalingConfig{}}); got != "on" {
		t.Errorf("autoscaling = %q, want on", got)
	}
}

func TestSpannerConfigTrimsThePrefix(t *testing.T) {
	tests := map[string]string{
		"projects/p/instanceConfigs/regional-us-central1": "us-central1",
		"projects/p/instanceConfigs/nam3":                 "nam3",
		"":                                                "-",
	}
	for config, want := range tests {
		if got := spannerConfig(&spanner.Instance{Config: config}); got != want {
			t.Errorf("spannerConfig(%q) = %q, want %q", config, got, want)
		}
	}
}

func TestSpannerInstanceWithoutAState(t *testing.T) {
	for _, state := range []string{"", "STATE_UNSPECIFIED"} {
		if got := spannerState(&spanner.Instance{State: state}); got != "UNKNOWN" {
			t.Errorf("spannerState(%q) = %q, want UNKNOWN", state, got)
		}
	}
}

// --- databases drill-down ---

func dbRow(d *spanner.Database) Resource {
	return spannerDatabaseResource(testProject(), "orders-prod", d)
}

func TestSpannerDatabaseResourceShape(t *testing.T) {
	r := dbRow(testSpannerDatabase())

	if r.Name != "orders" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Row[1] != "GoogleSQL" {
		t.Errorf("dialect cell = %q", r.Row[1])
	}
	if r.Row[3] != "1h" {
		t.Errorf("retention cell = %q", r.Row[3])
	}
	// The instance fixes the location; repeating it would spend a column
	// saying what the trail already says.
	if r.Location != "" {
		t.Errorf("Location = %q, want it left to the parent", r.Location)
	}
}

// TestDropProtectionOffIsTheFinding: it is off by default and is the only thing
// between a database and a one-command deletion. READY is reported either way.
func TestDropProtectionOffIsTheFinding(t *testing.T) {
	r := dbRow(testSpannerDatabase())

	if r.Status != "NO_DROP_PROTECTION" {
		t.Errorf("Status = %q, want the missing protection to outrank READY", r.Status)
	}
	if r.Row[2] != "off" {
		t.Errorf("drop protection cell = %q", r.Row[2])
	}
}

func TestDropProtectionOnIsOrdinary(t *testing.T) {
	d := testSpannerDatabase()
	d.EnableDropProtection = true

	r := dbRow(d)
	if r.Status != "READY" {
		t.Errorf("Status = %q, want READY", r.Status)
	}
	if r.Row[2] != "on" {
		t.Errorf("drop protection cell = %q", r.Row[2])
	}
}

func TestDatabaseBeingCreatedIsNotADropProtectionFinding(t *testing.T) {
	// A database mid-create has protection off too, and reporting that as a
	// finding turns a normal in-flight state into an alarm.
	d := testSpannerDatabase()
	d.State = "CREATING"

	if got := dbRow(d).Status; got != "CREATING" {
		t.Errorf("Status = %q, want the lifecycle state to win", got)
	}
}

func TestSpannerDialects(t *testing.T) {
	// The dialect decides what SQL works, and the two are not interchangeable.
	tests := map[string]string{
		"GOOGLE_STANDARD_SQL":          "GoogleSQL",
		"POSTGRESQL":                   "PostgreSQL",
		"DATABASE_DIALECT_UNSPECIFIED": "GoogleSQL",
		"":                             "GoogleSQL",
	}
	for dialect, want := range tests {
		if got := spannerDialect(&spanner.Database{DatabaseDialect: dialect}); got != want {
			t.Errorf("spannerDialect(%q) = %q, want %q", dialect, got, want)
		}
	}
}

func TestVersionRetentionFallsBackToTheRawValue(t *testing.T) {
	// The API can return "7d", which ParseDuration does not understand. Showing
	// the raw string beats dropping a retention setting on the floor.
	if got := versionRetention(&spanner.Database{VersionRetentionPeriod: "7d"}); got != "7d" {
		t.Errorf("versionRetention = %q", got)
	}
	if got := versionRetention(&spanner.Database{}); got != "-" {
		t.Errorf("versionRetention = %q, want a dash", got)
	}
}

func TestSpannerDatabaseDrillDownRejectsAWrongParent(t *testing.T) {
	_, err := (SpannerDatabaseLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-an-instance", Raw: testGKECluster()}, nil)
	if err == nil {
		t.Error("a non-Spanner parent was accepted")
	}
}

func TestSpannerRowsAreNotSSHOrAirflowTargets(t *testing.T) {
	for _, r := range []Resource{
		spannerInstanceResource(testProject(), testSpannerInstance()),
		dbRow(testSpannerDatabase()),
	} {
		if _, _, ok := SSHTarget(r); ok {
			t.Errorf("%s is an ssh target", r.Name)
		}
		if _, ok := AirflowURI(r); ok {
			t.Errorf("%s has an Airflow URI", r.Name)
		}
	}
}
