package gcp

import (
	"context"
	"fmt"
	"net/url"
	"time"

	service "cloud.google.com/go/orchestration/airflow/service/apiv1"
	"cloud.google.com/go/orchestration/airflow/service/apiv1/servicepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// ComposerLister lists Cloud Composer environments.
//
// Composer is location-scoped through the request parent rather than the
// endpoint, so one client serves every location — but there is still no
// wildcard parent, so the fan-out remains.
type ComposerLister struct{}

func (ComposerLister) Kind() Kind {
	return Kind{
		ID:    "composer",
		Title: "Composer Environments",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "LOCATION", Width: 3},
			{Title: "STATE", Width: 2},
			{Title: "IMAGE VERSION", Width: 4},
			{Title: "AIRFLOW URI", Width: 5},
			{Title: "AGE", Width: 2},
		},
	}
}

func (ComposerLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	locations := cfg.ComposerLocations(p)
	if len(locations) == 0 {
		// Nothing was looked at, and an empty table must not pretend
		// otherwise.
		return Result{Warnings: []Warning{narrowedWarning("no locations configured — set projects[].regions or defaults.regions")}}, nil
	}

	client, err := service.NewEnvironmentsClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("composer client: %w", err)
	}
	defer client.Close()

	return fanOut(ctx, locations, func(ctx context.Context, location string) (Result, error) {
		it := client.ListEnvironments(ctx, &servicepb.ListEnvironmentsRequest{
			Parent: fmt.Sprintf("projects/%s/locations/%s", p.ProjectID, location),
		})

		var out Result
		for {
			env, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return out, err
			}
			out.Resources = append(out.Resources, environmentResource(p, location, env))
		}
		return out, nil
	}), nil
}

func environmentResource(p config.Project, location string, env *servicepb.Environment) Resource {
	name := lastSegment(env.GetName())
	state := env.GetState().String()

	created := "-"
	if ts := env.GetCreateTime(); ts != nil {
		created = shortDuration(timeSince(ts.AsTime()))
	}

	return Resource{
		Name:     name,
		Location: location,
		Status:   state,
		Row: []string{
			name,
			location,
			state,
			env.GetConfig().GetSoftwareConfig().GetImageVersion(),
			env.GetConfig().GetAirflowUri(),
			created,
		},
		Raw: env,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/composer/environments/detail/%s/%s/monitoring?project=%s",
			url.PathEscape(location), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// AirflowURI returns the Airflow web UI for a Composer resource, so the tool
// can open the thing the user actually wants rather than the Console page.
func AirflowURI(r Resource) (string, bool) {
	env, ok := r.Raw.(*servicepb.Environment)
	if !ok {
		return "", false
	}
	uri := env.GetConfig().GetAirflowUri()
	return uri, uri != ""
}

func timeSince(t time.Time) time.Duration { return time.Since(t) }

// millisToTime converts epoch milliseconds, the unit the REST-shaped APIs use
// for timestamps, into a time.Time.
func millisToTime(millis int64) time.Time { return time.UnixMilli(millis) }
