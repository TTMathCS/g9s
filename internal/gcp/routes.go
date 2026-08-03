package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// RouteLister lists the effective project route resources. Routes are global;
// their network and next hop provide the location context that matters.
type RouteLister struct{}

func (RouteLister) Kind() Kind {
	return Kind{
		ID:    "routes",
		Title: "VPC Routes",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "NETWORK", Width: 3},
			{Title: "DESTINATION", Width: 3},
			{Title: "PRIORITY", Width: 2},
			{Title: "NEXT HOP", Width: 4},
			{Title: "TYPE", Width: 2},
			{Title: "TAGS", Width: 3},
			{Title: "STATUS", Width: 2},
		},
	}
}

func (RouteLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("routes client: %w", err)
	}

	var result Result
	err = svc.Routes.List(p.ProjectID).Context(ctx).Pages(ctx, func(page *compute.RouteList) error {
		if page.Warning != nil {
			if warning, ok := computeScopeWarning("global", page.Warning.Code, page.Warning.Message); ok {
				result.Warnings = append(result.Warnings, warning)
			}
		}
		for _, route := range page.Items {
			if route != nil {
				result.Resources = append(result.Resources, routeResource(p, route))
			}
		}
		return nil
	})
	sortRouteResources(result.Resources)
	dedupeSortWarnings(&result)
	return result, err
}

func sortRouteResources(resources []Resource) {
	stableSortBy(resources, func(a, b Resource) bool {
		if a.Row[1] != b.Row[1] {
			return a.Row[1] < b.Row[1]
		}
		aRoute, aOK := a.Raw.(*compute.Route)
		bRoute, bOK := b.Raw.(*compute.Route)
		if aOK && bOK && aRoute.Priority != bRoute.Priority {
			return aRoute.Priority < bRoute.Priority
		}
		return a.Name < b.Name
	})
}

func routeResource(p config.Project, route *compute.Route) Resource {
	tags := "-"
	if len(route.Tags) > 0 {
		tags = strings.Join(route.Tags, ",")
	}

	return Resource{
		Name:     route.Name,
		Location: "global",
		Status:   routeStatus(route),
		Row: []string{
			route.Name,
			dashIfEmpty(lastSegment(route.Network)),
			dashIfEmpty(route.DestRange),
			fmt.Sprintf("%d", route.Priority),
			routeNextHop(route),
			routeKind(route),
			tags,
			routeStatus(route),
		},
		Raw: route,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/networking/routes/details/%s?project=%s",
			url.PathEscape(route.Name), url.QueryEscape(p.ProjectID)),
	}
}

func routeStatus(route *compute.Route) string {
	for _, warning := range route.Warnings {
		if warning != nil && !strings.EqualFold(warning.Code, "NO_RESULTS_ON_PAGE") {
			return "DEGRADED"
		}
	}
	if route.RouteStatus != "" {
		return route.RouteStatus
	}
	return "ACTIVE"
}

func routeNextHop(route *compute.Route) string {
	for _, candidate := range []struct {
		kind string
		ref  string
	}{
		{"gateway", route.NextHopGateway},
		{"instance", route.NextHopInstance},
		{"vpn", route.NextHopVpnTunnel},
		{"peering", route.NextHopPeering},
		{"ilb", route.NextHopIlb},
		{"interconnect", route.NextHopInterconnectAttachment},
		{"hub", route.NextHopHub},
		{"network", route.NextHopNetwork},
		{"ip", route.NextHopIp},
	} {
		if candidate.ref == "" {
			continue
		}
		name := lastSegment(candidate.ref)
		if name == "" {
			name = candidate.ref
		}
		return candidate.kind + ":" + name
	}
	return "-"
}

func routeKind(route *compute.Route) string {
	if route.RouteType != "" {
		return strings.ToLower(route.RouteType)
	}
	switch {
	case route.NextHopPeering != "":
		return "peering"
	case route.NextHopVpnTunnel != "":
		return "vpn"
	case route.NextHopIlb != "":
		return "ilb"
	case route.NextHopInstance != "":
		return "instance"
	case route.NextHopIp != "":
		return "ip"
	case route.NextHopNetwork != "":
		return "network"
	case strings.Contains(route.NextHopGateway, "default-internet-gateway"):
		return "default"
	case route.NextHopGateway != "":
		return "gateway"
	default:
		return "subnet"
	}
}
