package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// RouterNATLister lists the Cloud NAT configurations inside one Cloud Router.
// NAT is not its own Compute resource: it is an inline Router.Nats entry, so a
// project-wide NAT tab would duplicate routers while discarding their parent.
type RouterNATLister struct{}

func (RouterNATLister) ParentKind() string { return "routers" }

func (RouterNATLister) Kind() Kind {
	return Kind{
		ID:    "routernats",
		Title: "NAT Gateways",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "TYPE", Width: 2},
			{Title: "IP ALLOCATION", Width: 3},
			{Title: "NAT IPS", Width: 3},
			{Title: "SOURCE RANGES", Width: 4},
			{Title: "MIN PORTS/VM", Width: 2},
			{Title: "LOGGING", Width: 2},
		},
	}
}

func (RouterNATLister) List(_ context.Context, _ *config.Config, p config.Project, parent Resource, _ []option.ClientOption) (Result, error) {
	router, ok := parent.Raw.(*compute.Router)
	if !ok {
		return Result{}, fmt.Errorf("no router data for %s", parent.Name)
	}

	var result Result
	for _, nat := range router.Nats {
		if nat != nil {
			result.Resources = append(result.Resources, routerNATResource(p, router, nat))
		}
	}
	sortResources(result.Resources)
	return result, nil
}

func routerNATResource(p config.Project, router *compute.Router, nat *compute.RouterNat) Resource {
	region := lastSegment(router.Region)

	kind := nat.Type
	if kind == "" {
		kind = "PUBLIC"
	}

	minPorts := "default"
	if nat.MinPortsPerVm > 0 {
		minPorts = fmt.Sprintf("%d", nat.MinPortsPerVm)
	}

	return Resource{
		Name:     nat.Name,
		Location: region,
		Status:   "ACTIVE",
		Row: []string{
			nat.Name,
			strings.ToLower(kind),
			strings.ToLower(dashIfEmpty(nat.NatIpAllocateOption)),
			routerNATIPs(nat),
			routerNATSources(nat),
			minPorts,
			routerNATLogging(nat),
		},
		Raw: nat,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/net-services/nat/details/%s/%s/%s?project=%s",
			url.PathEscape(region), url.PathEscape(router.Name), url.PathEscape(nat.Name),
			url.QueryEscape(p.ProjectID)),
	}
}

func routerNATIPs(nat *compute.RouterNat) string {
	if strings.EqualFold(nat.NatIpAllocateOption, "AUTO_ONLY") {
		return "automatic"
	}
	if len(nat.NatIps) == 0 {
		return "-"
	}

	names := make([]string, 0, len(nat.NatIps))
	for _, ref := range nat.NatIps {
		names = append(names, lastSegment(ref))
	}
	return strings.Join(names, ",")
}

func routerNATSources(nat *compute.RouterNat) string {
	mode := nat.SourceSubnetworkIpRangesToNat
	if mode != "LIST_OF_SUBNETWORKS" {
		return strings.ToLower(dashIfEmpty(mode))
	}
	if len(nat.Subnetworks) == 0 {
		return "-"
	}

	names := make([]string, 0, len(nat.Subnetworks))
	for _, subnet := range nat.Subnetworks {
		if subnet != nil {
			names = append(names, lastSegment(subnet.Name))
		}
	}
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, ",")
}

func routerNATLogging(nat *compute.RouterNat) string {
	if nat.LogConfig == nil || !nat.LogConfig.Enable {
		return "off"
	}
	if nat.LogConfig.Filter == "" {
		return "on"
	}
	return strings.ToLower(nat.LogConfig.Filter)
}
