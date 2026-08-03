package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/api/option"
	spanner "google.golang.org/api/spanner/v1"

	"github.com/TTMathCS/g9s/internal/config"
)

// SpannerLister lists Spanner instances.
//
// Global: the parent is `projects/{p}` and one paginated call returns every
// instance in every config, with the ones that did not answer in Unreachable.
// No fan-out and no dependence on the regions list — an instance in a config
// nobody configured still appears.
//
// CAPACITY is the column this exists for. Spanner bills by compute capacity,
// not by storage or by query, and the unit is either nodes or processing units
// (1000 PU = 1 node) depending on how the instance was created. A row showing
// "1" where another shows "1000" is the same size, and reading one as ten times
// the other is how a capacity review reaches the wrong conclusion. Both are
// normalised to processing units, with the node equivalent alongside.
type SpannerLister struct{}

func (SpannerLister) Kind() Kind {
	return Kind{
		ID:    "spanner",
		Title: "Spanner Instances",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "CONFIG", Width: 3},
			{Title: "CAPACITY", Width: 3},
			{Title: "EDITION", Width: 2},
			{Title: "AUTOSCALING", Width: 2},
			{Title: "STATE", Width: 2},
		},
	}
}

func (SpannerLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := spanner.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("spanner client: %w", err)
	}

	var (
		result      Result
		unreachable = map[string]bool{}
	)
	err = svc.Projects.Instances.List("projects/"+p.ProjectID).
		Pages(ctx, func(page *spanner.ListInstancesResponse) error {
			for _, loc := range page.Unreachable {
				unreachable[loc] = true
			}
			for _, inst := range page.Instances {
				if inst != nil {
					result.Resources = append(result.Resources, spannerInstanceResource(p, inst))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	for _, loc := range sortedKeys(unreachable) {
		if w, ok := describeFailure(loc, fmt.Errorf("location unreachable")); ok {
			result.Warnings = append(result.Warnings, w)
		}
	}

	sortResources(result.Resources)
	return result, nil
}

func spannerInstanceResource(p config.Project, i *spanner.Instance) Resource {
	name := lastSegment(i.Name)

	return Resource{
		Name: name,
		// The instance config is where a Spanner instance lives — regional or
		// multi-region — and it is the closest thing it has to a location.
		Location: spannerConfig(i),
		Status:   spannerState(i),
		Row: []string{
			name,
			spannerConfig(i),
			spannerCapacity(i),
			spannerEdition(i),
			spannerAutoscaling(i),
			spannerState(i),
		},
		Raw: i,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/spanner/instances/%s/details/databases?project=%s",
			url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// spannerConfig trims the config to its readable tail: the API returns
// `projects/p/instanceConfigs/regional-us-central1`.
func spannerConfig(i *spanner.Instance) string {
	if i.Config == "" {
		return "-"
	}
	return strings.TrimPrefix(lastSegment(i.Config), "regional-")
}

// spannerCapacity normalises the two units Spanner bills in.
//
// An instance created with nodes reports NodeCount and one created with
// processing units reports ProcessingUnits; 1000 PU is one node. Showing
// whichever field happens to be set puts "1" and "1000" in the same column for
// two instances of identical size.
func spannerCapacity(i *spanner.Instance) string {
	units := i.ProcessingUnits
	if units == 0 && i.NodeCount > 0 {
		units = i.NodeCount * 1000
	}
	if units == 0 {
		return "-"
	}
	if units < 1000 {
		// Below one node, PU is the only unit that says anything.
		return fmt.Sprintf("%d PU", units)
	}
	if units%1000 == 0 {
		return fmt.Sprintf("%d PU (%d node%s)", units, units/1000, plural(units/1000))
	}
	return fmt.Sprintf("%d PU (%.1f nodes)", units, float64(units)/1000)
}

func plural(n int64) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func spannerEdition(i *spanner.Instance) string {
	if i.Edition == "" || i.Edition == "EDITION_UNSPECIFIED" {
		return "-"
	}
	return strings.ToLower(i.Edition)
}

// spannerAutoscaling says whether capacity moves on its own, which changes what
// the CAPACITY column means: a fixed number, or wherever it happens to be now.
func spannerAutoscaling(i *spanner.Instance) string {
	if i.AutoscalingConfig == nil {
		return "off"
	}
	limits := i.AutoscalingConfig.AutoscalingLimits
	if limits == nil {
		return "on"
	}
	switch {
	case limits.MinProcessingUnits > 0 || limits.MaxProcessingUnits > 0:
		return fmt.Sprintf("%d–%d PU", limits.MinProcessingUnits, limits.MaxProcessingUnits)
	case limits.MinNodes > 0 || limits.MaxNodes > 0:
		return fmt.Sprintf("%d–%d nodes", limits.MinNodes, limits.MaxNodes)
	default:
		return "on"
	}
}

func spannerState(i *spanner.Instance) string {
	if i.State == "" || i.State == "STATE_UNSPECIFIED" {
		return "UNKNOWN"
	}
	return i.State
}
