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

// ManagedInstanceLister lists the VMs belonging to one managed instance group.
// This is a drill-down because a VM without its group, intended template and
// current repair/update action loses the context that makes the row useful.
type ManagedInstanceLister struct{}

func (ManagedInstanceLister) ParentKind() string { return "migs" }

func (ManagedInstanceLister) Kind() Kind {
	return Kind{
		ID:    "managedinstances",
		Title: "Managed Instances",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "ZONE", Width: 3},
			{Title: "INSTANCE STATUS", Width: 3},
			{Title: "ACTION", Width: 2},
			{Title: "TEMPLATE", Width: 4},
			{Title: "VERSION", Width: 2},
		},
	}
}

func (ManagedInstanceLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	manager, ok := parent.Raw.(*compute.InstanceGroupManager)
	if !ok {
		return Result{}, fmt.Errorf("no managed instance group data for %s", parent.Name)
	}

	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("managed instances client: %w", err)
	}

	if region := lastSegment(manager.Region); region != "" {
		return listRegionalManagedInstances(ctx, svc, p, manager, region)
	}
	zone := lastSegment(manager.Zone)
	if zone == "" && !isRegionScope(parent.Location) {
		zone = parent.Location
	}
	if zone == "" {
		return Result{}, fmt.Errorf("managed instance group %s has no zone or region", manager.Name)
	}
	return listZonalManagedInstances(ctx, svc, p, manager, zone)
}

func listZonalManagedInstances(ctx context.Context, svc *compute.Service, p config.Project, manager *compute.InstanceGroupManager, zone string) (Result, error) {
	var result Result
	pageToken := ""
	for {
		call := svc.InstanceGroupManagers.ListManagedInstances(p.ProjectID, zone, manager.Name).
			ReturnPartialSuccess(true).
			Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		page, err := call.Do()
		if err != nil {
			return result, err
		}
		for _, instance := range page.ManagedInstances {
			if instance != nil {
				result.Resources = append(result.Resources, managedInstanceResource(p, manager, instance))
			}
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	sortResources(result.Resources)
	return result, nil
}

func listRegionalManagedInstances(ctx context.Context, svc *compute.Service, p config.Project, manager *compute.InstanceGroupManager, region string) (Result, error) {
	var result Result
	pageToken := ""
	for {
		call := svc.RegionInstanceGroupManagers.ListManagedInstances(p.ProjectID, region, manager.Name).
			ReturnPartialSuccess(true).
			Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		page, err := call.Do()
		if err != nil {
			return result, err
		}
		for _, instance := range page.ManagedInstances {
			if instance != nil {
				result.Resources = append(result.Resources, managedInstanceResource(p, manager, instance))
			}
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	sortResources(result.Resources)
	return result, nil
}

func managedInstanceResource(p config.Project, manager *compute.InstanceGroupManager, instance *compute.ManagedInstance) Resource {
	name := instance.Name
	if name == "" {
		name = lastSegment(instance.Instance)
	}
	zone := segmentAfter(instance.Instance, "zones")

	instanceStatus := dashIfEmpty(instance.InstanceStatus)
	action := instance.CurrentAction
	if action == "" {
		action = "NONE"
	}
	status := instanceStatus
	if status == "-" {
		status = "UNKNOWN"
	}
	if !strings.EqualFold(action, "NONE") {
		status = action
	}

	template, version := "-", "-"
	if instance.Version != nil {
		template = dashIfEmpty(lastSegment(instance.Version.InstanceTemplate))
		version = dashIfEmpty(instance.Version.Name)
	}

	managerScope := lastSegment(manager.Zone)
	if managerScope == "" {
		managerScope = lastSegment(manager.Region)
	}
	consoleURL := fmt.Sprintf(
		"https://console.cloud.google.com/compute/instanceGroups/details/%s/%s?project=%s",
		url.PathEscape(managerScope), url.PathEscape(manager.Name), url.QueryEscape(p.ProjectID))
	if zone != "" && name != "" {
		consoleURL = fmt.Sprintf(
			"https://console.cloud.google.com/compute/instancesDetail/zones/%s/instances/%s?project=%s",
			url.PathEscape(zone), url.PathEscape(name), url.QueryEscape(p.ProjectID))
	}

	return Resource{
		Name:     name,
		Location: zone,
		Status:   status,
		Row: []string{
			name,
			dashIfEmpty(zone),
			instanceStatus,
			action,
			template,
			version,
		},
		Raw:        instance,
		ConsoleURL: consoleURL,
	}
}
