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

// InterconnectLister lists Cloud Interconnect VLAN attachments.
//
// Attachments are the per-region pieces that carry traffic, which is why they
// are listed rather than the Interconnect circuits themselves: a circuit being
// up says nothing about whether a given VPC can reach it. Regional, swept by
// aggregatedList in one call.
type InterconnectLister struct{}

func (InterconnectLister) Kind() Kind {
	return Kind{
		ID:    "interconnect",
		Title: "Interconnect Attachments",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "REGION", Width: 3},
			{Title: "TYPE", Width: 2},
			{Title: "BANDWIDTH", Width: 2},
			{Title: "VLAN", Width: 2},
			{Title: "STATE", Width: 3},
			{Title: "AGE", Width: 2},
		},
	}
}

func (InterconnectLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	client, err := compute.NewInterconnectAttachmentsRESTClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("interconnect attachments client: %w", err)
	}
	defer client.Close()

	it := client.AggregatedList(ctx, &computepb.AggregatedListInterconnectAttachmentsRequest{
		Project: p.ProjectID,
	})
	return collectAggregated(it,
		func(pair compute.InterconnectAttachmentsScopedListPair) (string, []*computepb.InterconnectAttachment) {
			return lastSegment(pair.Key), pair.Value.GetInterconnectAttachments()
		},
		func(scope string, a *computepb.InterconnectAttachment) Resource {
			return interconnectAttachmentResource(p, scope, a)
		})
}

func interconnectAttachmentResource(p config.Project, region string, a *computepb.InterconnectAttachment) Resource {
	name := a.GetName()
	state := a.GetState()

	bandwidth := a.GetBandwidth()
	if bandwidth == "" {
		bandwidth = "-"
	}

	kind := a.GetType()
	if kind == "" {
		kind = "-"
	}

	vlan := "-"
	if a.GetVlanTag8021Q() != 0 {
		vlan = fmt.Sprintf("%d", a.GetVlanTag8021Q())
	}

	// An attachment can be administratively disabled while its state still
	// reads ACTIVE, which would otherwise look entirely healthy.
	if !a.GetAdminEnabled() {
		state = "DISABLED"
	}

	return Resource{
		Name:     name,
		Location: region,
		Status:   state,
		Row: []string{
			name,
			region,
			kind,
			bandwidth,
			vlan,
			state,
			age(a.GetCreationTimestamp()),
		},
		Raw: a,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/hybrid/interconnects/attachments/details/%s/%s?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}
