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

// ManagedInstanceGroupLister lists zonal and regional managed instance groups.
// One aggregated request covers both scopes; the VMs managed by a selected
// group are fetched only when the row is opened.
type ManagedInstanceGroupLister struct{}

func (ManagedInstanceGroupLister) Kind() Kind {
	return Kind{
		ID:    "migs",
		Title: "Managed Instance Groups",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "LOCATION", Width: 3},
			{Title: "SCOPE", Width: 2},
			{Title: "TARGET", Width: 2},
			{Title: "TEMPLATE", Width: 4},
			{Title: "UPDATE", Width: 2},
			{Title: "STATUS", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (ManagedInstanceGroupLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("managed instance groups client: %w", err)
	}

	var result Result
	err = svc.InstanceGroupManagers.AggregatedList(p.ProjectID).
		ReturnPartialSuccess(true).
		Context(ctx).
		Pages(ctx, func(page *compute.InstanceGroupManagerAggregatedList) error {
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
				for _, manager := range scoped.InstanceGroupManagers {
					if manager != nil {
						result.Resources = append(result.Resources, managedInstanceGroupResource(p, scope, manager))
					}
				}
			}
			return nil
		})
	sortResources(result.Resources)
	dedupeSortWarnings(&result)
	return result, err
}

func managedInstanceGroupResource(p config.Project, scope string, manager *compute.InstanceGroupManager) Resource {
	if scope == "" {
		scope = lastSegment(manager.Zone)
		if scope == "" {
			scope = lastSegment(manager.Region)
		}
	}

	update := "-"
	if manager.UpdatePolicy != nil {
		update = dashIfEmpty(strings.ToLower(manager.UpdatePolicy.Type))
	}

	return Resource{
		Name:     manager.Name,
		Location: scope,
		Status:   managedInstanceGroupStatus(manager),
		Row: []string{
			manager.Name,
			scope,
			managedInstanceGroupScope(manager, scope),
			fmt.Sprintf("%d", manager.TargetSize),
			managedInstanceGroupTemplate(manager),
			update,
			managedInstanceGroupStatus(manager),
			age(manager.CreationTimestamp),
		},
		Raw: manager,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/compute/instanceGroups/details/%s/%s?project=%s",
			url.PathEscape(scope), url.PathEscape(manager.Name), url.QueryEscape(p.ProjectID)),
	}
}

func managedInstanceGroupStatus(manager *compute.InstanceGroupManager) string {
	if manager.Status == nil {
		return "UNKNOWN"
	}
	if manager.Status.IsStable {
		return "STABLE"
	}
	return "CHANGING"
}

func managedInstanceGroupScope(manager *compute.InstanceGroupManager, scope string) string {
	if manager.Region != "" || isRegionScope(scope) {
		return "regional"
	}
	return "zonal"
}

func managedInstanceGroupTemplate(manager *compute.InstanceGroupManager) string {
	if manager.InstanceTemplate != "" {
		return lastSegment(manager.InstanceTemplate)
	}

	var templates []string
	for _, version := range manager.Versions {
		if version == nil || version.InstanceTemplate == "" {
			continue
		}
		templates = append(templates, lastSegment(version.InstanceTemplate))
	}
	if len(templates) == 0 {
		return "-"
	}
	return strings.Join(templates, ",")
}
