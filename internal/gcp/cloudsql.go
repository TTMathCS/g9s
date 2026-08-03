package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/TTMathCS/g9s/internal/config"
)

// CloudSQLLister lists Cloud SQL instances.
//
// The Admin API's Instances.List is project-scoped and global — one paginated
// call covers every region, so there is no fan-out. It is also the only lister
// here that reports unreachable regions in the response body rather than as an
// error, which is why partial results are collected from Warnings instead of
// coming back through fanOut.
type CloudSQLLister struct{}

func (CloudSQLLister) Kind() Kind {
	return Kind{
		ID:    "sql",
		Title: "Cloud SQL Instances",
		Columns: []Column{
			{Title: "NAME", Width: 6},
			{Title: "REGION", Width: 3},
			{Title: "VERSION", Width: 3},
			{Title: "TIER", Width: 3},
			{Title: "HA", Width: 2},
			{Title: "STATUS", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (CloudSQLLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := sqladmin.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("cloudsql client: %w", err)
	}

	var result Result
	err = svc.Instances.List(p.ProjectID).Pages(ctx, func(page *sqladmin.InstancesListResponse) error {
		for _, inst := range page.Items {
			result.Resources = append(result.Resources, sqlInstanceResource(p, inst))
		}
		// A region Cloud SQL could not reach arrives here rather than as an
		// error, so the listing looks complete unless these are surfaced.
		for _, w := range page.Warnings {
			if msg, ok := sqlWarning(w); ok {
				result.Warnings = append(result.Warnings, msg)
			}
		}
		return nil
	})
	if err != nil {
		// Whatever arrived before the failure is still real, but a half-listing
		// presented as complete is the failure mode worth avoiding, so the
		// error goes back to the caller.
		return result, err
	}

	sortResources(result.Resources)
	dedupeSortWarnings(&result)
	return result, nil
}

// sqlWarning renders an API warning, preferring the region it names so the
// message matches the "<scope>: <reason>" shape the other listers produce.
func sqlWarning(w *sqladmin.ApiWarning) (Warning, bool) {
	if w == nil {
		return Warning{}, false
	}
	scope := w.Region
	if scope == "" {
		scope = "cloudsql"
	}
	detail := w.Message
	if detail == "" {
		detail = w.Code
	}
	if detail == "" {
		return Warning{}, false
	}
	// The API reports an unreachable region this way rather than as an error,
	// so the code is what distinguishes it from anything else it might say.
	reason := ReasonUnknown
	if w.Code == "REGION_UNREACHABLE" {
		reason = ReasonUnreachable
	}
	return scopeWarning(scope, reason, clip(detail, 100)), true
}

func sqlInstanceResource(p config.Project, inst *sqladmin.DatabaseInstance) Resource {
	name := inst.Name
	state := inst.State

	tier, ha := "-", "-"
	if s := inst.Settings; s != nil {
		if s.Tier != "" {
			tier = s.Tier
		}
		if s.AvailabilityType != "" {
			ha = s.AvailabilityType
		}
	}

	// POSTGRES_15 reads better as POSTGRES 15 in a narrow column, and the raw
	// value is still one keystroke away in the detail pane.
	version := strings.ReplaceAll(inst.DatabaseVersion, "_", " ")
	if version == "" {
		version = "-"
	}

	return Resource{
		Name:     name,
		Location: inst.Region,
		Status:   state,
		Row: []string{
			name,
			inst.Region,
			version,
			tier,
			ha,
			state,
			age(inst.CreateTime),
		},
		Raw: inst,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/sql/instances/%s/overview?project=%s",
			url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}
