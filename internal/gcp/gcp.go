// Package gcp lists GCP resources for a project.
//
// Every resource type is behind the Lister interface so adding one is a single
// new file. Listers return partial results: with a least-privilege account
// some regions and some APIs will refuse, and a tool that discards the whole
// refresh because one of ten regions returned 403 is useless. Failures come
// back as warnings alongside whatever did succeed.
package gcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TTMathCS/g9s/internal/config"
)

// Column describes one table column for a resource kind.
type Column struct {
	Title string
	// Width is a proportional weight, not a character count. The table
	// allocates the terminal width across columns using these.
	Width int
}

// Kind identifies a resource type and how to display it.
type Kind struct {
	ID      string
	Title   string
	Columns []Column
}

// Resource is one row in the table.
type Resource struct {
	// Name is the short resource name.
	Name string
	// Location is the zone, region or location the resource lives in.
	Location string
	// Status is the resource's own status string, used for row colouring.
	Status string
	// Row holds the cell values, aligned with Kind.Columns.
	Row []string
	// Raw is the full API object, rendered in the detail pane.
	Raw any
	// ConsoleURL deep-links to this resource in the Cloud Console.
	ConsoleURL string
}

// Result is a possibly-partial listing.
type Result struct {
	Resources []Resource
	// Warnings describe scopes that failed. Shown in the status bar so a
	// truncated list is never mistaken for an empty one.
	Warnings []string
}

// Lister fetches all resources of one kind for a project.
type Lister interface {
	Kind() Kind
	List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error)
}

// Listers returns the registered resource kinds, in display order.
func Listers() []Lister {
	return []Lister{
		ComputeLister{},
		GKELister{},
		CloudSQLLister{},
		StorageLister{},
		DataprocLister{},
		ComposerLister{},
	}
}

// fanOut runs fn once per location concurrently and collects partial results.
//
// This is the shape every region-scoped GCP API forces on you: there is no
// "list across all regions" call for Dataproc or Composer, so the tool has to
// do the fan-out and be honest about which legs failed.
func fanOut(ctx context.Context, locations []string, fn func(ctx context.Context, location string) ([]Resource, error)) Result {
	var (
		mu     sync.Mutex
		result Result
		wg     sync.WaitGroup
	)

	for _, loc := range locations {
		wg.Add(1)
		go func(loc string) {
			defer wg.Done()
			resources, err := fn(ctx, loc)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if w := describeFailure(loc, err); w != "" {
					result.Warnings = append(result.Warnings, w)
				}
				return
			}
			result.Resources = append(result.Resources, resources...)
		}(loc)
	}
	wg.Wait()

	sortResources(result.Resources)
	sort.Strings(result.Warnings)
	return result
}

// describeFailure turns an API error into a short warning, or "" if the error
// is not worth reporting.
//
// A region where the API is simply not enabled is not an error worth showing
// on every refresh — that is the normal state for most regions in most
// projects. Permission denied is worth showing exactly once per scope, since
// it is the difference between "nothing there" and "you cannot see it".
func describeFailure(scope string, err error) string {
	switch grpcCode(err) {
	case codes.NotFound:
		return ""
	case codes.PermissionDenied:
		return fmt.Sprintf("%s: permission denied", scope)
	case codes.Unauthenticated:
		return fmt.Sprintf("%s: not authenticated", scope)
	case codes.FailedPrecondition:
		// Usually "API not enabled for this project".
		if strings.Contains(strings.ToLower(err.Error()), "not been used") ||
			strings.Contains(strings.ToLower(err.Error()), "disabled") {
			return ""
		}
	}

	msg := err.Error()
	if strings.Contains(msg, "SERVICE_DISABLED") || strings.Contains(msg, "accessNotConfigured") {
		return ""
	}
	if len(msg) > 120 {
		msg = msg[:117] + "…"
	}
	return fmt.Sprintf("%s: %s", scope, msg)
}

func grpcCode(err error) codes.Code {
	if s, ok := status.FromError(err); ok {
		return s.Code()
	}
	return codes.Unknown
}

// dedupeSortWarnings collapses repeated warnings and gives them a stable order.
//
// A paginated listing repeats the same "region unreachable" warning on every
// page, which the footer would otherwise report as five unavailable scopes when
// only one region actually failed.
func dedupeSortWarnings(r *Result) {
	if len(r.Warnings) == 0 {
		return
	}
	seen := make(map[string]bool, len(r.Warnings))
	unique := make([]string, 0, len(r.Warnings))
	for _, w := range r.Warnings {
		if seen[w] {
			continue
		}
		seen[w] = true
		unique = append(unique, w)
	}
	sort.Strings(unique)
	r.Warnings = unique
}

// sortResources gives the table a stable order: location, then name.
func sortResources(resources []Resource) {
	sort.SliceStable(resources, func(i, j int) bool {
		if resources[i].Location != resources[j].Location {
			return resources[i].Location < resources[j].Location
		}
		return resources[i].Name < resources[j].Name
	})
}

// lastSegment returns the final path component of a GCP resource URL, which is
// how the APIs encode references to zones, machine types and networks.
func lastSegment(url string) string {
	if url == "" {
		return ""
	}
	if i := strings.LastIndex(url, "/"); i >= 0 {
		return url[i+1:]
	}
	return url
}

// regionOfZone turns "us-central1-a" into "us-central1".
func regionOfZone(zone string) string {
	if i := strings.LastIndex(zone, "-"); i > 0 {
		return zone[:i]
	}
	return zone
}
