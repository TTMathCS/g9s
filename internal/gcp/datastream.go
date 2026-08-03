package gcp

import (
	"context"
	"fmt"
	"net/url"

	datastream "google.golang.org/api/datastream/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// DatastreamLister lists Datastream streams.
//
// Regional service reached with the `locations/-` wildcard. The rule this
// follows, and that Memcached and Data Fusion follow too: a list response
// carrying an Unreachable field is one that can span locations, because a
// single-location call has nothing to be unreachable. If the wildcard were
// refused the call errors and the error surfaces — a listing that is silently
// short is the outcome worth avoiding, and that is the one a fan-out over
// guessed regions would produce.
//
// A stream is a replication pipeline, and the failure that matters is not that
// it is broken but that it stopped. PAUSED is the quiet one: no error, no
// alert, and the destination simply stops receiving rows while every dashboard
// built on it goes stale without going empty.
type DatastreamLister struct{}

func (DatastreamLister) Kind() Kind {
	return Kind{
		ID:    "datastream",
		Title: "Datastream Streams",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "REGION", Width: 2},
			{Title: "SOURCE", Width: 3},
			{Title: "DESTINATION", Width: 3},
			{Title: "BACKFILL", Width: 2},
			{Title: "STATE", Width: 2},
			{Title: "UPDATED", Width: 2},
		},
	}
}

func (DatastreamLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := datastream.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("datastream client: %w", err)
	}

	var (
		result      Result
		unreachable = map[string]bool{}
	)
	err = svc.Projects.Locations.Streams.List("projects/"+p.ProjectID+"/locations/-").
		Pages(ctx, func(page *datastream.ListStreamsResponse) error {
			for _, loc := range page.Unreachable {
				unreachable[loc] = true
			}
			for _, s := range page.Streams {
				if s != nil {
					result.Resources = append(result.Resources, streamResource(p, s))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	for _, loc := range sortedKeys(unreachable) {
		if w, ok := describeFailure(lastSegment(loc), fmt.Errorf("location unreachable")); ok {
			result.Warnings = append(result.Warnings, w)
		}
	}

	sortResources(result.Resources)
	return result, nil
}

func streamResource(p config.Project, s *datastream.Stream) Resource {
	name := lastSegment(s.Name)
	region := instanceRegion(s.Name)

	return Resource{
		Name:     name,
		Location: region,
		Status:   streamStatus(s),
		Row: []string{
			name,
			region,
			streamSource(s),
			streamDestination(s),
			streamBackfill(s),
			streamState(s),
			age(s.UpdateTime),
		},
		Raw: s,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/datastream/streams/locations/%s/instances/%s?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// streamStatus reports errors over the stream's own state.
//
// A stream carries an Errors list independently of its state, so one can be
// RUNNING and failing every row it reads. The state alone would report that as
// healthy.
func streamStatus(s *datastream.Stream) string {
	if len(s.Errors) > 0 {
		return "STREAM_ERRORS"
	}
	return streamState(s)
}

func streamState(s *datastream.Stream) string {
	if s.State == "" || s.State == "STATE_UNSPECIFIED" {
		return "UNKNOWN"
	}
	return s.State
}

// streamSource names what is being replicated from. Exactly one of the profile
// configs is set, and which one changes what a failure means — a MySQL binlog
// gap is a different problem from an Oracle privilege.
func streamSource(s *datastream.Stream) string {
	if s.SourceConfig == nil {
		return "-"
	}
	kind := ""
	switch {
	case s.SourceConfig.MysqlSourceConfig != nil:
		kind = "mysql"
	case s.SourceConfig.OracleSourceConfig != nil:
		kind = "oracle"
	case s.SourceConfig.PostgresqlSourceConfig != nil:
		kind = "postgres"
	case s.SourceConfig.SqlServerSourceConfig != nil:
		kind = "sqlserver"
	}
	return joinProfile(kind, s.SourceConfig.SourceConnectionProfile)
}

// streamDestination names where rows land.
func streamDestination(s *datastream.Stream) string {
	if s.DestinationConfig == nil {
		return "-"
	}
	kind := ""
	switch {
	case s.DestinationConfig.BigqueryDestinationConfig != nil:
		kind = "bigquery"
	case s.DestinationConfig.GcsDestinationConfig != nil:
		kind = "gcs"
	}
	return joinProfile(kind, s.DestinationConfig.DestinationConnectionProfile)
}

// joinProfile renders "<kind>: <profile>", dropping whichever half is missing
// rather than leaving a stray colon.
func joinProfile(kind, profile string) string {
	profile = lastSegment(profile)
	switch {
	case kind != "" && profile != "":
		return kind + ": " + profile
	case kind != "":
		return kind
	case profile != "":
		return profile
	default:
		return "-"
	}
}

// streamBackfill says whether historical data is being copied or only new
// changes are. A stream created with backfill off has no history in its
// destination, which is a surprise the first time someone queries last month.
func streamBackfill(s *datastream.Stream) string {
	switch {
	case s.BackfillAll != nil:
		return "all"
	case s.BackfillNone != nil:
		return "none — changes only"
	default:
		return "-"
	}
}
