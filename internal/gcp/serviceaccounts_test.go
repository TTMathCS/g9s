package gcp

import (
	"strings"
	"testing"
	"time"

	iam "google.golang.org/api/iam/v1"
)

func TestServiceAccountResourceShape(t *testing.T) {
	r := serviceAccountResource(testProject(), testServiceAccount(),
		[]*iam.ServiceAccountKey{testServiceAccountKey()})

	// The domain is identical on every row in the project and would cost half
	// the column to say nothing.
	if r.Name != "etl-runner" {
		t.Errorf("Name = %q, want the email's local part", r.Name)
	}
	if r.Row[1] != "ETL runner" {
		t.Errorf("display name cell = %q", r.Row[1])
	}
	if r.Row[2] != "1" {
		t.Errorf("key count cell = %q, want 1", r.Row[2])
	}
	if r.Row[3] != "30d" {
		t.Errorf("oldest key cell = %q, want 30d", r.Row[3])
	}
	if r.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE for a freshly rotated key", r.Status)
	}
}

func TestServiceAccountWithAStaleKey(t *testing.T) {
	// The whole reason the kind exists: IAM reports the same state for an
	// account with a two-year-old downloadable key as for one with none.
	old := testServiceAccountKey()
	old.ValidAfterTime = time.Now().Add(-400 * 24 * time.Hour).Format(time.RFC3339)

	r := serviceAccountResource(testProject(), testServiceAccount(), []*iam.ServiceAccountKey{old})
	if r.Status != "STALE_KEY" {
		t.Errorf("Status = %q, want STALE_KEY", r.Status)
	}
	if r.Row[3] != "400d" {
		t.Errorf("oldest key cell = %q, want 400d", r.Row[3])
	}
}

func TestServiceAccountOldestKeyWinsOverNewest(t *testing.T) {
	// An account rotated last week is not clean if the key from three years
	// ago was never deleted — which is the usual shape of this finding.
	fresh := testServiceAccountKey()
	stale := testServiceAccountKey()
	stale.ValidAfterTime = time.Now().Add(-1000 * 24 * time.Hour).Format(time.RFC3339)

	r := serviceAccountResource(testProject(), testServiceAccount(),
		[]*iam.ServiceAccountKey{fresh, stale})
	if r.Status != "STALE_KEY" {
		t.Errorf("Status = %q — the newest key hid the oldest one", r.Status)
	}
	if r.Row[2] != "2" {
		t.Errorf("key count cell = %q, want 2", r.Row[2])
	}
}

func TestServiceAccountWithNoKeysIsNotTheSameAsUnread(t *testing.T) {
	// An empty slice is an answer; a nil one means the lookup was denied or
	// past the cap. Showing 0 for the second would clear an account that may
	// well be carrying a five-year-old key.
	none := serviceAccountResource(testProject(), testServiceAccount(), []*iam.ServiceAccountKey{})
	if none.Row[2] != "0" || none.Row[3] != "-" {
		t.Errorf("no keys rendered as %q/%q, want 0 and a dash", none.Row[2], none.Row[3])
	}
	if none.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE for an account with no keys", none.Status)
	}

	unread := serviceAccountResource(testProject(), testServiceAccount(), nil)
	if unread.Row[2] != "?" || unread.Row[3] != "?" {
		t.Errorf("unread keys rendered as %q/%q, want ? for both", unread.Row[2], unread.Row[3])
	}
}

func TestDisabledAccountOutranksAStaleKey(t *testing.T) {
	// A disabled account cannot authenticate at all, so the age of a key it
	// cannot use is the smaller of the two problems.
	a := testServiceAccount()
	a.Disabled = true
	old := testServiceAccountKey()
	old.ValidAfterTime = time.Now().Add(-400 * 24 * time.Hour).Format(time.RFC3339)

	r := serviceAccountResource(testProject(), a, []*iam.ServiceAccountKey{old})
	if r.Status != "DISABLED" {
		t.Errorf("Status = %q, want DISABLED", r.Status)
	}
}

func TestServiceAccountRawCarriesTheKeysForTheDrillDown(t *testing.T) {
	r := serviceAccountResource(testProject(), testServiceAccount(),
		[]*iam.ServiceAccountKey{testServiceAccountKey()})

	detail, ok := r.Raw.(*ServiceAccountDetail)
	if !ok {
		t.Fatalf("Raw is %T, want *ServiceAccountDetail", r.Raw)
	}
	if len(detail.Keys) != 1 {
		t.Errorf("Raw carries %d keys, want 1 — the drill-down reads these", len(detail.Keys))
	}

	// And the drill-down does read them, without a client.
	result, err := (ServiceAccountKeyLister{}).List(t.Context(), nil, testProject(), r, nil)
	if err != nil {
		t.Fatalf("keys drill-down: %v", err)
	}
	if len(result.Resources) != 1 {
		t.Fatalf("drill-down listed %d keys, want 1", len(result.Resources))
	}
	if got := result.Resources[0].Name; got != "9f8e7d6c5b4a" {
		t.Errorf("key name = %q, want the key id", got)
	}
}

func TestKeyDrillDownSortsOldestFirst(t *testing.T) {
	fresh := testServiceAccountKey()
	stale := testServiceAccountKey()
	stale.Name = strings.Replace(stale.Name, "9f8e7d6c5b4a", "0000oldest0000", 1)
	stale.ValidAfterTime = time.Now().Add(-900 * 24 * time.Hour).Format(time.RFC3339)

	parent := serviceAccountResource(testProject(), testServiceAccount(),
		[]*iam.ServiceAccountKey{fresh, stale})
	result, err := (ServiceAccountKeyLister{}).List(t.Context(), nil, testProject(), parent, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The oldest key is the one the table was opened to find.
	if result.Resources[0].Name != "0000oldest0000" {
		t.Errorf("first row = %q, want the oldest key", result.Resources[0].Name)
	}
}

func TestKeyExpiryReadsAsSomethingAHumanWrote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// IAM writes year 9999 for a key that never expires, which renders as
		// a nonsense age unless it is named.
		{"never expires", "9999-12-31T23:59:59Z", "never"},
		{"none reported", "", "-"},
		{"already past", time.Now().Add(-time.Hour).Format(time.RFC3339), "expired"},
		// Offset past the day boundary: RFC3339 drops sub-second precision, so
		// exactly 48h formats to a hair under two days and renders as "1d".
		{"ahead", time.Now().Add(54 * time.Hour).Format(time.RFC3339), "2d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expiryCell(tt.in); got != tt.want {
				t.Errorf("expiryCell(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExpiredKeyReportsItself(t *testing.T) {
	k := testServiceAccountKey()
	k.ValidBeforeTime = time.Now().Add(-24 * time.Hour).Format(time.RFC3339)

	parent := serviceAccountResource(testProject(), testServiceAccount(), nil)
	r := serviceAccountKeyResource(testProject(), parent, k)
	if r.Status != "EXPIRED" {
		t.Errorf("Status = %q, want EXPIRED", r.Status)
	}
}

func TestServiceAccountsAreNotSSHOrAirflowTargets(t *testing.T) {
	r := serviceAccountResource(testProject(), testServiceAccount(), nil)
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a service account is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a service account has an Airflow URI")
	}
}
