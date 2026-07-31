package gcp

import (
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
)

func TestSubnetResourceShape(t *testing.T) {
	r := subnetResource(testProject(), "us-central1", testSubnet())

	if r.Name != "prod-us-central1" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Row[2] != "10.128.0.0/20" {
		t.Errorf("range cell = %q", r.Row[2])
	}
	// Named, not bare: the ranges alone do not say which is pods and which is
	// services, and that is the whole question a GKE range is opened for.
	if r.Row[3] != "pods=10.4.0.0/14 services=10.8.0.0/20" {
		t.Errorf("secondary ranges cell = %q", r.Row[3])
	}
	if r.Row[4] != "10.128.0.1" {
		t.Errorf("gateway cell = %q", r.Row[4])
	}
	if r.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE when the API reports no state", r.Status)
	}
}

func TestSubnetWithNoSecondaryRanges(t *testing.T) {
	r := subnetResource(testProject(), "us-east1", &computepb.Subnetwork{
		Name:        strPtr("plain"),
		IpCidrRange: strPtr("10.142.0.0/20"),
	})
	if r.Row[3] != "-" {
		t.Errorf("secondary ranges cell = %q, want a dash", r.Row[3])
	}
	if r.Row[4] != "-" {
		t.Errorf("gateway cell = %q, want a dash", r.Row[4])
	}
}

func TestPrivateGoogleAccessIsAlwaysStated(t *testing.T) {
	// Off by default, and the reason a VM with no external IP silently cannot
	// reach Cloud Storage. A blank cell would read as "not applicable".
	off := subnetResource(testProject(), "us-east1", &computepb.Subnetwork{Name: strPtr("s")})
	if off.Row[5] != "private off" {
		t.Errorf("access cell = %q, want it to say off", off.Row[5])
	}

	on := subnetResource(testProject(), "us-east1", &computepb.Subnetwork{
		Name: strPtr("s"), PrivateIpGoogleAccess: boolPtr(true),
	})
	if on.Row[5] != "private on" {
		t.Errorf("access cell = %q", on.Row[5])
	}
}

func TestFlowLogSummary(t *testing.T) {
	tests := []struct {
		name string
		in   *computepb.Subnetwork
		want string
	}{
		{"absent", &computepb.Subnetwork{}, "off"},
		{"disabled config", &computepb.Subnetwork{
			LogConfig: &computepb.SubnetworkLogConfig{Enable: boolPtr(false)},
		}, "off"},
		{"fully sampled", &computepb.Subnetwork{
			LogConfig: &computepb.SubnetworkLogConfig{Enable: boolPtr(true), FlowSampling: float32Ptr(1)},
		}, "on"},
		// The sampling rate matters as much as on/off: logs at 1% are on for
		// billing purposes and absent for the incident you wanted them for.
		{"sampled down", &computepb.Subnetwork{
			LogConfig: &computepb.SubnetworkLogConfig{Enable: boolPtr(true), FlowSampling: float32Ptr(0.01)},
		}, "on 1%"},
		// The pre-LogConfig field is still set on older subnets.
		{"legacy field", &computepb.Subnetwork{EnableFlowLogs: boolPtr(true)}, "on"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := flowLogSummary(tt.in); got != tt.want {
				t.Errorf("flowLogSummary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSubnetStateReportsAResize(t *testing.T) {
	r := subnetResource(testProject(), "us-east1", &computepb.Subnetwork{
		Name: strPtr("growing"), State: strPtr("DRAINING"),
	})
	if r.Status != "DRAINING" {
		t.Errorf("Status = %q, want the API's state when it reports one", r.Status)
	}
}

func TestSubnetsAreFilteredToTheParentNetwork(t *testing.T) {
	// The aggregated call returns every subnet in the project. In a shared-VPC
	// project two networks routinely hold identically-named subnets, so the
	// network is the only thing telling them apart.
	mine := subnetResource(testProject(), "us-central1", &computepb.Subnetwork{
		Name:    strPtr("app"),
		Network: strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/global/networks/prod-vpc"),
	})
	theirs := subnetResource(testProject(), "us-central1", &computepb.Subnetwork{
		Name:    strPtr("app"),
		Network: strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/global/networks/dev-vpc"),
	})

	got := subnetsOfNetwork([]Resource{mine, theirs}, testNetwork())
	if len(got) != 1 {
		t.Fatalf("kept %d subnets, want only the one on prod-vpc", len(got))
	}
	s, _ := got[0].Raw.(*computepb.Subnetwork)
	if lastSegment(s.GetNetwork()) != "prod-vpc" {
		t.Errorf("kept the subnet on %q", s.GetNetwork())
	}
}

func TestNetworkFilterComparesTheNameNotTheWholeURL(t *testing.T) {
	// The two URLs come back from different calls, and the API is not
	// consistent about the host or api-version prefix it writes. Comparing the
	// full self-links silently matches nothing, which renders as a network
	// that has no subnets at all.
	shortForm := subnetResource(testProject(), "us-central1", &computepb.Subnetwork{
		Name:    strPtr("app"),
		Network: strPtr("projects/sandbox-123/global/networks/prod-vpc"),
	})

	if got := subnetsOfNetwork([]Resource{shortForm}, testNetwork()); len(got) != 1 {
		t.Error("a short-form network reference was not matched")
	}
}

func TestSubnetDrillDownRejectsAParentThatIsNotANetwork(t *testing.T) {
	_, err := (SubnetLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-a-network", Raw: testInstance()}, nil)
	if err == nil {
		t.Error("a non-network parent was accepted")
	}
}

func TestSubnetsAreNotSSHOrAirflowTargets(t *testing.T) {
	r := subnetResource(testProject(), "us-central1", testSubnet())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a subnet is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a subnet has an Airflow URI")
	}
}
