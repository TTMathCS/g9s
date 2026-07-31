package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"cloud.google.com/go/compute/apiv1/computepb"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// maxHealthGroups bounds the getHealth calls one drill-down makes. Each backend
// group is its own request, and a URL map fanning out to a dozen services with
// several groups each would otherwise turn one keypress into a burst.
const maxHealthGroups = 40

// LoadBalancerHealthLister is the backend health behind one forwarding rule.
//
// This is the drill-down the mechanism was worth building for. Answering "which
// backends are actually passing health checks" means walking a chain — rule to
// target proxy to URL map to backend service to a getHealth call per backend
// group — which is four-plus round trips for a single row. Doing that for every
// load balancer on every refresh is out of the question; doing it for the one
// rule someone is looking at costs a keypress.
//
// It uses the generated compute client rather than the handwritten one the
// other compute kinds use, because the walk crosses six collections in both
// their global and regional forms, and one service object covers all twelve.
type LoadBalancerHealthLister struct{}

func (LoadBalancerHealthLister) ParentKind() string { return "lb" }

func (LoadBalancerHealthLister) Kind() Kind {
	return Kind{
		ID:    "lbhealth",
		Title: "Backend Health",
		Columns: []Column{
			{Title: "BACKEND", Width: 4},
			{Title: "SERVICE", Width: 3},
			{Title: "GROUP", Width: 3},
			{Title: "ENDPOINT", Width: 3},
			{Title: "HEALTH", Width: 2},
		},
	}
}

func (LoadBalancerHealthLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	rule, ok := parent.Raw.(*computepb.ForwardingRule)
	if !ok {
		return Result{}, fmt.Errorf("no forwarding rule data for %s", parent.Name)
	}

	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("compute client: %w", err)
	}

	services, warnings := backendServicesFor(ctx, svc, p, rule)
	result := Result{Warnings: warnings}
	if len(services) == 0 {
		return result, nil
	}

	health, healthWarnings := groupHealth(ctx, svc, p, services)
	result.Warnings = append(result.Warnings, healthWarnings...)
	result.Resources = health

	sortResources(result.Resources)
	return result, nil
}

// backendServicesFor resolves a forwarding rule to the backend services behind
// it, following whichever chain its scheme uses.
func backendServicesFor(ctx context.Context, svc *compute.Service, p config.Project, rule *computepb.ForwardingRule) ([]*compute.BackendService, []string) {
	// An internal passthrough load balancer points straight at its service,
	// with no proxy in between. The short chain, and the common one inside a
	// VPC.
	if ref := rule.GetBackendService(); ref != "" {
		bs, err := getBackendService(ctx, svc, p, ref)
		if err != nil {
			return nil, []string{"backend service: " + clip(err.Error(), 100)}
		}
		return []*compute.BackendService{bs}, nil
	}

	target := rule.GetTarget()
	if target == "" {
		return nil, []string{"this rule names no target — nothing to health check"}
	}

	collection, region, name := parseResourceURL(target)
	switch collection {
	case "targetHttpProxies", "targetHttpsProxies":
		mapRef, err := urlMapOfProxy(ctx, svc, p, collection, region, name)
		if err != nil {
			return nil, []string{"target proxy: " + clip(err.Error(), 100)}
		}
		return servicesOfURLMap(ctx, svc, p, mapRef)

	case "targetTcpProxies", "targetSslProxies":
		ref, err := serviceOfProxy(ctx, svc, p, collection, region, name)
		if err != nil {
			return nil, []string{"target proxy: " + clip(err.Error(), 100)}
		}
		bs, err := getBackendService(ctx, svc, p, ref)
		if err != nil {
			return nil, []string{"backend service: " + clip(err.Error(), 100)}
		}
		return []*compute.BackendService{bs}, nil

	default:
		// Target pools, target instances, VPN gateways and PSC service
		// attachments are all legitimate forwarding-rule targets with no
		// backend service anywhere in them. Saying which one it is beats an
		// empty table that looks like a failure.
		return nil, []string{fmt.Sprintf(
			"target is a %s — health checks live on backend services, which this kind of load balancer has none of",
			singular(collection))}
	}
}

