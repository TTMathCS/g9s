package gcp

import (
	"context"
	"fmt"
	"net/url"

	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// RouterLister lists Cloud Routers across all regions. A router is the parent
// for two related concerns: dynamic BGP routing and one or more Cloud NAT
// configurations. The first fits in the row; the second is a drill-down.
type RouterLister struct{}

func (RouterLister) Kind() Kind {
	return Kind{
		ID:    "routers",
		Title: "Cloud Routers",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "REGION", Width: 3},
			{Title: "NETWORK", Width: 3},
			{Title: "ASN", Width: 2},
			{Title: "BGP PEERS", Width: 2},
			{Title: "INTERFACES", Width: 2},
			{Title: "NAT GATEWAYS", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (RouterLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("routers client: %w", err)
	}

	var result Result
	err = svc.Routers.AggregatedList(p.ProjectID).
		ReturnPartialSuccess(true).
		Context(ctx).
		Pages(ctx, func(page *compute.RouterAggregatedList) error {
			appendComputeUnreachables(&result, page.Unreachables)
			if page.Warning != nil {
				if warning, ok := computeScopeWarning("all scopes", page.Warning.Code, page.Warning.Message); ok {
					result.Warnings = append(result.Warnings, warning)
				}
			}
			for scopeRef, scoped := range page.Items {
				region := lastSegment(scopeRef)
				if scoped.Warning != nil {
					if warning, ok := computeScopeWarning(region, scoped.Warning.Code, scoped.Warning.Message); ok {
						result.Warnings = append(result.Warnings, warning)
					}
				}
				for _, router := range scoped.Routers {
					if router != nil {
						result.Resources = append(result.Resources, routerResource(p, region, router))
					}
				}
			}
			return nil
		})
	sortResources(result.Resources)
	dedupeSortWarnings(&result)
	return result, err
}

func routerResource(p config.Project, region string, router *compute.Router) Resource {
	if region == "" {
		region = lastSegment(router.Region)
	}

	asn := "-"
	if router.Bgp != nil && router.Bgp.Asn != 0 {
		asn = fmt.Sprintf("%d", router.Bgp.Asn)
	}

	return Resource{
		Name:     router.Name,
		Location: region,
		Status:   "ACTIVE",
		Row: []string{
			router.Name,
			region,
			dashIfEmpty(lastSegment(router.Network)),
			asn,
			fmt.Sprintf("%d", len(router.BgpPeers)),
			fmt.Sprintf("%d", len(router.Interfaces)),
			fmt.Sprintf("%d", len(router.Nats)),
			age(router.CreationTimestamp),
		},
		Raw: router,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/networking/routers/details/%s/%s?project=%s",
			url.PathEscape(region), url.PathEscape(router.Name), url.QueryEscape(p.ProjectID)),
	}
}
