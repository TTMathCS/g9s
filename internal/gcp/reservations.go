package gcp

import (
	"context"
	"fmt"
	"net/url"

	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// ReservationLister lists Compute Engine capacity reservations, including
// GPU-backed reservations. Cloud TPU reservations are a separate service and
// API; folding them into this table would make the scope and permissions lie.
type ReservationLister struct{}

func (ReservationLister) Kind() Kind {
	return Kind{
		ID:    "reservations",
		Title: "Compute Reservations",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "ZONE", Width: 3},
			{Title: "MACHINE", Width: 3},
			{Title: "ACCELERATORS", Width: 3},
			{Title: "USAGE", Width: 2},
			{Title: "CONSUMPTION", Width: 2},
			{Title: "STATUS", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (ReservationLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("reservations client: %w", err)
	}

	var result Result
	err = svc.Reservations.AggregatedList(p.ProjectID).
		ReturnPartialSuccess(true).
		Context(ctx).
		Pages(ctx, func(page *compute.ReservationAggregatedList) error {
			appendComputeUnreachables(&result, page.Unreachables)
			if page.Warning != nil {
				if warning := computeScopeWarning("all scopes", page.Warning.Code, page.Warning.Message); warning != "" {
					result.Warnings = append(result.Warnings, warning)
				}
			}
			for scopeRef, scoped := range page.Items {
				zone := lastSegment(scopeRef)
				if scoped.Warning != nil {
					if warning := computeScopeWarning(zone, scoped.Warning.Code, scoped.Warning.Message); warning != "" {
						result.Warnings = append(result.Warnings, warning)
					}
				}
				for _, reservation := range scoped.Reservations {
					if reservation != nil {
						result.Resources = append(result.Resources, reservationResource(p, zone, reservation))
					}
				}
			}
			return nil
		})
	sortResources(result.Resources)
	dedupeSortWarnings(&result)
	return result, err
}

func reservationResource(p config.Project, zone string, reservation *compute.Reservation) Resource {
	if zone == "" {
		zone = lastSegment(reservation.Zone)
	}

	return Resource{
		Name:     reservation.Name,
		Location: zone,
		Status:   reservationStatus(reservation),
		Row: []string{
			reservation.Name,
			zone,
			reservationMachine(reservation),
			reservationAccelerators(reservation),
			reservationUsage(reservation),
			reservationConsumption(reservation),
			reservationStatus(reservation),
			age(reservation.CreationTimestamp),
		},
		Raw: reservation,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/compute/reservations/detail/%s/%s?project=%s",
			url.PathEscape(zone), url.PathEscape(reservation.Name), url.QueryEscape(p.ProjectID)),
	}
}

func reservationStatus(reservation *compute.Reservation) string {
	status := reservation.Status
	if status == "" {
		status = "UNKNOWN"
	}
	specific := reservation.SpecificReservation
	if status != "READY" || specific == nil || specific.Count <= 0 {
		return status
	}
	switch {
	case specific.InUseCount == 0:
		return "UNUSED"
	case specific.InUseCount < specific.Count:
		return "PARTIAL"
	default:
		return "IN_USE"
	}
}

func reservationMachine(reservation *compute.Reservation) string {
	specific := reservation.SpecificReservation
	if specific == nil {
		return "aggregate"
	}
	if specific.InstanceProperties != nil {
		return dashIfEmpty(lastSegment(specific.InstanceProperties.MachineType))
	}
	if specific.SourceInstanceTemplate != "" {
		return "template:" + lastSegment(specific.SourceInstanceTemplate)
	}
	return "-"
}

func reservationAccelerators(reservation *compute.Reservation) string {
	if reservation.SpecificReservation == nil || reservation.SpecificReservation.InstanceProperties == nil {
		return "-"
	}
	return acceleratorSummary(reservation.SpecificReservation.InstanceProperties.GuestAccelerators)
}

func reservationUsage(reservation *compute.Reservation) string {
	if reservation.SpecificReservation == nil {
		return "aggregate"
	}
	return fmt.Sprintf("%d/%d", reservation.SpecificReservation.InUseCount, reservation.SpecificReservation.Count)
}

func reservationConsumption(reservation *compute.Reservation) string {
	if reservation.SpecificReservationRequired {
		return "specific"
	}
	return "shared"
}
