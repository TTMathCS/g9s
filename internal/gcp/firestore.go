package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	firestore "google.golang.org/api/firestore/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// FirestoreLister lists Firestore databases.
//
// Global: the parent is `projects/{p}` and one call returns every database,
// with unanswered locations named in Unreachable. A project usually has one —
// `(default)` — but multi-database has been generally available for a while and
// a project with five is no longer unusual.
//
// Two settings decide whether a mistake is recoverable, and neither is on by
// default. Delete protection is the only guard against removing the database
// itself; point-in-time recovery is the only way back from a bad write, and
// without it the answer to "can we roll back an hour" is no. Both report
// nothing at all when off, which is why they are columns rather than prose.
type FirestoreLister struct{}

func (FirestoreLister) Kind() Kind {
	return Kind{
		ID:    "firestore",
		Title: "Firestore Databases",
		Columns: []Column{
			{Title: "NAME", Width: 3},
			{Title: "LOCATION", Width: 2},
			{Title: "MODE", Width: 3},
			{Title: "PITR", Width: 2},
			{Title: "DELETE PROTECTION", Width: 2},
			{Title: "CONCURRENCY", Width: 2},
		},
	}
}

func (FirestoreLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := firestore.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("firestore client: %w", err)
	}

	resp, err := svc.Projects.Databases.List("projects/" + p.ProjectID).Context(ctx).Do()
	if err != nil {
		return Result{}, err
	}

	var result Result
	for _, db := range resp.Databases {
		if db != nil {
			result.Resources = append(result.Resources, firestoreResource(p, db))
		}
	}
	for _, loc := range resp.Unreachable {
		if w := describeFailure(loc, fmt.Errorf("location unreachable")); w != "" {
			result.Warnings = append(result.Warnings, w)
		}
	}

	sortResources(result.Resources)
	return result, nil
}

func firestoreResource(p config.Project, d *firestore.GoogleFirestoreAdminV1Database) Resource {
	name := lastSegment(d.Name)

	location := d.LocationId
	if location == "" {
		location = "-"
	}

	return Resource{
		Name:     name,
		Location: location,
		Status:   firestoreStatus(d),
		Row: []string{
			name,
			location,
			firestoreMode(d),
			pointInTimeRecovery(d),
			firestoreDeleteProtection(d),
			firestoreConcurrency(d),
		},
		Raw: d,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/firestore/databases/%s/data?project=%s",
			url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// firestoreStatus leads with whichever recovery guard is missing.
//
// Delete protection outranks PITR: losing the database outright is worse than
// being unable to rewind it, and a database with neither has the more urgent
// problem named first.
func firestoreStatus(d *firestore.GoogleFirestoreAdminV1Database) string {
	if !deleteProtectionEnabled(d) {
		return "NO_DELETE_PROTECTION"
	}
	if !pitrEnabled(d) {
		return "NO_PITR"
	}
	return "ACTIVE"
}

func deleteProtectionEnabled(d *firestore.GoogleFirestoreAdminV1Database) bool {
	return d.DeleteProtectionState == "DELETE_PROTECTION_ENABLED"
}

func pitrEnabled(d *firestore.GoogleFirestoreAdminV1Database) bool {
	return d.PointInTimeRecoveryEnablement == "POINT_IN_TIME_RECOVERY_ENABLED"
}

func firestoreDeleteProtection(d *firestore.GoogleFirestoreAdminV1Database) string {
	if deleteProtectionEnabled(d) {
		return "on"
	}
	return "off"
}

// pointInTimeRecovery is the difference between recovering from a bad write and
// not. Enabled, Firestore keeps seven days of versions.
func pointInTimeRecovery(d *firestore.GoogleFirestoreAdminV1Database) string {
	if pitrEnabled(d) {
		return "on (7d)"
	}
	return "off"
}

// firestoreMode separates the two products sharing one API. A Datastore-mode
// database does not speak the Firestore client libraries and cannot be switched
// after creation, so it is not a detail — it is which database this is.
func firestoreMode(d *firestore.GoogleFirestoreAdminV1Database) string {
	switch d.Type {
	case "FIRESTORE_NATIVE":
		return "native"
	case "DATASTORE_MODE":
		return "datastore mode"
	case "", "DATABASE_TYPE_UNSPECIFIED":
		return "-"
	default:
		return strings.ToLower(d.Type)
	}
}

// firestoreConcurrency reports the transaction mode. Optimistic is the legacy
// default and retries under contention; pessimistic locks instead, which is
// what a workload with hot documents usually wants.
func firestoreConcurrency(d *firestore.GoogleFirestoreAdminV1Database) string {
	switch d.ConcurrencyMode {
	case "OPTIMISTIC":
		return "optimistic"
	case "PESSIMISTIC":
		return "pessimistic"
	case "OPTIMISTIC_WITH_ENTITY_GROUPS":
		return "optimistic (entity groups)"
	case "", "CONCURRENCY_MODE_UNSPECIFIED":
		return "-"
	default:
		return strings.ToLower(d.ConcurrencyMode)
	}
}
