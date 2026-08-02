package gcp

import (
	"testing"

	compute "google.golang.org/api/compute/v1"
)

func TestInstanceTemplateResourceShape(t *testing.T) {
	r := instanceTemplateResource(testProject(), "us-central1", testInstanceTemplate())

	if r.Name != "gpu-worker-v4" || r.Location != "us-central1" {
		t.Errorf("got name=%q location=%q", r.Name, r.Location)
	}
	if r.Row[2] != "g2-standard-8" || r.Row[3] != "2" || r.Row[4] != "1" {
		t.Errorf("machine/disks/nics = %q/%q/%q", r.Row[2], r.Row[3], r.Row[4])
	}
	if r.Row[5] != "1x nvidia-l4" {
		t.Errorf("accelerators = %q", r.Row[5])
	}
	if r.Status != "ACTIVE" {
		t.Errorf("status = %q", r.Status)
	}
}

func TestTemplateWithoutPropertiesUsesPlaceholders(t *testing.T) {
	r := instanceTemplateResource(testProject(), "global", &compute.InstanceTemplate{Name: "empty"})
	if r.Row[2] != "-" || r.Row[3] != "0" || r.Row[4] != "0" || r.Row[5] != "-" {
		t.Errorf("unexpected empty template row: %v", r.Row)
	}
}
