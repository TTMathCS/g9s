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

// VPNLister lists Cloud VPN tunnels.
//
// Regional, but aggregatedList sweeps every region server-side, so no fan-out.
// Unlike most networking resources a tunnel has a real status worth colouring —
// ESTABLISHED versus a handshake that never completed is the whole question.
type VPNLister struct{}

func (VPNLister) Kind() Kind {
	return Kind{
		ID:    "vpn",
		Title: "VPN Tunnels",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "REGION", Width: 3},
			{Title: "PEER IP", Width: 3},
			{Title: "GATEWAY", Width: 3},
			{Title: "STATUS", Width: 3},
			{Title: "AGE", Width: 2},
		},
	}
}

func (VPNLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	client, err := compute.NewVpnTunnelsRESTClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("vpn tunnels client: %w", err)
	}
	defer client.Close()

	it := client.AggregatedList(ctx, &computepb.AggregatedListVpnTunnelsRequest{
		Project: p.ProjectID,
	})
	return collectAggregated(it,
		func(pair compute.VpnTunnelsScopedListPair) (string, []*computepb.VpnTunnel) {
			return lastSegment(pair.Key), pair.Value.GetVpnTunnels()
		},
		func(scope string, t *computepb.VpnTunnel) Resource {
			return vpnTunnelResource(p, scope, t)
		})
}

func vpnTunnelResource(p config.Project, region string, t *computepb.VpnTunnel) Resource {
	name := t.GetName()
	status := t.GetStatus()

	peer := t.GetPeerIp()
	if peer == "" {
		// A tunnel to another GCP gateway has no peer IP, it has a gateway ref.
		if gcp := t.GetPeerGcpGateway(); gcp != "" {
			peer = lastSegment(gcp) + " (gcp)"
		} else {
			peer = "-"
		}
	}

	// Either the modern HA VPN gateway or the classic target gateway is set.
	gateway := lastSegment(t.GetVpnGateway())
	if gateway == "" {
		gateway = lastSegment(t.GetTargetVpnGateway())
	}
	if gateway == "" {
		gateway = "-"
	}

	return Resource{
		Name:     name,
		Location: region,
		Status:   status,
		Row: []string{
			name,
			region,
			peer,
			gateway,
			status,
			age(t.GetCreationTimestamp()),
		},
		Raw: t,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/hybrid/vpn/tunnels/details/%s/%s?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}
