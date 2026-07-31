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

// SubnetLister is the subnets inside one VPC network.
//
// The VPC row already counts them and offers no way to see them, which is the
// same gap the DNS zones row had. A count answers "is auto mode on"; it does
// not answer the question people actually open a network for, which is which
// range a given region is on and whether anything is left in it.
//
// One aggregatedList call covers every region server-side, then the results are
// filtered to this network. Listing subnets project-wide instead would flatten
// away which network each belongs to, and in a shared-VPC project that is the
// only thing distinguishing two identically-named subnets.
type SubnetLister struct{}

func (SubnetLister) ParentKind() string { return "vpc" }

func (SubnetLister) Kind() Kind {
	return Kind{
		ID:    "subnets",
		Title: "Subnets",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "REGION", Width: 2},
			{Title: "IP RANGE", Width: 3},
			{Title: "SECONDARY RANGES", Width: 4},
			{Title: "GATEWAY", Width: 2},
			{Title: "ACCESS", Width: 2},
			{Title: "FLOW LOGS", Width: 2},
		},
	}
}

func (SubnetLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	network, ok := parent.Raw.(*computepb.Network)
	if !ok {
		return Result{}, fmt.Errorf("no network data for %s", parent.Name)
	}

	client, err := compute.NewSubnetworksRESTClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("subnetworks client: %w", err)
	}
	defer client.Close()

	it := client.AggregatedList(ctx, &computepb.AggregatedListSubnetworksRequest{
		Project: p.ProjectID,
	})

	result, err := collectAggregated(it,
		func(pair compute.SubnetworksScopedListPair) (string, []*computepb.Subnetwork) {
			return lastSegment(pair.Key), pair.Value.GetSubnetworks()
		},
		func(scope string, s *computepb.Subnetwork) Resource {
			return subnetResource(p, scope, s)
		})
	if err != nil {
		return result, err
	}

	result.Resources = subnetsOfNetwork(result.Resources, network)
	return result, nil
}

// subnetsOfNetwork keeps only the subnets belonging to one network.
//
// Compared on the last URL segment rather than the whole self-link: the two
// come back from different calls and the API is not consistent about the host
// or the api-version prefix it writes into them, so comparing the full strings
// silently matches nothing.
func subnetsOfNetwork(resources []Resource, network *computepb.Network) []Resource {
	want := network.GetName()
	if want == "" {
		return nil
	}

	out := resources[:0]
	for _, r := range resources {
		s, ok := r.Raw.(*computepb.Subnetwork)
		if !ok {
			continue
		}
		if lastSegment(s.GetNetwork()) == want {
			out = append(out, r)
		}
	}
	return out
}

func subnetResource(p config.Project, region string, s *computepb.Subnetwork) Resource {
	name := s.GetName()

	gateway := s.GetGatewayAddress()
	if gateway == "" {
		gateway = "-"
	}

	// Private Google access is what decides whether a VM with no external IP
	// can reach a Google API at all, and it is off by default — so a subnet
	// full of instances that cannot talk to Cloud Storage looks completely
	// healthy until you check this column.
	access := "private off"
	if s.GetPrivateIpGoogleAccess() {
		access = "private on"
	}

	return Resource{
		Name:     name,
		Location: region,
		// A subnet has no lifecycle of its own except while it is being
		// resized, which is the one state worth colouring.
		Status: subnetState(s),
		Row: []string{
			name,
			region,
			dashIfEmpty(s.GetIpCidrRange()),
			secondaryRanges(s),
			gateway,
			access,
			flowLogSummary(s),
		},
		Raw: s,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/networking/subnetworks/details/%s/%s?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// subnetState reports DRAINING while a range is being resized, ACTIVE
// otherwise. The API leaves the field empty in the ordinary case.
func subnetState(s *computepb.Subnetwork) string {
	if state := s.GetState(); state != "" {
		return state
	}
	return "ACTIVE"
}

// secondaryRanges renders the alias ranges, which is where GKE puts pods and
// services. A cluster that cannot schedule because its pod range is full is
// diagnosed here and almost nowhere else.
func secondaryRanges(s *computepb.Subnetwork) string {
	ranges := s.GetSecondaryIpRanges()
	if len(ranges) == 0 {
		return "-"
	}

	parts := make([]string, 0, len(ranges))
	for _, r := range ranges {
		if r == nil {
			continue
		}
		// Named rather than bare: the range alone does not say which is pods
		// and which is services, and that is the whole question.
		parts = append(parts, fmt.Sprintf("%s=%s", r.GetRangeName(), r.GetIpCidrRange()))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

// flowLogSummary says whether VPC flow logs are on, and how hard.
//
// The sampling rate matters as much as the on/off: logs sampled at 1% are on
// for billing purposes and absent for the incident you wanted them for.
func flowLogSummary(s *computepb.Subnetwork) string {
	cfg := s.GetLogConfig()
	if cfg == nil || !cfg.GetEnable() {
		// EnableFlowLogs is the older field and still set on subnets created
		// before LogConfig existed.
		if s.GetEnableFlowLogs() {
			return "on"
		}
		return "off"
	}
	if rate := cfg.GetFlowSampling(); rate > 0 && rate < 1 {
		return fmt.Sprintf("on %.0f%%", rate*100)
	}
	return "on"
}
