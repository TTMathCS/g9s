package gcp

import "testing"

func TestReservationResourceShape(t *testing.T) {
	r := reservationResource(testProject(), "us-central1-a", testReservation())

	if r.Name != "gpu-inference-capacity" || r.Location != "us-central1-a" {
		t.Errorf("got name=%q location=%q", r.Name, r.Location)
	}
	if r.Row[2] != "g2-standard-8" || r.Row[3] != "1x nvidia-l4" {
		t.Errorf("machine/accelerator = %q/%q", r.Row[2], r.Row[3])
	}
	if r.Row[4] != "2/4" || r.Row[5] != "specific" {
		t.Errorf("usage/consumption = %q/%q", r.Row[4], r.Row[5])
	}
	if r.Status != "PARTIAL" {
		t.Errorf("status = %q, want PARTIAL", r.Status)
	}
}

func TestReservationUtilizationStatuses(t *testing.T) {
	reservation := testReservation()
	reservation.SpecificReservation.InUseCount = 0
	if got := reservationStatus(reservation); got != "UNUSED" {
		t.Errorf("empty status = %q", got)
	}

	reservation.SpecificReservation.InUseCount = reservation.SpecificReservation.Count
	if got := reservationStatus(reservation); got != "IN_USE" {
		t.Errorf("full status = %q", got)
	}

	reservation.Status = "CREATING"
	if got := reservationStatus(reservation); got != "CREATING" {
		t.Errorf("API lifecycle status lost: %q", got)
	}
}