// urlMapOfProxy reads the URL map off an HTTP or HTTPS proxy, global or
// regional. Four methods on four types that differ in nothing this cares
// about, so each branch reduces to the one field.
func urlMapOfProxy(ctx context.Context, svc *compute.Service, p config.Project, collection, region, name string) (string, error) {
	switch {
	case collection == "targetHttpProxies" && region == "":
		proxy, err := svc.TargetHttpProxies.Get(p.ProjectID, name).Context(ctx).Do()
		if err != nil {
			return "", err
		}
		return proxy.UrlMap, nil

	case collection == "targetHttpProxies":
		proxy, err := svc.RegionTargetHttpProxies.Get(p.ProjectID, region, name).Context(ctx).Do()
		if err != nil {
			return "", err
		}
		return proxy.UrlMap, nil

	case region == "":
		proxy, err := svc.TargetHttpsProxies.Get(p.ProjectID, name).Context(ctx).Do()
		if err != nil {
			return "", err
		}
		return proxy.UrlMap, nil

	default:
		proxy, err := svc.RegionTargetHttpsProxies.Get(p.ProjectID, region, name).Context(ctx).Do()
		if err != nil {
			return "", err
		}
		return proxy.UrlMap, nil
	}
}

func serviceOfProxy(ctx context.Context, svc *compute.Service, p config.Project, collection, region, name string) (string, error) {
	if collection == "targetSslProxies" {
		proxy, err := svc.TargetSslProxies.Get(p.ProjectID, name).Context(ctx).Do()
		if err != nil {
			return "", err
		}
		return proxy.Service, nil
	}
	if region == "" {
		proxy, err := svc.TargetTcpProxies.Get(p.ProjectID, name).Context(ctx).Do()
		if err != nil {
			return "", err
		}
		return proxy.Service, nil
	}
	proxy, err := svc.RegionTargetTcpProxies.Get(p.ProjectID, region, name).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return proxy.Service, nil
}

// servicesOfURLMap collects every backend service a URL map can route to.
//
// Not just the default: a map whose default is healthy while the service behind
// /api is down is the exact situation someone opens this table during.
func servicesOfURLMap(ctx context.Context, svc *compute.Service, p config.Project, mapRef string) ([]*compute.BackendService, []string) {
	if mapRef == "" {
		return nil, []string{"the target proxy names no URL map"}
	}

	_, region, name := parseResourceURL(mapRef)
	var (
		um  *compute.UrlMap
		err error
	)
	if region == "" {
		um, err = svc.UrlMaps.Get(p.ProjectID, name).Context(ctx).Do()
	} else {
		um, err = svc.RegionUrlMaps.Get(p.ProjectID, region, name).Context(ctx).Do()
	}
	if err != nil {
		return nil, []string{"url map: " + clip(err.Error(), 100)}
	}

	refs := urlMapServiceRefs(um)
	var (
		services []*compute.BackendService
		warnings []string
	)
	for _, ref := range refs {
		bs, err := getBackendService(ctx, svc, p, ref)
		if err != nil {
			// A backend bucket is a perfectly ordinary route target and is not
			// a backend service, so a miss here is not necessarily a fault.
			warnings = append(warnings, fmt.Sprintf("%s: %s", lastSegment(ref), clip(err.Error(), 80)))
			continue
		}
		services = append(services, bs)
	}
	return services, warnings
}

// urlMapServiceRefs returns the map's distinct backend service URLs, in a
// stable order: the default first, then whatever the path matchers route to.
func urlMapServiceRefs(um *compute.UrlMap) []string {
	var (
		refs []string
		seen = map[string]bool{}
	)
	add := func(ref string) {
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		refs = append(refs, ref)
	}

	add(um.DefaultService)
	for _, pm := range um.PathMatchers {
		if pm == nil {
			continue
		}
		add(pm.DefaultService)
		for _, pr := range pm.PathRules {
			if pr != nil {
				add(pr.Service)
			}
		}
	}
	return refs
}

func getBackendService(ctx context.Context, svc *compute.Service, p config.Project, ref string) (*compute.BackendService, error) {
	_, region, name := parseResourceURL(ref)
	if region == "" {
		return svc.BackendServices.Get(p.ProjectID, name).Context(ctx).Do()
	}
	return svc.RegionBackendServices.Get(p.ProjectID, region, name).Context(ctx).Do()
}

