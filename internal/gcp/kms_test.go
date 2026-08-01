package gcp

import (
	"os"
	"strings"
	"testing"
	"time"

	cloudkms "google.golang.org/api/cloudkms/v1"
)

const testRing = "projects/sandbox-123/locations/us-central1/keyRings/app-secrets"

func kmsRow(k *cloudkms.CryptoKey) Resource {
	return cryptoKeyResource(testProject(), "us-central1", testRing, k)
}

func TestCryptoKeyResourceShape(t *testing.T) {
	r := kmsRow(testCryptoKey())

	if r.Name != "db-encryption" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "us-central1" {
		t.Errorf("Location = %q", r.Location)
	}
	// The ring is a column rather than a table, because a ring's own row would
	// carry a name and nothing anyone opens a tool to find.
	if r.Row[2] != "app-secrets" {
		t.Errorf("key ring cell = %q", r.Row[2])
	}
	if r.Row[3] != "symmetric" {
		t.Errorf("purpose cell = %q", r.Row[3])
	}
	if r.Row[6] != "software" {
		t.Errorf("protection level cell = %q", r.Row[6])
	}
	if r.Status != "ENABLED" {
		t.Errorf("Status = %q", r.Status)
	}
}

// TestRotationPeriodIsRenderedAsDays: the API returns "7776000s", which is
// ninety days and reads as nothing at all.
func TestRotationPeriodIsRenderedAsDays(t *testing.T) {
	if got := keyRotation(testCryptoKey()); got != "90d" {
		t.Errorf("keyRotation = %q, want 90d", got)
	}
}

// TestKeyWithNoRotationIsTheFinding is what the kind exists for. A symmetric
// key with rotation never configured reports ENABLED forever, which is exactly
// what hides it in a table of forty keys.
func TestKeyWithNoRotationIsTheFinding(t *testing.T) {
	k := testCryptoKey()
	k.RotationPeriod = ""
	k.NextRotationTime = ""

	r := kmsRow(k)
	if r.Status != "ROTATION_OFF" {
		t.Errorf("Status = %q, want the rotation finding to outrank ENABLED", r.Status)
	}
	if r.Row[4] != "never" {
		t.Errorf("rotation cell = %q, want never", r.Row[4])
	}
	if r.Row[5] != "-" {
		t.Errorf("next cell = %q, want a dash with nothing scheduled", r.Row[5])
	}
}

func TestOverdueRotationIsFlagged(t *testing.T) {
	// Rotation configured and the date passed: worse than never configured,
	// because someone did set it up and nothing enforced it.
	k := testCryptoKey()
	k.NextRotationTime = time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339)

	r := kmsRow(k)
	if r.Status != "ROTATION_OVERDUE" {
		t.Errorf("Status = %q, want ROTATION_OVERDUE", r.Status)
	}
	if !strings.HasSuffix(r.Row[5], " ago") {
		t.Errorf("next cell = %q, want it to say how overdue", r.Row[5])
	}
}

// TestAsymmetricKeysAreNotFlaggedForRotation is the false positive worth
// avoiding. KMS cannot rotate an asymmetric signing key on a schedule — its
// public half is pinned by whoever verifies signatures — so reporting one as
// unrotated invents a finding, and a column of invented findings is a column
// nobody reads when a real one appears.
func TestAsymmetricKeysAreNotFlaggedForRotation(t *testing.T) {
	for _, purpose := range []string{"ASYMMETRIC_SIGN", "ASYMMETRIC_DECRYPT"} {
		k := testCryptoKey()
		k.Purpose = purpose
		k.RotationPeriod = ""
		k.NextRotationTime = ""

		r := kmsRow(k)
		if r.Status == "ROTATION_OFF" || r.Status == "ROTATION_OVERDUE" {
			t.Errorf("%s reported as %q — automatic rotation is not available for it", purpose, r.Status)
		}
		if r.Row[4] != "n/a" {
			t.Errorf("%s rotation cell = %q, want n/a rather than never", purpose, r.Row[4])
		}
	}
}

func TestRotatableCoversTheSymmetricPurposes(t *testing.T) {
	// Anything that can rotate must be listed, or a real finding goes unreported
	// as "n/a" — the silent direction of this bug.
	for _, purpose := range []string{"ENCRYPT_DECRYPT", "RAW_ENCRYPT_DECRYPT", "MAC"} {
		if !rotatable(purpose) {
			t.Errorf("%s should be rotatable", purpose)
		}
	}
	for _, purpose := range []string{"ASYMMETRIC_SIGN", "ASYMMETRIC_DECRYPT", "", "CRYPTO_KEY_PURPOSE_UNSPECIFIED"} {
		if rotatable(purpose) {
			t.Errorf("%s should not be rotatable", purpose)
		}
	}
}

