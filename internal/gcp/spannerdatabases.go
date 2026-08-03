package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"google.golang.org/api/option"
	spanner "google.golang.org/api/spanner/v1"

	"github.com/TTMathCS/g9s/internal/config"
)

// SpannerDatabaseLister is the databases inside one Spanner instance.
//
// One call, made when you open the instance. A drill-down rather than a kind of
// its own because a database has no capacity, no config and no bill — those all
// belong to the instance — so "every Spanner database in the project", stripped
// of which instance each is in, answers nothing.
//
// DROP PROTECTION is the column worth the trip. It is off by default, it is the
// only thing standing between a database and a one-command deletion, and the
// instance row cannot show it.
type SpannerDatabaseLister struct{}

func (SpannerDatabaseLister) ParentKind() string { return "spanner" }

func (SpannerDatabaseLister) Kind() Kind {
	return Kind{
		ID:    "spannerdbs",
		Title: "Databases",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "DIALECT", Width: 2},
			{Title: "DROP PROTECTION", Width: 2},
			{Title: "VERSION RETENTION", Width: 2},
			{Title: "LEADER", Width: 2},
			{Title: "STATE", Width: 2},
		},
	}
}

func (SpannerDatabaseLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	instance, ok := parent.Raw.(*spanner.Instance)
	if !ok || instance.Name == "" {
		return Result{}, fmt.Errorf("no Spanner instance data for %s", parent.Name)
	}

	svc, err := spanner.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("spanner client: %w", err)
	}

	var result Result
	err = svc.Projects.Instances.Databases.List(instance.Name).
		Pages(ctx, func(page *spanner.ListDatabasesResponse) error {
			for _, db := range page.Databases {
				if db != nil {
					result.Resources = append(result.Resources, spannerDatabaseResource(p, parent.Name, db))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	sortResources(result.Resources)
	return result, nil
}

func spannerDatabaseResource(p config.Project, instanceName string, d *spanner.Database) Resource {
	name := lastSegment(d.Name)

	return Resource{
		Name: name,
		// The instance fixes the location, so repeating it on every child row
		// would spend a column saying what the trail already says.
		Location: "",
		Status:   spannerDatabaseStatus(d),
		Row: []string{
			name,
			spannerDialect(d),
			dropProtection(d),
			versionRetention(d),
			spannerLeader(d),
			spannerDatabaseState(d),
		},
		Raw: d,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/spanner/instances/%s/databases/%s/details/tables?project=%s",
			url.PathEscape(instanceName), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// spannerDatabaseStatus leads with drop protection, for the same reason the
// bucket lifecycle rules lead with Delete: it is the setting whose absence
// loses data, and the API reports READY either way.
func spannerDatabaseStatus(d *spanner.Database) string {
	state := spannerDatabaseState(d)
	if state == "READY" && !d.EnableDropProtection {
		return "NO_DROP_PROTECTION"
	}
	return state
}

func spannerDatabaseState(d *spanner.Database) string {
	if d.State == "" || d.State == "STATE_UNSPECIFIED" {
		return "UNKNOWN"
	}
	return d.State
}

func dropProtection(d *spanner.Database) string {
	if d.EnableDropProtection {
		return "on"
	}
	return "off"
}

// spannerDialect matters because it decides what SQL works against the
// database, and the two are not interchangeable.
func spannerDialect(d *spanner.Database) string {
	switch d.DatabaseDialect {
	case "GOOGLE_STANDARD_SQL":
		return "GoogleSQL"
	case "POSTGRESQL":
		return "PostgreSQL"
	case "", "DATABASE_DIALECT_UNSPECIFIED":
		// Unspecified means GoogleSQL, which is the default the API applies
		// when a database was created without saying.
		return "GoogleSQL"
	default:
		return d.DatabaseDialect
	}
}

// versionRetention is how far back stale reads and point-in-time recovery can
// go. The default is one hour; a database configured for a week is paying for
// that in storage, and one left at the default cannot recover from yesterday.
func versionRetention(d *spanner.Database) string {
	if d.VersionRetentionPeriod == "" {
		return "-"
	}
	if dur, err := time.ParseDuration(d.VersionRetentionPeriod); err == nil {
		return shortDuration(dur)
	}
	// The API can return "1h" or "7d"; ParseDuration does not know days.
	return strings.TrimSpace(d.VersionRetentionPeriod)
}

// spannerLeader is the leader region for a multi-region instance, which is
// where writes are acknowledged and therefore where write latency comes from.
func spannerLeader(d *spanner.Database) string {
	if d.DefaultLeader == "" {
		return "-"
	}
	return d.DefaultLeader
}
