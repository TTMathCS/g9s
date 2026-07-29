package gcp

import (
	"context"
	"fmt"
	"net/url"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// StorageLister lists Cloud Storage buckets.
//
// The simplest lister here: Buckets is project-scoped and global, one call,
// no region fan-out and no aggregation trick needed.
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
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("storage client: %w", err)
	}
	defer client.Close()

	var result Result
	it := client.Buckets(ctx, p.ProjectID)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return result, err
		}
		result.Resources = append(result.Resources, bucketResource(p, attrs))
	}

	sortResources(result.Resources)
	return result, nil
}

func bucketResource(p config.Project, b *storage.BucketAttrs) Resource {
	versioning := "off"
	if b.VersioningEnabled {
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
			shortDuration(timeSince(b.Created)),
		},
		Raw: b,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/storage/browser/%s?project=%s",
			url.PathEscape(b.Name), url.QueryEscape(p.ProjectID)),
	}
}
