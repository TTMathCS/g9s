package gcp

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// FirewallLister lists VPC firewall rules.
//
// Global, one call. Rules are ordered by priority at evaluation time, so the
// table is sorted that way rather than by name — reading a firewall means
// reading it in the order the network does.
type FirewallLister struct{}

func (FirewallLister) Kind() Kind {
	return Kind{
		ID:    "fw",
		Title: "Firewall Rules",
		Columns: []Column{
			{Title: "PRIORITY", Width: 2},
			{Title: "NAME", Width: 5},
			{Title: "NETWORK", Width: 3},
			{Title: "DIRECTION", Width: 2},
			{Title: "ACTION", Width: 2},
			{Title: "SCOPE", Width: 4},
			{Title: "STATE", Width: 2},
		},
	}
}

func (FirewallLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	client, err := compute.NewFirewallsRESTClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("firewalls client: %w", err)
	}
	defer client.Close()

	it := client.List(ctx, &computepb.ListFirewallsRequest{Project: p.ProjectID})
	result, err := collectList(it, func(f *computepb.Firewall) Resource {
		return firewallResource(p, f)
	})
	if err != nil {
		return result, err
	}

	// Priority order is the order the network evaluates rules in, which is the
	// only order in which a rule set can actually be reasoned about.
	sortByFirewallPriority(result.Resources)
	return result, nil
}

func firewallResource(p config.Project, f *computepb.Firewall) Resource {
	name := f.GetName()

	// A rule carries either allow or deny entries, never both. They are
	// separate generated types with identical shape, hence the conversion.
	action := "ALLOW"
	rules := f.GetAllowed()
	if len(f.GetDenied()) > 0 {
		action = "DENY"
		rules = deniedAsAllowed(f.GetDenied())
	}

	direction := f.GetDirection()
	if direction == "" {
		direction = "INGRESS"
	}

	// A disabled rule is inert, which is worth seeing at a glance: a rule that
	// looks like it protects something but does not is a real hazard.
	state := "ENABLED"
	if f.GetDisabled() {
		state = "DISABLED"
	}

	return Resource{
		Name:     name,
		Location: "global",
		Status:   state,
		Row: []string{
			fmt.Sprintf("%d", f.GetPriority()),
			name,
			lastSegment(f.GetNetwork()),
			direction,
			action,
			firewallScope(f, rules),
			state,
		},
		Raw: f,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/networking/firewalls/details/%s?project=%s",
			url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// firewallScope summarises what a rule matches: the protocols and ports it
// covers, and the address range it applies to. The full lists live in the
// detail pane; this is the at-a-glance version.
func firewallScope(f *computepb.Firewall, rules []*computepb.Allowed) string {
	protos := make([]string, 0, len(rules))
	for _, r := range rules {
		proto := r.GetIPProtocol()
		if ports := r.GetPorts(); len(ports) > 0 {
			proto += ":" + strings.Join(ports, ",")
		}
		protos = append(protos, proto)
	}

	// Egress rules match on destination, ingress on source.
	ranges := f.GetSourceRanges()
	if strings.EqualFold(f.GetDirection(), "EGRESS") {
		ranges = f.GetDestinationRanges()
	}

	scope := strings.Join(protos, " ")
	if len(ranges) > 0 {
		scope += " from " + strings.Join(ranges, ",")
		if strings.EqualFold(f.GetDirection(), "EGRESS") {
			scope = strings.Join(protos, " ") + " to " + strings.Join(ranges, ",")
		}
	}
	if scope == "" {
		return "-"
	}
	return scope
}

// sortByFirewallPriority orders rules the way the network evaluates them:
// lowest priority number first, then by name so equal priorities are stable.
func sortByFirewallPriority(resources []Resource) {
	sort.SliceStable(resources, func(i, j int) bool {
		pi, pj := firewallPriority(resources[i]), firewallPriority(resources[j])
		if pi != pj {
			return pi < pj
		}
		return resources[i].Name < resources[j].Name
	})
}

func firewallPriority(r Resource) int32 {
	f, ok := r.Raw.(*computepb.Firewall)
	if !ok {
		return 0
	}
	return f.GetPriority()
}

// Denied entries have the same shape as Allowed ones but a distinct generated
// type, so they are converted rather than duplicating the summary logic.
func deniedAsAllowed(denied []*computepb.Denied) []*computepb.Allowed {
	out := make([]*computepb.Allowed, 0, len(denied))
	for _, d := range denied {
		out = append(out, &computepb.Allowed{
			IPProtocol: d.IPProtocol,
			Ports:      d.Ports,
		})
	}
	return out
}
