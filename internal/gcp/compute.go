package gcp

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// ComputeLister lists Compute Engine instances.
//
// Compute is the one service in this tool that does not need a region fan-out:
// AggregatedList returns every zone in a single paginated call.
type ComputeLister struct{}

func (ComputeLister) Kind() Kind {
	return Kind{
		ID:    "vm",
		Title: "VM Instances",
		Columns: []Column{
			{Title: "NAME", Width: 6},
			{Title: "ZONE", Width: 3},
			{Title: "MACHINE TYPE", Width: 3},
			{Title: "INTERNAL IP", Width: 3},
			{Title: "EXTERNAL IP", Width: 3},
			{Title: "STATUS", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (ComputeLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("compute client: %w", err)
	}
	defer client.Close()

	var result Result
	it := client.AggregatedList(ctx, &computepb.AggregatedListInstancesRequest{
		Project: p.ProjectID,
	})

	for {
		pair, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			// A hard failure here means the whole listing failed, not one
			// scope, so surface it rather than degrading to an empty table.
			return result, err
		}
		if pair.Value == nil {
			continue
		}

		zone := lastSegment(pair.Key)
		for _, inst := range pair.Value.Instances {
			result.Resources = append(result.Resources, instanceResource(p, zone, inst))
		}
	}

	sortResources(result.Resources)
	return result, nil
}

func instanceResource(p config.Project, zone string, inst *computepb.Instance) Resource {
	name := inst.GetName()
	if zone == "" {
		zone = lastSegment(inst.GetZone())
	}

	internal, external := instanceIPs(inst)

	return Resource{
		Name:     name,
		Location: zone,
		Status:   inst.GetStatus(),
		Row: []string{
			name,
			zone,
			lastSegment(inst.GetMachineType()),
			internal,
			external,
			inst.GetStatus(),
			age(inst.GetCreationTimestamp()),
		},
		Raw: inst,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/compute/instancesDetail/zones/%s/instances/%s?project=%s",
			url.PathEscape(zone), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// instanceIPs pulls the primary internal and external addresses out of the
// instance's network interfaces. Both are commonly absent, so both default to
// a dash rather than an empty cell.
func instanceIPs(inst *computepb.Instance) (internal, external string) {
	internal, external = "-", "-"
	for _, nic := range inst.GetNetworkInterfaces() {
		if internal == "-" && nic.GetNetworkIP() != "" {
			internal = nic.GetNetworkIP()
		}
		for _, ac := range nic.GetAccessConfigs() {
			if ac.GetNatIP() != "" {
				external = ac.GetNatIP()
				break
			}
		}
	}
	return internal, external
}

// age renders an RFC3339 timestamp as a compact relative age, k9s style.
func age(timestamp string) string {
	if timestamp == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return "-"
	}
	return shortDuration(time.Since(t))
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// resourceNamePattern is the shape GCP guarantees for an instance name or a
// zone: a lowercase letter, then lowercase letters, digits and hyphens.
//
// Checked rather than assumed because both values become argv for `gcloud
// compute ssh`. They arrive from an API response, and a name that could pass
// for a flag — or carry a shell metacharacter, for whoever wraps this in a
// shell next — must not reach a command line. GCP will not mint one; this is
// the guard for the day something upstream returns one anyway.
var resourceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// SSHTarget reports the zone and instance name for a compute resource, for the
// `gcloud compute ssh` action.
func SSHTarget(r Resource) (name, zone string, ok bool) {
	inst, isInstance := r.Raw.(*computepb.Instance)
	if !isInstance {
		return "", "", false
	}
	if !strings.EqualFold(inst.GetStatus(), "RUNNING") {
		return "", "", false
	}

	name, zone = inst.GetName(), lastSegment(inst.GetZone())
	if !resourceNamePattern.MatchString(name) || !resourceNamePattern.MatchString(zone) {
		return "", "", false
	}
	return name, zone, true
}
