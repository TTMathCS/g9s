package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	htransport "google.golang.org/api/transport/http"

	"github.com/TTMathCS/g9s/internal/config"
)

// SnapshotLister lists persistent-disk snapshots across the project.
//
// Snapshots are top-level even though they originate from disks. A snapshot
// can outlive its source disk, can be restored into several new disks, and is
// billed independently. Making it a disk-only drill-down would hide exactly
// the orphaned snapshots a project-wide inventory is meant to find.
type SnapshotLister struct{}

func (SnapshotLister) Kind() Kind {
	return Kind{
		ID:    "snapshots",
		Title: "Disk Snapshots",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "SOURCE DISK", Width: 4},
			{Title: "DISK SIZE", Width: 2},
			{Title: "STORED", Width: 2},
			{Title: "STORAGE LOCATIONS", Width: 3},
			{Title: "STATUS", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (SnapshotLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	transportOpts := append([]option.ClientOption{option.WithScopes(compute.ComputeReadonlyScope)}, opts...)
	client, endpoint, err := htransport.NewClient(ctx, transportOpts...)
	if err != nil {
		return Result{}, fmt.Errorf("snapshots client: %w", err)
	}
	if endpoint == "" {
		endpoint = "https://compute.googleapis.com/compute/beta/"
	}

	var result Result
	pageToken := ""
	for {
		requestURL, err := snapshotAggregatedListURL(endpoint, p.ProjectID, pageToken)
		if err != nil {
			return result, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return result, fmt.Errorf("snapshots request: %w", err)
		}
		response, err := client.Do(req)
		if err != nil {
			return result, err
		}
		if err := googleapi.CheckResponse(response); err != nil {
			response.Body.Close()
			return result, err
		}

		var page snapshotAggregatedList
		err = json.NewDecoder(response.Body).Decode(&page)
		response.Body.Close()
		if err != nil {
			return result, fmt.Errorf("decode snapshots response: %w", err)
		}

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
			for _, snapshot := range scoped.Snapshots {
				if snapshot != nil {
					result.Resources = append(result.Resources, snapshotResourceInScope(p, scope, snapshot))
				}
			}
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	sortResources(result.Resources)
	dedupeSortWarnings(&result)
	return result, nil
}

func snapshotResource(p config.Project, snapshot *compute.Snapshot) Resource {
	return snapshotResourceInScope(p, lastSegment(snapshot.Region), snapshot)
}

func snapshotResourceInScope(p config.Project, scope string, snapshot *compute.Snapshot) Resource {
	status := snapshot.Status
	if status == "" {
		status = "UNKNOWN"
	}

	locations := "-"
	if len(snapshot.StorageLocations) > 0 {
		locations = strings.Join(snapshot.StorageLocations, ",")
	}

	scope = lastSegment(scope)
	if scope == "" {
		scope = "global"
	}
	consoleScope := "global"
	if scope != "global" {
		consoleScope = "regions/" + url.PathEscape(scope)
	}

	return Resource{
		Name:     snapshot.Name,
		Location: scope,
		Status:   status,
		Row: []string{
			snapshot.Name,
			dashIfEmpty(lastSegment(snapshot.SourceDisk)),
			fmt.Sprintf("%dGB", snapshot.DiskSizeGb),
			humanBytes(snapshot.StorageBytes),
			locations,
			status,
			age(snapshot.CreationTimestamp),
		},
		Raw: snapshot,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/compute/snapshotsDetail/projects/%s/%s/snapshots/%s?project=%s",
			url.PathEscape(p.ProjectID), consoleScope,
			url.PathEscape(snapshot.Name), url.QueryEscape(p.ProjectID)),
	}
}

// Regional snapshots and their project-wide sweep are still a Compute Preview,
// and the stable generated Go client does not expose AggregatedList. Keep the
// authenticated transport and Snapshot model from the pinned Google API
// module, and locally describe only the small response envelope that the
// generator is missing. This can collapse back to the generated v1 method once
// the feature reaches it.
type snapshotAggregatedList struct {
	Items         map[string]snapshotScopedList `json:"items"`
	NextPageToken string                        `json:"nextPageToken"`
	Unreachables  []string                      `json:"unreachables"`
	Warning       *computeWarning               `json:"warning"`
}

type snapshotScopedList struct {
	Snapshots []*compute.Snapshot `json:"snapshots"`
	Warning   *computeWarning     `json:"warning"`
}

type computeWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func snapshotAggregatedListURL(endpoint, projectID, pageToken string) (string, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse Compute endpoint: %w", err)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/projects/" +
		url.PathEscape(projectID) + "/aggregated/snapshots"
	query := base.Query()
	query.Set("returnPartialSuccess", "true")
	if pageToken != "" {
		query.Set("pageToken", pageToken)
	}
	base.RawQuery = query.Encode()
	return base.String(), nil
}
