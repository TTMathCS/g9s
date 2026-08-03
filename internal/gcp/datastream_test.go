package gcp

import (
	"testing"

	datastream "google.golang.org/api/datastream/v1"
)

func streamRow(s *datastream.Stream) Resource {
	return streamResource(testProject(), s)
}

func TestStreamResourceShape(t *testing.T) {
	r := streamRow(testStream())

	if r.Name != "orders-cdc" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "us-central1" {
		t.Errorf("Location = %q", r.Location)
	}
	if r.Row[2] != "mysql: orders-mysql" {
		t.Errorf("source cell = %q, want the type and the profile", r.Row[2])
	}
	if r.Row[3] != "bigquery: warehouse-bq" {
		t.Errorf("destination cell = %q", r.Row[3])
	}
	if r.Row[4] != "all" {
		t.Errorf("backfill cell = %q", r.Row[4])
	}
	if r.Status != "RUNNING" {
		t.Errorf("Status = %q", r.Status)
	}
}

// TestErrorsOutrankTheState is the failure the state column hides. A stream
// carries an Errors list independently of its state, so it can be RUNNING and
// failing every row it reads.
func TestErrorsOutrankTheState(t *testing.T) {
	s := testStream()
	s.Errors = []*datastream.Error{{Reason: "PERMISSION_DENIED", Message: "no replication slot"}}

	r := streamRow(s)
	if r.Status != "STREAM_ERRORS" {
		t.Errorf("Status = %q, want the errors to outrank RUNNING", r.Status)
	}
	// The state column still reports what the stream itself says.
	if r.Row[5] != "RUNNING" {
		t.Errorf("state cell = %q, want the stream's own state kept", r.Row[5])
	}
}

// TestPausedStreamIsTheQuietFailure: no error, no alert, the destination just
// stops receiving rows while dashboards built on it go stale without emptying.
func TestPausedStreamIsTheQuietFailure(t *testing.T) {
	s := testStream()
	s.State = "PAUSED"

	if got := streamRow(s).Status; got != "PAUSED" {
		t.Errorf("Status = %q, want PAUSED", got)
	}
}

// TestBackfillNoneMeansNoHistory: a stream created without backfill has nothing
// older than itself in the destination, which surprises whoever queries last
// month for the first time.
func TestBackfillNoneMeansNoHistory(t *testing.T) {
	s := testStream()
	s.BackfillAll = nil
	s.BackfillNone = &datastream.BackfillNoneStrategy{}

	if got := streamRow(s).Row[4]; got != "none — changes only" {
		t.Errorf("backfill cell = %q", got)
	}
}

func TestBackfillUnsetIsADash(t *testing.T) {
	s := testStream()
	s.BackfillAll = nil
	if got := streamRow(s).Row[4]; got != "-" {
		t.Errorf("backfill cell = %q, want a dash", got)
	}
}

func TestStreamSourceTypes(t *testing.T) {
	// Which source it is changes what a failure means: a MySQL binlog gap is a
	// different problem from an Oracle privilege.
	tests := []struct {
		name string
		cfg  *datastream.SourceConfig
		want string
	}{
		{"mysql", &datastream.SourceConfig{MysqlSourceConfig: &datastream.MysqlSourceConfig{}}, "mysql"},
		{"oracle", &datastream.SourceConfig{OracleSourceConfig: &datastream.OracleSourceConfig{}}, "oracle"},
		{"postgres", &datastream.SourceConfig{PostgresqlSourceConfig: &datastream.PostgresqlSourceConfig{}}, "postgres"},
		{"sqlserver", &datastream.SourceConfig{SqlServerSourceConfig: &datastream.SqlServerSourceConfig{}}, "sqlserver"},
		// A source type this lister does not name yet still has a profile, and
		// showing that beats showing a dash.
		{"unknown with profile", &datastream.SourceConfig{
			SourceConnectionProfile: "projects/p/locations/l/connectionProfiles/mongo-src"}, "mongo-src"},
		{"nothing at all", &datastream.SourceConfig{}, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := testStream()
			s.SourceConfig = tt.cfg
			if got := streamSource(s); got != tt.want {
				t.Errorf("streamSource = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStreamWithNoConfigs(t *testing.T) {
	// Nil nested structs on a generated REST type are the standard way these
	// listers panic.
	s := &datastream.Stream{Name: "projects/p/locations/us-central1/streams/orphan"}

	r := streamRow(s)
	if r.Row[2] != "-" {
		t.Errorf("source cell = %q, want a dash", r.Row[2])
	}
	if r.Row[3] != "-" {
		t.Errorf("destination cell = %q, want a dash", r.Row[3])
	}
	if r.Status != "UNKNOWN" {
		t.Errorf("Status = %q, want UNKNOWN", r.Status)
	}
}

func TestStreamDestinationTypes(t *testing.T) {
	s := testStream()
	s.DestinationConfig = &datastream.DestinationConfig{
		GcsDestinationConfig:         &datastream.GcsDestinationConfig{},
		DestinationConnectionProfile: "projects/p/locations/l/connectionProfiles/lake",
	}
	if got := streamDestination(s); got != "gcs: lake" {
		t.Errorf("streamDestination = %q", got)
	}
}

func TestStreamsAreNotSSHOrAirflowTargets(t *testing.T) {
	// Datastream is often driven from Composer, so this is worth pinning.
	r := streamRow(testStream())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a stream is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a stream has an Airflow URI")
	}
}
