package gcp

import (
	"context"
	"fmt"
	"net/url"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// VPCLister lists VPC networks.
//
// Networks are global, so one call covers the project with no fan-out and no
// aggregation.
type VPCLister struct{}

func (VPCLister) Kind() Kind {
	return Kind{
		ID:    "vpc",
		Title: "VPC Networks",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "SUBNET MODE", Width: 3},
			{Title: "SUBNETS", Width: 2},
			{Title: "ROUTING", Width: 2},
			{Title: "MTU", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (VPCLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	client, err := compute.NewNetworksRESTClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("networks client: %w", err)
	}
	defer client.Close()

	it := client.List(ctx, &computepb.ListNetworksRequest{Project: p.ProjectID})
	return collectList(it, func(n *computepb.Network) Resource {
		return networkResource(p, n)
	})
}

func networkResource(p config.Project, n *computepb.Network) Resource {
	name := n.GetName()

	// Auto-mode creates a subnet in every region automatically; custom mode
	// does not. It is the first thing you check when a subnet is "missing".
	mode := "custom"
	if n.GetAutoCreateSubnetworks() {
		mode = "auto"
	}

	routing := n.GetRoutingConfig().GetRoutingMode()
	if routing == "" {
		routing = "-"
	}

	mtu := "-"
	if n.GetMtu() != 0 {
		mtu = fmt.Sprintf("%d", n.GetMtu())
	}

	return Resource{
		Name: name,
		// Networks are global; leaving Location empty would sort them
		// unpredictably against kinds that set it.
		Location: "global",
		// A network has no lifecycle state — existing is the healthy state.
		// See bucketResource for why a synthetic status beats an empty one.
		Status: "ACTIVE",
		Row: []string{
			name,
			mode,
			fmt.Sprintf("%d", len(n.GetSubnetworks())),
			routing,
			mtu,
			age(n.GetCreationTimestamp()),
		},
		Raw: n,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/networking/networks/details/%s?project=%s",
			url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}
