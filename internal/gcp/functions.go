package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	cloudfunctions "google.golang.org/api/cloudfunctions/v2"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// CloudFunctionLister lists Cloud Functions.
//
// Regional service, global call: the v2 API takes `locations/-` for the parent
// and returns every location in one paginated call, with the ones that did not
// answer named in Unreachable. That is the same shape as Dataflow's aggregated
// sweep and the opposite of Cloud Run, whose v2 API refuses the wildcard — two
// services in the same family, two different answers, which is why each lister
// says which one it got.
//
// Both generations list here. A gen-1 function and a gen-2 function are
// different things underneath — gen 2 is Cloud Run with a build attached — and
// the column saying which is the first thing anyone asks when one behaves
// differently from its neighbour.
type CloudFunctionLister struct{}

func (CloudFunctionLister) Kind() Kind {
	return Kind{
		ID:    "functions",
		Title: "Cloud Functions",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "REGION", Width: 2},
			{Title: "GEN", Width: 1},
			{Title: "RUNTIME", Width: 2},
			{Title: "TRIGGER", Width: 4},
			{Title: "STATE", Width: 2},
			{Title: "UPDATED", Width: 2},
		},
	}
}

func (CloudFunctionLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := cloudfunctions.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("cloud functions client: %w", err)
	}

	var (
		result      Result
		unreachable = map[string]bool{}
	)
	err = svc.Projects.Locations.Functions.List("projects/"+p.ProjectID+"/locations/-").
		Pages(ctx, func(page *cloudfunctions.ListFunctionsResponse) error {
			for _, loc := range page.Unreachable {
				unreachable[loc] = true
			}
			for _, f := range page.Functions {
				if f != nil {
					result.Resources = append(result.Resources, functionResource(p, f))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	for _, loc := range sortedKeys(unreachable) {
		if w, ok := describeFailure(loc, fmt.Errorf("location unreachable")); ok {
			result.Warnings = append(result.Warnings, w)
		}
	}

	sortResources(result.Resources)
	return result, nil
}

func functionResource(p config.Project, f *cloudfunctions.Function) Resource {
	name := lastSegment(f.Name)
	region := functionRegion(f.Name)

	state := f.State
	if state == "" || state == "STATE_UNSPECIFIED" {
		state = "UNKNOWN"
	}

	return Resource{
		Name:     name,
		Location: region,
		Status:   state,
		Row: []string{
			name,
			region,
			functionGeneration(f),
			functionRuntime(f),
			functionTrigger(f),
			state,
			age(f.UpdateTime),
		},
		Raw: f,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/functions/details/%s/%s?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// functionRegion pulls the location out of the resource name, which is the only
// place it appears — the wildcard call returns no location field of its own.
func functionRegion(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		if part == "locations" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return "-"
}

// functionGeneration distinguishes the two, because they are not the same
// product wearing one name: gen 2 is Cloud Run underneath, with the scaling,
// concurrency and timeout behaviour that implies.
func functionGeneration(f *cloudfunctions.Function) string {
	switch f.Environment {
	case "GEN_1":
		return "1"
	case "GEN_2":
		return "2"
	default:
		return "-"
	}
}

func functionRuntime(f *cloudfunctions.Function) string {
	if f.BuildConfig == nil || f.BuildConfig.Runtime == "" {
		return "-"
	}
	return f.BuildConfig.Runtime
}

// functionTrigger says what actually invokes the function.
//
// The distinction that matters: an HTTP function is reachable by anyone the IAM
// policy allows, an event-driven one only fires on its source. Reading a
// function's blast radius starts here.
func functionTrigger(f *cloudfunctions.Function) string {
	trigger := f.EventTrigger
	if trigger == nil {
		return "HTTP"
	}

	if topic := trigger.PubsubTopic; topic != "" {
		return "pubsub: " + lastSegment(topic)
	}
	if event := trigger.EventType; event != "" {
		// The full type is a reverse-DNS mouthful — google.cloud.storage.
		// object.v1.finalized — and the tail is the part that differs.
		return shortEventType(event)
	}
	return "event"
}

// shortEventType keeps the readable tail of a reverse-DNS event type.
func shortEventType(event string) string {
	parts := strings.Split(event, ".")
	if len(parts) <= 3 {
		return event
	}
	return strings.Join(parts[len(parts)-3:], ".")
}
