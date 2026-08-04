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
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/api/googleapi"
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
	// State marks a column whose cells are resource states and should be
	// coloured like one. A kind's own status column is found by its title;
	// this is for tables whose column titles are something else — the
	// comparison view heads each column with a project name, and every one of
	// them holds a state.
	State bool
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
	// Project names the configured project this row came from.
	//
	// Empty in a single-project table, where the header already says which
	// project every row belongs to. Set by the fleet sweep, where it is the
	// only thing distinguishing two identically-named instances in dev and
	// prod — which is exactly the pair a comparison exists to look at.
	Project string
	// KindID names the lister this row came from. Listers do not set it —
	// StampKind does, once, on the way out of a fetch — so it stays correct
	// without every lister having to remember. It is what lets the merged
	// table, which has flattened away the per-kind columns, still answer "what
	// is this row" for a drill-down.
	KindID string
}

// StampKind records which kind a listing's rows came from.
func StampKind(result *Result, kindID string) {
	for i := range result.Resources {
		result.Resources[i].KindID = kindID
	}
}

// Result is a possibly-partial listing.
type Result struct {
	Resources []Resource
	// Warnings describe every way this listing falls short of the whole
	// picture — scopes that failed, caps that stopped it, sub-items that could
	// not be read. Shown in the status bar so a truncated list is never
	// mistaken for an empty one, and typed so that a caller aggregating across
	// projects can tell a missing permission from a row limit without reading
	// the sentence. See warning.go.
	Warnings []Warning
	// NextPageToken is set only by listings that deliberately expose
	// pagination to the UI. Most resource kinds drain every API page inside
	// List and leave this empty; object browsers keep it so a large bucket can
	// stop after one human-sized page without pretending the listing is
	// complete.
	NextPageToken string
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
		ComputeDiskLister{},
		SnapshotLister{},
		ManagedInstanceGroupLister{},
		InstanceTemplateLister{},
		ReservationLister{},
		GKELister{},
		CloudSQLLister{},
		StorageLister{},
		BigQueryDatasetLister{},
		BigQueryJobLister{},
		BigQueryReservationLister{},
		DataprocLister{},
		DataprocJobLister{},
		ComposerLister{},
		DataflowLister{},
		// The managed data stores, after the processing kinds rather than
		// interleaved with them. Two reasons, both practical: `:data` has to go
		// on resolving to Dataproc, which it does only while Dataproc comes
		// first among the five ids starting with those letters; and every kind
		// after an insertion point gets a new hotkey, so new kinds go at the end
		// of a group instead of the middle of one.
		SpannerLister{},
		BigtableLister{},
		FirestoreLister{},
		RedisLister{},
		MemcacheLister{},
		DatastreamLister{},
		DataFusionLister{},
		ArtifactRegistryLister{},
		PubSubTopicLister{},
		PubSubSubscriptionLister{},
		CloudRunServiceLister{},
		CloudRunJobLister{},
		CloudFunctionLister{},
		SchedulerJobLister{},
		CloudBuildLister{},
		CloudTaskQueueLister{},
		AlertPolicyLister{},
		ErrorGroupLister{},
		BatchJobLister{},
		TPULister{},
		VPCLister{},
		FirewallLister{},
		RouteLister{},
		RouterLister{},
		AddressLister{},
		LoadBalancerLister{},
		DNSLister{},
		VPNLister{},
		InterconnectLister{},
		PSCLister{},
		// Last, and together: not compute, not data, not networking. The two
		// kinds you open with an audit question rather than an outage.
		CertificateLister{},
		SecretLister{},
		ServiceAccountLister{},
		IAMBindingLister{},
		KMSKeyLister{},
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
			var partial Result
			err := safely(loc, func() (err error) {
				partial, err = fn(ctx, loc)
				return err
			})

			mu.Lock()
			defer mu.Unlock()
			// Whatever the leg did produce is kept even when it then failed:
			// a page of results followed by a 403 on the next one is still a
			// page of results.
			result.Resources = append(result.Resources, partial.Resources...)
			result.Warnings = append(result.Warnings, partial.Warnings...)
			if err != nil {
				if w, ok := describeFailure(loc, err); ok {
					result.Warnings = append(result.Warnings, w)
				}
			}
		}(loc)
	}
	wg.Wait()

	sortResources(result.Resources)
	sortWarnings(result.Warnings)
	return result
}

// safely runs fn in the current goroutine, converting a panic into an error.
//
// Every goroutine g9s starts itself needs this. bubbletea recovers a panic in
// the command goroutine it started and restores the terminal on the way out,
// but recover only reaches its own goroutine's stack — a panic in a fan-out
// leg bypasses that entirely and takes the process down with the terminal
// still in raw mode and on the alternate screen. What the user gets is a shell
// that no longer echoes, with no message explaining it and nothing to do but
// `reset`. That is a far worse outcome than the missing rows.
//
// A malformed or unexpected API response reaching a row builder is the
// realistic trigger: listers index into slices and dereference fields that the
// proto or JSON says are optional. Turning that into one scope's warning is
// what every other partial failure here already does, so the table stays
// honest — it shows what could be read and names what could not.
func safely(scope string, fn func() error) (err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		// Deliberately not the standard logger: it writes to stderr, and
		// stderr is the terminal bubbletea is drawing on. A stack trace there
		// would scribble across the UI, which is the second-worst outcome
		// after the crash this is preventing. The stack goes to the debug file
		// when one is configured, and nowhere otherwise.
		writeCrashLog(scope, r, debug.Stack())
		err = internalError{detail: fmt.Sprintf("internal error: %v", r)}
	}()
	return fn()
}

