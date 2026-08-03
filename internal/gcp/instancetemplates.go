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

// InstanceTemplateLister lists both global and regional instance templates.
// Templates are independent top-level resources: several MIGs and reservations
// can consume one, and unused templates have no parent row at all.
type InstanceTemplateLister struct{}

func (InstanceTemplateLister) Kind() Kind {
	return Kind{
		ID:    "templates",
		Title: "Instance Templates",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "SCOPE", Width: 3},
			{Title: "MACHINE TYPE", Width: 3},
			{Title: "DISKS", Width: 2},
			{Title: "NICS", Width: 2},
			{Title: "ACCELERATORS", Width: 3},
			{Title: "AGE", Width: 2},
		},
	}
}

func (InstanceTemplateLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("instance templates client: %w", err)
	}

	var result Result
	err = svc.InstanceTemplates.AggregatedList(p.ProjectID).
		ReturnPartialSuccess(true).
		Context(ctx).
		Pages(ctx, func(page *compute.InstanceTemplateAggregatedList) error {
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
				for _, template := range scoped.InstanceTemplates {
					if template != nil {
						result.Resources = append(result.Resources, instanceTemplateResource(p, scope, template))
					}
				}
			}
			return nil
		})
	sortResources(result.Resources)
	dedupeSortWarnings(&result)
	return result, err
}

func instanceTemplateResource(p config.Project, scope string, template *compute.InstanceTemplate) Resource {
	if scope == "" {
		scope = lastSegment(template.Region)
	}
	if scope == "" {
		scope = "global"
	}

	machine, disks, nics, accelerators := "-", 0, 0, "-"
	if properties := template.Properties; properties != nil {
		machine = dashIfEmpty(lastSegment(properties.MachineType))
		disks = len(properties.Disks)
		nics = len(properties.NetworkInterfaces)
		accelerators = acceleratorSummary(properties.GuestAccelerators)
	}

	consoleURL := fmt.Sprintf(
		"https://console.cloud.google.com/compute/instanceTemplates/details/%s?project=%s",
		url.PathEscape(template.Name), url.QueryEscape(p.ProjectID))
	if scope != "global" {
		consoleURL += "&region=" + url.QueryEscape(scope)
	}

	return Resource{
		Name:     template.Name,
		Location: scope,
		Status:   "ACTIVE",
		Row: []string{
			template.Name,
			scope,
			machine,
			fmt.Sprintf("%d", disks),
			fmt.Sprintf("%d", nics),
			accelerators,
			age(template.CreationTimestamp),
		},
		Raw:        template,
		ConsoleURL: consoleURL,
	}
}

func acceleratorSummary(accelerators []*compute.AcceleratorConfig) string {
	if len(accelerators) == 0 {
		return "-"
	}

	parts := make([]string, 0, len(accelerators))
	for _, accelerator := range accelerators {
		if accelerator == nil {
			continue
		}
		name := lastSegment(accelerator.AcceleratorType)
		if name == "" {
			name = "accelerator"
		}
		parts = append(parts, fmt.Sprintf("%dx %s", accelerator.AcceleratorCount, name))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}