// groupHealth asks each backend group how it is doing, concurrently.
func groupHealth(ctx context.Context, svc *compute.Service, p config.Project, services []*compute.BackendService) ([]Resource, []string) {
	type job struct {
		service *compute.BackendService
		group   string
	}

	var jobs []job
	for _, bs := range services {
		for _, b := range bs.Backends {
			if b == nil || b.Group == "" {
				continue
			}
			if len(jobs) >= maxHealthGroups {
				break
			}
			jobs = append(jobs, job{service: bs, group: b.Group})
		}
	}

	var (
		mu        sync.Mutex
		resources []Resource
		warnings  []string
		wg        sync.WaitGroup
	)
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()

			health, err := healthOfGroup(ctx, svc, p, j.service, j.group)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// Serverless NEGs and a few other backend types have no health
				// concept at all and refuse the call. That is a fact about the
				// backend, not a failure of the listing.
				if w := describeFailure(lastSegment(j.group), err); w != "" {
					warnings = append(warnings, w)
				}
				return
			}
			for _, hs := range health.HealthStatus {
				if hs == nil {
					continue
				}
				resources = append(resources, backendHealthResource(p, j.service, j.group, hs))
			}
		}(j)
	}
	wg.Wait()

	if len(services) > 0 && len(jobs) == 0 {
		warnings = append(warnings, "the backend services behind this rule have no backend groups")
	}
	return resources, warnings
}

func healthOfGroup(ctx context.Context, svc *compute.Service, p config.Project, bs *compute.BackendService, group string) (*compute.BackendServiceGroupHealth, error) {
	ref := &compute.ResourceGroupReference{Group: group}
	if bs.Region == "" {
		return svc.BackendServices.GetHealth(p.ProjectID, bs.Name, ref).Context(ctx).Do()
	}
	return svc.RegionBackendServices.GetHealth(p.ProjectID, lastSegment(bs.Region), bs.Name, ref).Context(ctx).Do()
}

func backendHealthResource(p config.Project, bs *compute.BackendService, group string, hs *compute.HealthStatus) Resource {
	// A managed instance group backend names an instance; a network endpoint
	// group has no instance behind the endpoint, so the address is the only
	// identity it has.
	name := lastSegment(hs.Instance)
	if name == "" {
		name = hs.IpAddress
	}
	if name == "" {
		name = "-"
	}

	endpoint := hs.IpAddress
	if endpoint == "" {
		endpoint = "-"
	}
	if hs.Port != 0 {
		endpoint = fmt.Sprintf("%s:%d", endpoint, hs.Port)
	}

	state := hs.HealthState
	if state == "" {
		state = "UNKNOWN"
	}

	return Resource{
		Name:     name,
		Location: bs.Name,
		Status:   state,
		Row: []string{
			name,
			bs.Name,
			lastSegment(group),
			endpoint,
			state,
		},
		Raw: hs,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/net-services/loadbalancing/details/backendService/%s?project=%s",
			url.PathEscape(bs.Name), url.QueryEscape(p.ProjectID)),
	}
}

// parseResourceURL splits a compute resource URL into its collection, region
// and name. Region is "" for a global resource.
//
// Every reference between compute resources is a full URL like
//
//	https://www.googleapis.com/compute/v1/projects/P/regions/us-central1/targetHttpProxies/web
//
// and the chain walk needs all three parts of it: the collection decides which
// branch to follow, and global and regional resources live behind different
// methods on the same client.
func parseResourceURL(ref string) (collection, region, name string) {
	parts := strings.Split(strings.TrimSuffix(ref, "/"), "/")
	if len(parts) < 2 {
		return "", "", ref
	}

	name = parts[len(parts)-1]
	collection = parts[len(parts)-2]
	// ".../regions/<region>/<collection>/<name>" — the region sits two
	// segments before the collection.
	if len(parts) >= 4 && parts[len(parts)-4] == "regions" {
		region = parts[len(parts)-3]
	}
	return collection, region, name
}

// singular turns a collection name into something that reads in a sentence:
// "targetPools" becomes "target pool".
func singular(collection string) string {
	if collection == "" {
		return "target of an unrecognised kind"
	}
	words := strings.TrimSuffix(collection, "s")

	var b strings.Builder
	for i, r := range words {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
