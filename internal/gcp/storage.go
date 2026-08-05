package gcp

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"google.golang.org/api/option"
	storagev1 "google.golang.org/api/storage/v1"

	"github.com/TTMathCS/g9s/internal/config"
)

// StorageLister lists Cloud Storage buckets.
//
// The simplest lister here: Buckets is project-scoped and global, one call,
// no region fan-out and no aggregation trick needed.
//
// On the generated REST client rather than cloud.google.com/go/storage, which
// every other lister here already uses for its own service. The hand-written
// wrapper is a second client stack with its own transport, its own auth path
// and its own release cadence, and it was the one place g9s took a dependency
// on a library whose version had to line up with google.golang.org/api rather
// than simply being it. The REST client is the same API, produces
// *googleapi.Error like everything else so describeFailure classifies storage
// failures the same way it classifies the rest, and drops a large dependency
// tree that existed to serve three listers.
type StorageLister struct{}

func (StorageLister) Kind() Kind {
	return Kind{
		ID:    "gcs",
		Title: "Storage Buckets",
		Columns: []Column{
			{Title: "NAME", Width: 6},
			{Title: "LOCATION", Width: 3},
			{Title: "STORAGE CLASS", Width: 3},
			{Title: "VERSIONING", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (StorageLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := storagev1.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("storage client: %w", err)
	}

	var result Result
	// full projection, because the lifecycle drill-down reads its rules off this
	// response rather than paying a Get per bucket. noAcl would omit them and
	// turn a free listing into an N+1.
	err = svc.Buckets.List(p.ProjectID).Projection("full").
		Pages(ctx, func(page *storagev1.Buckets) error {
			for _, b := range page.Items {
				if b != nil {
					result.Resources = append(result.Resources, bucketResource(p, b))
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

func bucketResource(p config.Project, b *storagev1.Bucket) Resource {
	versioning := "off"
	if b.Versioning != nil && b.Versioning.Enabled {
		versioning = "on"
	}

	return Resource{
		Name:     b.Name,
		Location: b.Location,
		// Buckets have no running/stopped lifecycle the way a VM or a cluster
		// does — appearing in the list is the healthy state. A synthetic
		// status keeps the dashboard's rollup and the row colouring
		// meaningful instead of everything landing in a colourless UNKNOWN.
		Status: "ACTIVE",
		Row: []string{
			b.Name,
			b.Location,
			b.StorageClass,
			versioning,
			bucketAge(b.TimeCreated),
		},
		Raw: b,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/storage/browser/%s?project=%s",
			url.PathEscape(b.Name), url.QueryEscape(p.ProjectID)),
	}
}

// bucketAge renders a creation timestamp the REST API returns as RFC 3339.
//
// A dash rather than an invented age when it will not parse: the wrapper
// handed back a time.Time and an unset one was obvious, while a string that
// fails to parse would otherwise render as "55y" and look like a real finding.
func bucketAge(created string) string {
	t, err := time.Parse(time.RFC3339, created)
	if err != nil || t.IsZero() {
		return "-"
	}
	return shortDuration(timeSince(t))
}
