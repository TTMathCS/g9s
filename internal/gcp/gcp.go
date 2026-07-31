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

	"google.golang.org/api/iterator"
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
		// Compute and data first, then networking. Order is display order, and
		// the number keys bind to it, so the kinds reached most often lead.
		ComputeLister{},
		GKELister{},
		CloudSQLLister{},
		StorageLister{},
		BigQueryDatasetLister{},
		BigQueryJobLister{},
		DataprocLister{},
		DataprocJobLister{},
		ComposerLister{},
		PubSubTopicLister{},
		PubSubSubscriptionLister{},
		CloudRunServiceLister{},
		CloudRunJobLister{},
		VPCLister{},
		FirewallLister{},
		LoadBalancerLister{},
		DNSLister{},
		VPNLister{},
		InterconnectLister{},
		PSCLister{},
		// Last, and on its own: not compute, not data, not networking. Metadata
		// only — see SecretLister.
		SecretLister{},
	}
}

// fanOut runs fn once per location concurrently and collects partial results.
//
// This is the shape every region-scoped GCP API forces on you: there is no
// "list across all regions" call for Dataproc or Composer, so the tool has to
// do the fan-out and be honest about which legs failed.
//
// fn returns a Result rather than a slice so a leg can report a warning about a
// listing that succeeded: a region whose jobs were capped is not a failure, but
// it is not the whole truth either, and those are the same thing to a reader.
func fanOut(ctx context.Context, locations []string, fn func(ctx context.Context, location string) (Result, error)) Result {
	var (
		mu     sync.Mutex
		result Result
		wg     sync.WaitGroup
	)

	for _, loc := range locations {
		wg.Add(1)
		go func(loc string) {
			defer wg.Done()
			partial, err := fn(ctx, loc)

			mu.Lock()
			defer mu.Unlock()
			// Whatever the leg did produce is kept even when it then failed:
			// a page of results followed by a 403 on the next one is still a
			// page of results.
			result.Resources = append(result.Resources, partial.Resources...)
			result.Warnings = append(result.Warnings, partial.Warnings...)
			if err != nil {
				if w := describeFailure(loc, err); w != "" {
					result.Warnings = append(result.Warnings, w)
				}
			}
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
	return fmt.Sprintf("%s: %s", scope, clip(msg, 120))
}

// clip shortens s to at most max runes, marking the cut with an ellipsis.
//
// Runes, not bytes: API error strings carry quoted resource names and server
// messages that are not always ASCII, and slicing a byte offset lands in the
// middle of a multi-byte character. The replacement characters that produces
// are the kind of thing that gets mistaken for a corrupt response.
func clip(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

func grpcCode(err error) codes.Code {
	if s, ok := status.FromError(err); ok {
		return s.Code()
	}
	return codes.Unknown
}

// nexter is the shape every google-cloud-go iterator has. Depending on it
// rather than the concrete iterator types is what lets one collector serve
// resources whose only difference is the element type.
type nexter[T any] interface {
	Next() (T, error)
}

// collectList drains a flat, global iterator — the shape of Networks,
// Firewalls, GlobalForwardingRules and anything else with no location axis.
func collectList[T any](it nexter[T], build func(T) Resource) (Result, error) {
	var result Result
	for {
		v, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			// A hard failure means the whole listing failed rather than one
			// scope, so it goes back to the caller instead of degrading to a
			// table that looks empty.
			return result, err
		}
		result.Resources = append(result.Resources, build(v))
	}
	sortResources(result.Resources)
	return result, nil
}

// collectAggregated drains a Compute aggregatedList iterator, which hands back
// (scope, items) pairs covering every region or zone in one call.
//
// This is why none of the networking kinds need a fan-out: Compute will do the
// sweep server-side, unlike Dataproc where the endpoint itself is regional.
func collectAggregated[P, V any](it nexter[P], split func(P) (string, []V), build func(scope string, v V) Resource) (Result, error) {
	var result Result
	for {
		pair, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return result, err
		}
		scope, items := split(pair)
		for _, v := range items {
			result.Resources = append(result.Resources, build(scope, v))
		}
	}
	sortResources(result.Resources)
	return result, nil
}

// mergeResults folds one Result into another, keeping the combined list sorted
// and the warnings deduplicated. Load balancers need it because regional and
// global forwarding rules come from two different calls.
func mergeResults(dst *Result, src Result) {
	dst.Resources = append(dst.Resources, src.Resources...)
	dst.Warnings = append(dst.Warnings, src.Warnings...)
	sortResources(dst.Resources)
	dedupeSortWarnings(dst)
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
