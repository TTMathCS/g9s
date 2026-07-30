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

// PSCLister lists Private Service Connect service attachments.
//
// PSC has two sides, and this is deliberately the producer one. A consumer PSC
// endpoint *is* a forwarding rule, so it already appears under Load Balancers —
// listing those here would double-count. Service attachments are the side with
// no other home: what this project publishes, and how many consumers are
// attached to it.
type PSCLister struct{}

func (PSCLister) Kind() Kind {
	return Kind{
		ID:    "psc",
		Title: "PSC Service Attachments",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "REGION", Width: 3},
			{Title: "TARGET", Width: 4},
			{Title: "ACCEPTS", Width: 3},
			{Title: "CONNECTED", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (PSCLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	client, err := compute.NewServiceAttachmentsRESTClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("service attachments client: %w", err)
	}
	defer client.Close()

	it := client.AggregatedList(ctx, &computepb.AggregatedListServiceAttachmentsRequest{
		Project: p.ProjectID,
	})
	return collectAggregated(it,
		func(pair compute.ServiceAttachmentsScopedListPair) (string, []*computepb.ServiceAttachment) {
			return lastSegment(pair.Key), pair.Value.GetServiceAttachments()
		},
		func(scope string, sa *computepb.ServiceAttachment) Resource {
			return serviceAttachmentResource(p, scope, sa)
		})
}

func serviceAttachmentResource(p config.Project, region string, sa *computepb.ServiceAttachment) Resource {
	name := sa.GetName()

	target := lastSegment(sa.GetTargetService())
	if target == "" {
		target = "-"
	}

	// ACCEPT_AUTOMATIC versus ACCEPT_MANUAL is the difference between "anyone
	// who asks" and "only the projects on the accept list".
	accepts := sa.GetConnectionPreference()
	if accepts == "" {
		accepts = "-"
	}

	return Resource{
		Name:     name,
		Location: region,
		// A service attachment has no lifecycle state; the per-consumer status
		// lives in ConnectedEndpoints, visible in the detail pane.
		Status: "ACTIVE",
		Row: []string{
			name,
			region,
			target,
			accepts,
			fmt.Sprintf("%d", len(sa.GetConnectedEndpoints())),
			age(sa.GetCreationTimestamp()),
		},
		Raw: sa,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/net-services/psc/published/details/%s/%s?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}
