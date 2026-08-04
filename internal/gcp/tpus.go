package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/api/option"
	tpu "google.golang.org/api/tpu/v2"

	"github.com/TTMathCS/g9s/internal/config"
)

// TPULister lists Cloud TPU nodes.
//
// TPUs are the most expensive thing most projects can accidentally leave
// running, by a wide margin — a v5 pod slice costs more per hour than the rest
// of a small project costs per month. The failure this table is for is not a
// crash: it is a node in READY that finished its training run on Tuesday and
// has been ready ever since.
//
// So the row leads with accelerator type and state, and PREEMPTED gets its own
// treatment: a preemptible TPU that was reclaimed is not running your job and
// is not going to start again on its own, while its state alone reads as
// something that might recover.
type TPULister struct{}

func (TPULister) Kind() Kind {
	return Kind{
		ID:    "tpus",
		Title: "Cloud TPUs",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "ZONE", Width: 2},
			{Title: "ACCELERATOR", Width: 3},
			{Title: "RUNTIME", Width: 3},
			{Title: "PREEMPTIBLE", Width: 2},
			{Title: "STATE", Width: 2},
		},
	}
}

func (TPULister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	regions := cfg.Regions(p)
	if len(regions) == 0 {
		return Result{Warnings: []Warning{narrowedWarning("no regions configured — set projects[].regions or defaults.regions")}}, nil
	}

	svc, err := tpu.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("tpu client: %w", err)
	}

	// The `-` wildcard asks each region for every zone in it, which is the
	// difference between one call per region and one per zone — and the zone
	// list is not something the config knows.
	return fanOut(ctx, regions, func(ctx context.Context, region string) (Result, error) {
		parent := fmt.Sprintf("projects/%s/locations/%s-", p.ProjectID, region)

		var out Result
		err := svc.Projects.Locations.Nodes.List(parent).
			Pages(ctx, func(page *tpu.ListNodesResponse) error {
				for _, n := range page.Nodes {
					if n != nil {
						out.Resources = append(out.Resources, tpuResource(p, region, n))
					}
				}
				return nil
			})
		return out, err
	}), nil
}

func tpuResource(p config.Project, region string, n *tpu.Node) Resource {
	name := lastSegment(n.Name)
	zone := segmentAfter(n.Name, "locations")
	if zone == "" {
		zone = region
	}

	state := n.State
	if state == "" || state == "STATE_UNSPECIFIED" {
		state = "UNKNOWN"
	}

	preemptible := "no"
	if n.SchedulingConfig != nil && n.SchedulingConfig.Preemptible {
		preemptible = "yes"
	}

	return Resource{
		Name:     name,
		Location: zone,
		Status:   tpuStatus(n, state),
		Row: []string{
			name,
			zone,
			orDash(n.AcceleratorType),
			orDash(n.RuntimeVersion),
			preemptible,
			state,
		},
		Raw: n,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/compute/tpus/detail/%s/%s?project=%s",
			url.PathEscape(zone), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// tpuStatus keeps PREEMPTED distinct from every other non-running state.
//
// A reclaimed preemptible TPU is not coming back without someone recreating
// it, and anything waiting on its output is waiting forever. Every other
// stopped state is either deliberate or in motion.
func tpuStatus(n *tpu.Node, state string) string {
	if strings.EqualFold(state, "PREEMPTED") {
		return "PREEMPTED"
	}
	return state
}

// orDash renders an empty API field as the table's placeholder rather than as
// a blank cell, which reads as a rendering fault.
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
