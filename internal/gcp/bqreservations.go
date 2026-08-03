package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	bigqueryreservation "google.golang.org/api/bigqueryreservation/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// bigQueryMultiRegions are the two locations most reservations live in.
//
// BigQuery locations are not Compute regions. A reservation is created in `US`
// or `EU` far more often than in a named region, and neither appears in anyone's
// `regions` config — so sweeping only the configured regions would show an empty
// table to a project whose slots are all committed.
var bigQueryMultiRegions = []string{"US", "EU"}

// BigQueryReservationLister lists BigQuery slot reservations.
//
// Location-scoped, and the location axis is BigQuery's own rather than
// Compute's, so this sweeps the configured regions plus `US` and `EU` — the
// same shape as Dataproc always including `global`, and for the same reason:
// the place the resource is most likely to be is the one nobody lists.
//
// A reservation is a standing commitment. Slots are paid for whether or not a
// query uses them, so the row that matters is one with capacity nobody is
// running against — the BigQuery equivalent of an unattached disk, and equally
// invisible from the jobs table. IDLE SLOTS says whether that capacity is at
// least being lent to other reservations rather than sitting idle.
type BigQueryReservationLister struct{}

func (BigQueryReservationLister) Kind() Kind {
	return Kind{
		ID:    "bqreservations",
		Title: "BigQuery Reservations",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "LOCATION", Width: 2},
			{Title: "SLOTS", Width: 2},
			{Title: "AUTOSCALE", Width: 3},
			{Title: "EDITION", Width: 2},
			{Title: "IDLE SLOTS", Width: 3},
		},
	}
}

func (BigQueryReservationLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := bigqueryreservation.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("bigquery reservation client: %w", err)
	}

	return fanOut(ctx, bigQueryLocations(cfg, p), func(ctx context.Context, location string) (Result, error) {
		parent := fmt.Sprintf("projects/%s/locations/%s", p.ProjectID, location)

		var out Result
		err := svc.Projects.Locations.Reservations.List(parent).
			Pages(ctx, func(page *bigqueryreservation.ListReservationsResponse) error {
				for _, r := range page.Reservations {
					if r != nil {
						out.Resources = append(out.Resources, bqReservationResource(p, location, r))
					}
				}
				return nil
			})
		return out, err
	}), nil
}

// bigQueryLocations is the configured regions plus the two multi-regions,
// deduplicated and in a stable order.
func bigQueryLocations(cfg *config.Config, p config.Project) []string {
	seen := map[string]bool{}
	var out []string
	for _, loc := range append(append([]string{}, bigQueryMultiRegions...), cfg.Regions(p)...) {
		if loc == "" || seen[loc] {
			continue
		}
		seen[loc] = true
		out = append(out, loc)
	}
	return out
}

func bqReservationResource(p config.Project, location string, r *bigqueryreservation.Reservation) Resource {
	name := lastSegment(r.Name)

	return Resource{
		Name:     name,
		Location: location,
		Status:   bqReservationStatus(r),
		Row: []string{
			name,
			location,
			bqReservationSlots(r),
			bqReservationAutoscale(r),
			bqReservationEdition(r),
			bqIdleSlots(r),
		},
		Raw: r,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/bigquery/admin/reservations?project=%s",
			url.QueryEscape(p.ProjectID)),
	}
}

// bqReservationStatus flags a reservation holding baseline capacity that cannot
// be shared.
//
// Slots are billed whether or not a query uses them. A reservation with a
// baseline and IgnoreIdleSlots set will not lend its unused capacity to any
// other reservation, so the slots are paid for and unavailable to everything
// else — the combination worth seeing on a row.
func bqReservationStatus(r *bigqueryreservation.Reservation) string {
	if r.SlotCapacity > 0 && r.IgnoreIdleSlots {
		return "IDLE_SLOTS_RESERVED"
	}
	return "ACTIVE"
}

// bqReservationSlots is the baseline commitment: capacity paid for continuously,
// separate from whatever autoscaling adds on top.
func bqReservationSlots(r *bigqueryreservation.Reservation) string {
	if r.SlotCapacity <= 0 {
		// Autoscale-only reservations are legitimate and have no baseline.
		return "0 baseline"
	}
	return fmt.Sprintf("%d baseline", r.SlotCapacity)
}

// bqReservationAutoscale reports the ceiling and what is currently on top of the
// baseline, which are different numbers and are billed differently.
func bqReservationAutoscale(r *bigqueryreservation.Reservation) string {
	if r.Autoscale == nil || r.Autoscale.MaxSlots <= 0 {
		return "off"
	}
	if r.Autoscale.CurrentSlots > 0 {
		return fmt.Sprintf("%d now, max %d", r.Autoscale.CurrentSlots, r.Autoscale.MaxSlots)
	}
	return fmt.Sprintf("max %d", r.Autoscale.MaxSlots)
}

func bqReservationEdition(r *bigqueryreservation.Reservation) string {
	if r.Edition == "" || r.Edition == "EDITION_UNSPECIFIED" {
		return "-"
	}
	return strings.ToLower(r.Edition)
}

// bqIdleSlots says whether unused capacity is lent to other reservations.
//
// Sharing is the default and is almost always what someone wants: the slots are
// already paid for, so letting another reservation burst into them is free.
// Turning it off is deliberate — and is also how capacity ends up idle and
// billed at the same time.
func bqIdleSlots(r *bigqueryreservation.Reservation) string {
	if r.IgnoreIdleSlots {
		return "not shared"
	}
	return "shared"
}
