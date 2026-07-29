package gcp

import (
	"context"
	"fmt"
	"net/url"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// GKELister lists GKE clusters.
//
// Like Compute, this needs no region fan-out — GKE's ListClusters accepts
// location "-" in the parent to mean "every zone and every region", and
// returns them all from the global endpoint in one call.
type GKELister struct{}

func (GKELister) Kind() Kind {
	return Kind{
		ID:    "gke",
		Title: "GKE Clusters",
		Columns: []Column{
			{Title: "NAME", Width: 6},
			{Title: "LOCATION", Width: 3},
			{Title: "MODE", Width: 2},
			{Title: "NODES", Width: 2},
			{Title: "VERSION", Width: 3},
			{Title: "STATUS", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (GKELister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	client, err := container.NewClusterManagerClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("gke client: %w", err)
	}
	defer client.Close()

	resp, err := client.ListClusters(ctx, &containerpb.ListClustersRequest{
		Parent: fmt.Sprintf("projects/%s/locations/-", p.ProjectID),
	})
	if err != nil {
		return Result{}, err
	}

	var result Result
	for _, c := range resp.GetClusters() {
		result.Resources = append(result.Resources, clusterNodeResource(p, c))
	}
	// ListClusters also reports locations it could not reach, same partial
	// listing story as a fanOut leg — surface it the same way.
	for _, missing := range resp.GetMissingZones() {
		if w := describeFailure(missing, fmt.Errorf("location unreachable")); w != "" {
			result.Warnings = append(result.Warnings, w)
		}
	}

	sortResources(result.Resources)
	return result, nil
}

func clusterNodeResource(p config.Project, c *containerpb.Cluster) Resource {
	name := c.GetName()
	location := c.GetLocation()
	status := c.GetStatus().String()

	mode := "Standard"
	if c.GetAutopilot().GetEnabled() {
		mode = "Autopilot"
	}

	created := "-"
	if ts := c.GetCreateTime(); ts != "" {
		created = age(ts)
	}

	return Resource{
		Name:     name,
		Location: location,
		Status:   status,
		Row: []string{
			name,
			location,
			mode,
			fmt.Sprintf("%d", c.GetCurrentNodeCount()),
			c.GetCurrentMasterVersion(),
			status,
			created,
		},
		Raw: c,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/kubernetes/clusters/details/%s/%s/details?project=%s",
			url.PathEscape(location), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}
