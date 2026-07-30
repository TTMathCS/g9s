package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// LoadBalancerLister lists forwarding rules — the front door of every GCP load
// balancer, and the thing that actually holds the IP address people ask about.
//
// This is the one networking kind that needs two calls: a load balancer is
// either regional or global, and those live in different collections. Regional
// rules come back through aggregatedList in one call; global ones need their
// own. Missing the global call would silently hide every external HTTP(S) load
// balancer, which is usually the one someone is looking for.
type LoadBalancerLister struct{}

func (LoadBalancerLister) Kind() Kind {
	return Kind{
		ID:    "lb",
		Title: "Load Balancers",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "SCOPE", Width: 3},
			{Title: "IP ADDRESS", Width: 3},
			{Title: "PROTOCOL", Width: 2},
			{Title: "PORTS", Width: 2},
			{Title: "SCHEME", Width: 3},
			{Title: "AGE", Width: 2},
		},
	}
}

func (LoadBalancerLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	var result Result

	regional, err := listRegionalForwardingRules(ctx, p, opts)
	if err != nil {
		return result, err
	}
	mergeResults(&result, regional)

	global, err := listGlobalForwardingRules(ctx, p, opts)
	if err != nil {
		// The regional half already succeeded; returning it with the error lets
		// the caller decide rather than throwing away a good half-listing.
		return result, err
	}
	mergeResults(&result, global)

	return result, nil
}

func listRegionalForwardingRules(ctx context.Context, p config.Project, opts []option.ClientOption) (Result, error) {
	client, err := compute.NewForwardingRulesRESTClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("forwarding rules client: %w", err)
	}
	defer client.Close()

	it := client.AggregatedList(ctx, &computepb.AggregatedListForwardingRulesRequest{
		Project: p.ProjectID,
	})
	return collectAggregated(it,
		func(pair compute.ForwardingRulesScopedListPair) (string, []*computepb.ForwardingRule) {
			return lastSegment(pair.Key), pair.Value.GetForwardingRules()
		},
		func(scope string, r *computepb.ForwardingRule) Resource {
			return forwardingRuleResource(p, scope, r)
		})
}

func listGlobalForwardingRules(ctx context.Context, p config.Project, opts []option.ClientOption) (Result, error) {
	client, err := compute.NewGlobalForwardingRulesRESTClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("global forwarding rules client: %w", err)
	}
	defer client.Close()

	it := client.List(ctx, &computepb.ListGlobalForwardingRulesRequest{Project: p.ProjectID})
	return collectList(it, func(r *computepb.ForwardingRule) Resource {
		return forwardingRuleResource(p, "global", r)
	})
}

func forwardingRuleResource(p config.Project, scope string, r *computepb.ForwardingRule) Resource {
	name := r.GetName()

	ports := r.GetPortRange()
	if ports == "" {
		if list := r.GetPorts(); len(list) > 0 {
			ports = strings.Join(list, ",")
		} else if r.GetAllPorts() {
			ports = "all"
		} else {
			ports = "-"
		}
	}

	scheme := r.GetLoadBalancingScheme()
	if scheme == "" {
		scheme = "-"
	}

	ip := r.GetIPAddress()
	if ip == "" {
		ip = "-"
	}

	// A regional rule's Console URL needs its region; a global one must not
	// carry a region parameter at all.
	consoleURL := fmt.Sprintf(
		"https://console.cloud.google.com/net-services/loadbalancing/details/http/%s?project=%s",
		url.PathEscape(name), url.QueryEscape(p.ProjectID))

	return Resource{
		Name:     name,
		Location: scope,
		// A forwarding rule has no lifecycle state of its own; its health lives
		// in the backend service, which is a separate kind.
		Status: "ACTIVE",
		Row: []string{
			name,
			scope,
			ip,
			r.GetIPProtocol(),
			ports,
			scheme,
			age(r.GetCreationTimestamp()),
		},
		Raw:        r,
		ConsoleURL: consoleURL,
	}
}
