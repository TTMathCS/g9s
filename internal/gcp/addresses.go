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

// AddressLister lists global and regional reserved IP addresses. Ephemeral
// addresses are not Address resources and therefore do not appear here; that
// distinction is the purpose of this inventory.
type AddressLister struct{}

func (AddressLister) Kind() Kind {
	return Kind{
		ID:    "addresses",
		Title: "Reserved IP Addresses",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "SCOPE", Width: 3},
			{Title: "ADDRESS", Width: 3},
			{Title: "TYPE", Width: 2},
			{Title: "PURPOSE", Width: 3},
			{Title: "TIER", Width: 2},
			{Title: "USERS", Width: 2},
			{Title: "STATUS", Width: 2},
		},
	}
}

func (AddressLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("addresses client: %w", err)
	}

	var result Result
	err = svc.Addresses.AggregatedList(p.ProjectID).
		ReturnPartialSuccess(true).
		Context(ctx).
		Pages(ctx, func(page *compute.AddressAggregatedList) error {
			appendComputeUnreachables(&result, page.Unreachables)
			if page.Warning != nil {
				if warning, ok := computeScopeWarning("all scopes", page.Warning.Code, page.Warning.Message); ok {
					result.Warnings = append(result.Warnings, warning)
				}
			}
			for scopeRef, scoped := range page.Items {
				scope := lastSegment(scopeRef)
				if scoped.Warning != nil {
					if warning, ok := computeScopeWarning(scope, scoped.Warning.Code, scoped.Warning.Message); ok {
						result.Warnings = append(result.Warnings, warning)
					}
				}
				for _, address := range scoped.Addresses {
					if address != nil {
						result.Resources = append(result.Resources, addressResource(p, scope, address))
					}
				}
			}
			return nil
		})
	sortResources(result.Resources)
	dedupeSortWarnings(&result)
	return result, err
}

func addressResource(p config.Project, scope string, address *compute.Address) Resource {
	if scope == "" {
		scope = lastSegment(address.Region)
	}
	if scope == "" {
		scope = "global"
	}

	status := address.Status
	if status == "" {
		status = "UNKNOWN"
	}

	kind := address.AddressType
	if address.IpVersion != "" {
		if kind == "" {
			kind = address.IpVersion
		} else {
			kind += "/" + address.IpVersion
		}
	}

	return Resource{
		Name:     address.Name,
		Location: scope,
		Status:   status,
		Row: []string{
			address.Name,
			scope,
			dashIfEmpty(address.Address),
			strings.ToLower(dashIfEmpty(kind)),
			strings.ToLower(dashIfEmpty(address.Purpose)),
			strings.ToLower(dashIfEmpty(address.NetworkTier)),
			fmt.Sprintf("%d", len(address.Users)),
			status,
		},
		Raw: address,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/networking/addresses/list?project=%s",
			url.QueryEscape(p.ProjectID)),
	}
}
