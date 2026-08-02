package gcp

import (
	"testing"

	compute "google.golang.org/api/compute/v1"
)

func TestManagedInstanceGroupResourceShape(t *testing.T) {
	r := managedInstanceGroupResource(testProject(), "us-central1", testInstanceGroupManager())

	if r.Name != "api-mig" || r.Location != "us-central1" {
		t.Errorf("got name=%q location=%q", r.Name, r.Location)
	}
	if r.Row[2] != "regional" || r.Row[3] != "6" {
		t.Errorf("scope/target = %q/%q", r.Row[2], r.Row[3])
	}
	if r.Row[4] != "api-v12" || r.Row[5] != "proactive" {
		t.Errorf("template/update = %q/%q", r.Row[4], r.Row[5])
	}
	if r.Status != "STABLE" {
		t.Errorf("status = %q", r.Status)
	}
}

func TestManagedInstanceGroupInFlightIsChanging(t *testing.T) {
	manager := testInstanceGroupManager()
	manager.Status.IsStable = false
	if got := managedInstanceGroupStatus(manager); got != "CHANGING" {
		t.Errorf("status = %q, want CHANGING", got)
	}
}

func TestManagedInstanceGroupUsesVersionTemplates(t *testing.T) {
	manager := &compute.InstanceGroupManager{Versions: []*compute.InstanceGroupManagerVersion{
		{InstanceTemplate: "projects/p/global/instanceTemplates/primary"},
		{InstanceTemplate: "projects/p/global/instanceTemplates/canary"},
	}}
	if got := managedInstanceGroupTemplate(manager); got != "primary,canary" {
		t.Errorf("templates = %q", got)
	}
}

func TestManagedInstanceResourceShape(t *testing.T) {
	r := managedInstanceResource(testProject(), testInstanceGroupManager(), testManagedInstance())

	if r.Name != "api-mig-2f8q" || r.Location != "us-central1-b" {
		t.Errorf("got name=%q location=%q", r.Name, r.Location)
	}
	if r.Row[2] != "RUNNING" || r.Row[3] != "NONE" {
		t.Errorf("status/action = %q/%q", r.Row[2], r.Row[3])
	}
	if r.Row[4] != "api-v12" || r.Row[5] != "primary" {
		t.Errorf("template/version = %q/%q", r.Row[4], r.Row[5])
	}
}

func TestManagedInstanceActionTakesStatusPrecedence(t *testing.T) {
	instance := testManagedInstance()
	instance.CurrentAction = "RECREATING"
	r := managedInstanceResource(testProject(), testInstanceGroupManager(), instance)
	if r.Status != "RECREATING" {
		t.Errorf("status = %q, want current action", r.Status)
	}
}