// internalError marks a recovered panic, so describeFailure can classify it as
// a g9s bug rather than sorting it in with the failures a user can act on.
type internalError struct{ detail string }

func (e internalError) Error() string { return e.detail }

// debugLogEnv names the file to append panic stacks to. Unset means no file is
// written and no stack is kept.
const debugLogEnv = "G9S_DEBUG_LOG"

// writeCrashLog appends a recovered panic to the debug file, if one is set.
//
// Opt-in by environment variable rather than a default path: g9s writes
// nothing outside its credential directory unless asked, and a support
// engineer chasing a reproducible panic can set one variable and re-run.
// Failures to write are ignored on purpose — the panic is already handled, and
// a logging error is not worth a second failure path on top of it.
func writeCrashLog(scope string, recovered any, stack []byte) {
	path := os.Getenv(debugLogEnv)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n=== %s: recovered panic listing %s: %v\n%s\n",
		time.Now().Format(time.RFC3339), scope, recovered, stack)
}

// describeFailure turns an API error into a short warning, or "" if the error
// is not worth reporting.
//
// A region where the API is simply not enabled is not an error worth showing
// on every refresh — that is the normal state for most regions in most
// projects. Permission denied is worth showing exactly once per scope, since
// it is the difference between "nothing there" and "you cannot see it".
func describeFailure(scope string, err error) (Warning, bool) {
	// A recovered panic is a g9s bug and says so, rather than being sorted
	// into one of the categories the user could act on.
	var internal internalError
	if errors.As(err, &internal) {
		return scopeWarning(scope, ReasonInternal, internal.detail), true
	}

	// REST next. Roughly half the listers reach services with no gRPC surface
	// — Cloud SQL, DNS, IAM, Resource Manager, Storage — and those return an
	// *googleapi.Error that carries no gRPC code at all. Without this branch a
	// 403 fell through to the raw-message case and reached the user as a
	// truncated blob of JSON prose, which is the one error where knowing the
	// difference between "nothing there" and "you cannot see it" matters most.
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		if warning, handled := restReason(scope, apiErr); handled {
			return warning, warning != Warning{}
		}
		return scopeWarning(scope, ReasonUnknown, clip(apiErr.Message, 120)), true
	}

	switch grpcCode(err) {
	case codes.NotFound:
		return Warning{}, false
	case codes.PermissionDenied:
		return scopeWarning(scope, ReasonDenied, "permission denied"), true
	case codes.Unauthenticated:
		return scopeWarning(scope, ReasonUnauthenticated, "not authenticated"), true
	case codes.FailedPrecondition:
		// Usually "API not enabled for this project".
		if strings.Contains(strings.ToLower(err.Error()), "not been used") ||
			strings.Contains(strings.ToLower(err.Error()), "disabled") {
			return Warning{}, false
		}
	}

	msg := err.Error()
	if strings.Contains(msg, "SERVICE_DISABLED") || strings.Contains(msg, "accessNotConfigured") {
		return Warning{}, false
	}
	return scopeWarning(scope, ReasonUnknown, clip(msg, 120)), true
}

// restReason maps an HTTP error to the same vocabulary the gRPC branch uses.
//
// handled is false when the status carries no useful classification and the
// caller should fall back to the server's own message; a handled warning of ""
// means the error is the normal state and should not be reported at all.
//
// The subtlety is 403, which these APIs overload: it is both "you lack the
// permission" and "nobody enabled this API". Only the first is worth a warning
// — an unenabled API is the normal state for most services in most projects,
// and reporting it would put a line on every refresh forever. They are told
// apart by the reason field rather than by the message, which is prose and
// changes.
func restReason(scope string, err *googleapi.Error) (warning Warning, handled bool) {
	for _, item := range err.Errors {
		switch item.Reason {
		case "accessNotConfigured", "SERVICE_DISABLED":
			return Warning{}, true
		}
	}
	if strings.Contains(err.Message, "SERVICE_DISABLED") ||
		strings.Contains(err.Message, "has not been used in project") ||
		strings.Contains(err.Message, "accessNotConfigured") {
		return Warning{}, true
	}

	switch err.Code {
	case http.StatusNotFound:
		return Warning{}, true
	case http.StatusForbidden:
		return scopeWarning(scope, ReasonDenied, "permission denied"), true
	case http.StatusUnauthorized:
		return scopeWarning(scope, ReasonUnauthenticated, "not authenticated"), true
	}
	return Warning{}, false
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

// stableSortBy is sort.SliceStable with a comparison written in terms of the
// resources rather than their indices, which is how every lister's ordering
// actually reads.
func stableSortBy(resources []Resource, less func(a, b Resource) bool) {
	sort.SliceStable(resources, func(i, j int) bool {
		return less(resources[i], resources[j])
	})
}

// secondsDuration converts an API's seconds field to a Duration.
func secondsDuration(seconds int64) time.Duration {
	return time.Duration(seconds) * time.Second
}
