package gcp

import (
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
)

func TestAttachedDiskListingCostsNoAPICall(t *testing.T) {
	// aggregatedList already returned the attachments, so opening them asks
	// nothing new. A nil client and nil options here is the assertion.
	parent := instanceResource(testProject(), "us-central1-a", testInstance())

	result, err := (AttachedDiskLister{}).List(t.Context(), nil, testProject(), parent, nil)
	if err != nil {
		t.Fatalf("disks drill-down: %v", err)
	}
	if len(result.Resources) != 2 {
		t.Fatalf("listed %d disks, want 2", len(result.Resources))
	}
	// Attachment order, not name order: index 0 is the boot disk and the rest
	// are in the order the guest sees them.
	if result.Resources[0].Name != "persistent-disk-0" {
		t.Errorf("first row = %q, want the boot attachment", result.Resources[0].Name)
	}
}

func TestAttachedDiskResourceShape(t *testing.T) {
	inst := testInstance()
	r := attachedDiskResource(testProject(), inst, inst.GetDisks()[0])

	// The device name is what the guest sees under /dev/disk/by-id/google-*,
	// which is the name someone reading a df output is holding.
	if r.Name != "persistent-disk-0" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Row[1] != "web-01-boot" {
		t.Errorf("source cell = %q", r.Row[1])
	}
	if r.Row[2] != "50GB" {
		t.Errorf("size cell = %q", r.Row[2])
	}
	if r.Row[3] != "boot" {
		t.Errorf("type cell = %q", r.Row[3])
	}
	if r.Row[6] != "google-managed" {
		t.Errorf("encryption cell = %q", r.Row[6])
	}
}

func TestAutoDeleteIsTheSettingThatDestroysData(t *testing.T) {
	// The one setting on an attachment that loses a disk, so it earns both a
	// column and the row's status.
	inst := testInstance()

	boot := attachedDiskResource(testProject(), inst, inst.GetDisks()[0])
	if boot.Row[5] != "delete" || boot.Status != "AUTO_DELETE" {
		t.Errorf("boot disk = %q/%q, want delete and AUTO_DELETE", boot.Row[5], boot.Status)
	}

	data := attachedDiskResource(testProject(), inst, inst.GetDisks()[1])
	if data.Row[5] != "keep" || data.Status != "ACTIVE" {
		t.Errorf("data disk = %q/%q, want keep and ACTIVE", data.Row[5], data.Status)
	}
}

func TestLocalSSDIsNamedForWhatItIs(t *testing.T) {
	// A local SSD vanishes when the instance stops. A row that looks like an
	// ordinary data disk is how someone puts state on one.
	r := attachedDiskResource(testProject(), testInstance(), &computepb.AttachedDisk{
		DeviceName: strPtr("local-ssd-0"),
		Type:       strPtr("SCRATCH"),
		Index:      int32Ptr(2),
	})

	if r.Row[3] != "local ssd" {
		t.Errorf("type cell = %q", r.Row[3])
	}
	if r.Status != "EPHEMERAL" {
		t.Errorf("Status = %q, want EPHEMERAL", r.Status)
	}
	// No source disk exists for one, and a dash is more honest than a blank.
	if r.Row[1] != "-" {
		t.Errorf("source cell = %q, want a dash", r.Row[1])
	}
}

func TestReadOnlyAttachmentIsFlagged(t *testing.T) {
	// A disk shared read-only between instances is deliberate, and a surprise
	// if it is not.
	r := attachedDiskResource(testProject(), testInstance(), &computepb.AttachedDisk{
		DeviceName: strPtr("shared"),
		Source:     strPtr("projects/p/zones/z/disks/reference-data"),
		Mode:       strPtr("READ_ONLY"),
		Type:       strPtr("PERSISTENT"),
	})
	if r.Status != "READ_ONLY" {
		t.Errorf("Status = %q", r.Status)
	}
	if r.Row[4] != "read_only" {
		t.Errorf("mode cell = %q", r.Row[4])
	}
}

func TestDiskEncryption(t *testing.T) {
	tests := []struct {
		name string
		in   *computepb.AttachedDisk
		want string
	}{
		{"default", &computepb.AttachedDisk{}, "google-managed"},
		{"cmek", &computepb.AttachedDisk{
			DiskEncryptionKey: &computepb.CustomerEncryptionKey{
				KmsKeyName: strPtr("projects/p/locations/us/keyRings/r/cryptoKeys/disk-key"),
			},
		}, "disk-key"},
		// Customer-supplied is a different thing from customer-managed: Google
		// never stores the key, so the disk cannot be read without it being
		// handed over on every attach.
		{"customer supplied", &computepb.AttachedDisk{
			DiskEncryptionKey: &computepb.CustomerEncryptionKey{Sha256: strPtr("abc123")},
		}, "customer-supplied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diskEncryption(tt.in); got != tt.want {
				t.Errorf("diskEncryption = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAttachedDiskFallsBackForItsName(t *testing.T) {
	// Neither a device name nor a source leaves nothing identifying the row,
	// and the index is at least unique within the instance.
	r := attachedDiskResource(testProject(), testInstance(),
		&computepb.AttachedDisk{Index: int32Ptr(3)})
	if r.Name != "disk-3" {
		t.Errorf("Name = %q, want the index to stand in", r.Name)
	}
}

func TestDiskDrillDownRejectsAParentThatIsNotAnInstance(t *testing.T) {
	_, err := (AttachedDiskLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-an-instance", Raw: testGKECluster()}, nil)
	if err == nil {
		t.Error("a non-instance parent was accepted")
	}
}

func TestAttachedDisksAreNotSSHTargets(t *testing.T) {
	// The parent VM is an ssh target and these rows carry its zone, so this is
	// worth pinning: s must not offer to shell into a disk.
	inst := testInstance()
	r := attachedDiskResource(testProject(), inst, inst.GetDisks()[0])
	if _, _, ok := SSHTarget(r); ok {
		t.Error("an attached disk is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("an attached disk has an Airflow URI")
	}
}