func TestKeyWithNoPrimaryVersion(t *testing.T) {
	// Asymmetric keys have no primary — every version is addressed explicitly —
	// so there is no state to report and a nil deref to avoid.
	k := testCryptoKey()
	k.Purpose = "ASYMMETRIC_SIGN"
	k.Primary = nil
	k.RotationPeriod = ""

	r := kmsRow(k)
	if r.Status != "-" {
		t.Errorf("Status = %q, want a dash", r.Status)
	}
	// The version template still carries the protection level.
	if r.Row[6] != "software" {
		t.Errorf("protection level cell = %q", r.Row[6])
	}
}

func TestProtectionLevelFallsBackToThePrimary(t *testing.T) {
	// An HSM key is a different security posture from a software one, so the
	// column must not go blank just because the template is absent.
	k := testCryptoKey()
	k.VersionTemplate = nil
	k.Primary.ProtectionLevel = "HSM"

	if got := keyProtectionLevel(k); got != "hsm" {
		t.Errorf("keyProtectionLevel = %q, want hsm", got)
	}

	k.Primary = nil
	if got := keyProtectionLevel(k); got != "-" {
		t.Errorf("keyProtectionLevel = %q, want a dash", got)
	}
}

func TestUnparseableRotationPeriodIsShownRaw(t *testing.T) {
	// Better to show the API's own string than to drop a rotation setting on
	// the floor because it did not parse.
	k := testCryptoKey()
	k.RotationPeriod = "P90D"

	if got := keyRotation(k); got != "P90D" {
		t.Errorf("keyRotation = %q, want the raw value", got)
	}
}

func TestOverdueIgnoresJunkTimestamps(t *testing.T) {
	if overdue("") {
		t.Error("an empty rotation time counts as overdue")
	}
	if overdue("not-a-time") {
		t.Error("an unparseable rotation time counts as overdue")
	}
	if overdue(time.Now().Add(time.Hour).Format(time.RFC3339)) {
		t.Error("a future rotation time counts as overdue")
	}
}

func TestShortPurposeCoversTheConstants(t *testing.T) {
	tests := map[string]string{
		"ENCRYPT_DECRYPT":     "symmetric",
		"RAW_ENCRYPT_DECRYPT": "raw symmetric",
		"ASYMMETRIC_SIGN":     "sign",
		"ASYMMETRIC_DECRYPT":  "asym decrypt",
		"MAC":                 "mac",
		"":                    "-",
		// An unknown purpose is a new one, and lowercasing it beats hiding it.
		"SOMETHING_NEW": "something_new",
	}
	for purpose, want := range tests {
		if got := shortPurpose(purpose); got != want {
			t.Errorf("shortPurpose(%q) = %q, want %q", purpose, got, want)
		}
	}
}

func TestKMSConsoleURLAddressesTheKey(t *testing.T) {
	r := kmsRow(testCryptoKey())
	if !strings.Contains(r.ConsoleURL, "/us-central1/app-secrets/db-encryption") {
		t.Errorf("console URL = %q, want location, ring and key in it", r.ConsoleURL)
	}
}

// TestKMSNeverReadsKeyMaterial is the same guarantee the Secret Manager kind
// makes, checked the same structural way. A listing has no business calling
// Decrypt or AsymmetricSign, and the failure mode if one ever does is silent.
func TestKMSNeverReadsKeyMaterial(t *testing.T) {
	raw, err := os.ReadFile("kms.go")
	if err != nil {
		t.Fatalf("reading kms.go: %v", err)
	}
	source := string(raw)

	for _, forbidden := range []string{
		".Decrypt(", ".Encrypt(", ".AsymmetricSign(", ".AsymmetricDecrypt(",
		".MacSign(", ".RawDecrypt(", ".GetPublicKey(",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("kms.go calls %s — a listing must stay metadata-only", forbidden)
		}
	}
}

func TestKMSKeysAreNotSSHOrAirflowTargets(t *testing.T) {
	r := kmsRow(testCryptoKey())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a KMS key is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a KMS key has an Airflow URI")
	}
}
