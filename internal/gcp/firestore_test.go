package gcp

import (
	"testing"

	firestore "google.golang.org/api/firestore/v1"
)

func fsRow(d *firestore.GoogleFirestoreAdminV1Database) Resource {
	return firestoreResource(testProject(), d)
}

func TestFirestoreResourceShape(t *testing.T) {
	r := fsRow(testFirestoreDatabase())

	if r.Name != "(default)" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "nam5" {
		t.Errorf("Location = %q", r.Location)
	}
	if r.Row[2] != "native" {
		t.Errorf("mode cell = %q", r.Row[2])
	}
	if r.Row[5] != "pessimistic" {
		t.Errorf("concurrency cell = %q", r.Row[5])
	}
}

// TestBothRecoveryGuardsOffIsTheFinding: neither is on by default, and both
// report nothing at all when off.
func TestDeleteProtectionOffIsTheFinding(t *testing.T) {
	r := fsRow(testFirestoreDatabase())

	if r.Status != "NO_DELETE_PROTECTION" {
		t.Errorf("Status = %q, want the missing guard named", r.Status)
	}
	if r.Row[4] != "off" {
		t.Errorf("delete protection cell = %q", r.Row[4])
	}
}

// TestDeleteProtectionOutranksPITR: losing the database outright is worse than
// being unable to rewind it, so a database with neither names the worse one.
func TestDeleteProtectionOutranksPITR(t *testing.T) {
	d := testFirestoreDatabase()
	d.PointInTimeRecoveryEnablement = "POINT_IN_TIME_RECOVERY_ENABLED"

	if got := fsRow(d).Status; got != "NO_DELETE_PROTECTION" {
		t.Errorf("Status = %q, want delete protection to lead", got)
	}
}

func TestMissingPITRIsReportedOnceProtectedFromDeletion(t *testing.T) {
	d := testFirestoreDatabase()
	d.DeleteProtectionState = "DELETE_PROTECTION_ENABLED"

	r := fsRow(d)
	if r.Status != "NO_PITR" {
		t.Errorf("Status = %q, want the recovery gap surfaced", r.Status)
	}
	if r.Row[3] != "off" {
		t.Errorf("pitr cell = %q", r.Row[3])
	}
}

func TestDatabaseWithBothGuardsOnIsOrdinary(t *testing.T) {
	d := testFirestoreDatabase()
	d.DeleteProtectionState = "DELETE_PROTECTION_ENABLED"
	d.PointInTimeRecoveryEnablement = "POINT_IN_TIME_RECOVERY_ENABLED"

	r := fsRow(d)
	if r.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE", r.Status)
	}
	if r.Row[3] != "on (7d)" {
		t.Errorf("pitr cell = %q, want the retention window", r.Row[3])
	}
	if r.Row[4] != "on" {
		t.Errorf("delete protection cell = %q", r.Row[4])
	}
}

// TestUnsetGuardsCountAsOff is the safe direction. An empty enablement field
// means the API did not report the guard as on, and treating that as protected
// would hide the exact case this kind exists to show.
func TestUnsetGuardsCountAsOff(t *testing.T) {
	d := &firestore.GoogleFirestoreAdminV1Database{Name: "projects/p/databases/(default)"}

	if deleteProtectionEnabled(d) {
		t.Error("an unset delete protection state reads as protected")
	}
	if pitrEnabled(d) {
		t.Error("an unset PITR state reads as enabled")
	}
}

// TestModeIsWhichProductThisIs: a Datastore-mode database does not speak the
// Firestore client libraries and cannot be switched after creation.
func TestFirestoreModes(t *testing.T) {
	tests := map[string]string{
		"FIRESTORE_NATIVE":          "native",
		"DATASTORE_MODE":            "datastore mode",
		"DATABASE_TYPE_UNSPECIFIED": "-",
		"":                          "-",
	}
	for typ, want := range tests {
		got := firestoreMode(&firestore.GoogleFirestoreAdminV1Database{Type: typ})
		if got != want {
			t.Errorf("firestoreMode(%q) = %q, want %q", typ, got, want)
		}
	}
}

func TestFirestoreConcurrencyModes(t *testing.T) {
	tests := map[string]string{
		"OPTIMISTIC":                    "optimistic",
		"PESSIMISTIC":                   "pessimistic",
		"OPTIMISTIC_WITH_ENTITY_GROUPS": "optimistic (entity groups)",
		"CONCURRENCY_MODE_UNSPECIFIED":  "-",
		"":                              "-",
	}
	for mode, want := range tests {
		got := firestoreConcurrency(&firestore.GoogleFirestoreAdminV1Database{ConcurrencyMode: mode})
		if got != want {
			t.Errorf("firestoreConcurrency(%q) = %q, want %q", mode, got, want)
		}
	}
}

func TestFirestoreWithoutALocation(t *testing.T) {
	d := testFirestoreDatabase()
	d.LocationId = ""
	if got := fsRow(d).Row[1]; got != "-" {
		t.Errorf("location cell = %q, want a dash", got)
	}
}

func TestFirestoreDatabasesAreNotSSHOrAirflowTargets(t *testing.T) {
	r := fsRow(testFirestoreDatabase())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a Firestore database is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a Firestore database has an Airflow URI")
	}
}
