package gcp

import (
	"testing"
	"time"

	secretmanager "google.golang.org/api/secretmanager/v1"
)

func testSecretParent() Resource {
	return secretResource(testProject(), testSecret())
}

func TestSecretVersionResourceShape(t *testing.T) {
	r := secretVersionResource(testProject(), testSecretParent(), testSecretVersion())

	// Versions are numbered, and the number is how they are referred to in a
	// workload's config, in gcloud and in the Console.
	if r.Name != "7" {
		t.Errorf("Name = %q, want the version number", r.Name)
	}
	if r.Status != "ENABLED" {
		t.Errorf("Status = %q", r.Status)
	}
	if r.Row[2] != "18d" {
		t.Errorf("created cell = %q, want 18d", r.Row[2])
	}
	if r.Row[3] != "-" {
		t.Errorf("destroyed cell = %q, want a dash for a live version", r.Row[3])
	}
	if r.Row[4] != "google-managed" {
		t.Errorf("encryption cell = %q", r.Row[4])
	}
}

func TestSecretVersionRawCarriesNoPayload(t *testing.T) {
	// The same guarantee as the parent kind, asserted on the type that is
	// stored: a SecretVersion has no payload field, so nothing in the detail
	// pane or the clipboard can render one.
	r := secretVersionResource(testProject(), testSecretParent(), testSecretVersion())
	v, ok := r.Raw.(*secretmanager.SecretVersion)
	if !ok {
		t.Fatalf("Raw is %T, want the metadata object", r.Raw)
	}
	if v.Name == "" {
		t.Error("metadata object is empty")
	}
}

func TestScheduledDestructionIsTheWindowBeforeBreakage(t *testing.T) {
	// A version scheduled for destruction still works until the date arrives,
	// which is exactly the window where a workload pinned to it is fine and
	// about to stop being fine.
	r := secretVersionResource(testProject(), testSecretParent(), &secretmanager.SecretVersion{
		Name:                 "projects/p/secrets/s/versions/3",
		State:                "ENABLED",
		ScheduledDestroyTime: time.Now().Add(78 * time.Hour).Format(time.RFC3339),
	})
	if r.Row[3] != "in 3d" {
		t.Errorf("destroyed cell = %q, want the countdown", r.Row[3])
	}
}

func TestDestroyedVersionSaysWhen(t *testing.T) {
	r := secretVersionResource(testProject(), testSecretParent(), &secretmanager.SecretVersion{
		Name:        "projects/p/secrets/s/versions/1",
		State:       "DESTROYED",
		DestroyTime: time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
	})
	if r.Row[3] != "30d ago" {
		t.Errorf("destroyed cell = %q", r.Row[3])
	}
	if r.Status != "DESTROYED" {
		t.Errorf("Status = %q", r.Status)
	}
}

func TestSecretVersionEncryptionIsPerVersion(t *testing.T) {
	// Per version rather than per secret because a rotation can change it: a
	// secret whose newest version is on a new key while older enabled versions
	// sit on the retired one is the state a key deletion breaks.
	r := secretVersionResource(testProject(), testSecretParent(), &secretmanager.SecretVersion{
		Name: "projects/p/secrets/s/versions/9",
		CustomerManagedEncryption: &secretmanager.CustomerManagedEncryptionStatus{
			KmsKeyVersionName: "projects/p/locations/us/keyRings/r/cryptoKeys/secrets-key/cryptoKeyVersions/4",
		},
	})
	if r.Row[4] != "4" {
		t.Errorf("encryption cell = %q, want the key version", r.Row[4])
	}
}

func TestSecretVersionsSortNewestFirstNumerically(t *testing.T) {
	// Version 10 sorts before version 9 alphabetically, which puts the version
	// a workload is most likely pinned to in the wrong place.
	parent := testSecretParent()
	resources := []Resource{
		secretVersionResource(testProject(), parent, &secretmanager.SecretVersion{Name: "projects/p/secrets/s/versions/9"}),
		secretVersionResource(testProject(), parent, &secretmanager.SecretVersion{Name: "projects/p/secrets/s/versions/10"}),
		secretVersionResource(testProject(), parent, &secretmanager.SecretVersion{Name: "projects/p/secrets/s/versions/2"}),
	}
	sortSecretVersions(resources)

	want := []string{"10", "9", "2"}
	for i, w := range want {
		if resources[i].Name != w {
			t.Errorf("row %d = %q, want %q", i, resources[i].Name, w)
		}
	}
}

func TestAtoiSafe(t *testing.T) {
	tests := []struct {
		in string
		n  int
		ok bool
	}{
		{"7", 7, true},
		{"10", 10, true},
		{"", 0, false},
		{"latest", 0, false},
		{"1a", 0, false},
	}
	for _, tt := range tests {
		n, ok := atoiSafe(tt.in)
		if n != tt.n || ok != tt.ok {
			t.Errorf("atoiSafe(%q) = (%d, %v), want (%d, %v)", tt.in, n, ok, tt.n, tt.ok)
		}
	}
}

func TestSecretVersionDrillDownRejectsAParentThatIsNotASecret(t *testing.T) {
	_, err := (SecretVersionLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-a-secret", Raw: testInstance()}, nil)
	if err == nil {
		t.Error("a non-secret parent was accepted")
	}
}

func TestSecretVersionsAreNotSSHOrAirflowTargets(t *testing.T) {
	r := secretVersionResource(testProject(), testSecretParent(), testSecretVersion())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a secret version is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a secret version has an Airflow URI")
	}
}
