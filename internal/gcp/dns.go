package gcp

import (
	"context"
	"fmt"
	"net/url"

	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// DNSLister lists Cloud DNS managed zones.
//
// Zones are global and paginated. Like Cloud SQL this is the generated REST
// client rather than a handwritten one, so it lives in google.golang.org/api
// and costs no new dependency.
type DNSLister struct{}

func (DNSLister) Kind() Kind {
	return Kind{
		ID:    "dns",
		Title: "Cloud DNS Zones",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "DNS NAME", Width: 5},
			{Title: "VISIBILITY", Width: 2},
			{Title: "NAME SERVERS", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (DNSLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := dns.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("dns client: %w", err)
	}

	var result Result
	err = svc.ManagedZones.List(p.ProjectID).Pages(ctx, func(page *dns.ManagedZonesListResponse) error {
		for _, z := range page.ManagedZones {
			result.Resources = append(result.Resources, dnsZoneResource(p, z))
		}
		return nil
	})
	if err != nil {
		return result, err
	}

	sortResources(result.Resources)
	return result, nil
}

func dnsZoneResource(p config.Project, z *dns.ManagedZone) Resource {
	visibility := z.Visibility
	if visibility == "" {
		// The API omits it for public zones, which is the default.
		visibility = "public"
	}

	return Resource{
		Name:     z.Name,
		Location: "global",
		// A zone has no lifecycle state; existing is the healthy state.
		Status: "ACTIVE",
		Row: []string{
			z.Name,
			z.DnsName,
			visibility,
			fmt.Sprintf("%d", len(z.NameServers)),
			age(z.CreationTime),
		},
		Raw: z,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/net-services/dns/zones/%s/details?project=%s",
			url.PathEscape(z.Name), url.QueryEscape(p.ProjectID)),
	}
}
