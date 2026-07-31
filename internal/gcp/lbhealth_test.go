package gcp

import (
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	compute "google.golang.org/api/compute/v1"
)

func TestBackendHealthResourceShape(t *testing.T) {
	bs := testBackendService()
	r := backendHealthResource(testProject(), bs, bs.Backends[0].Group, testHealthStatus())

	if r.Name != "web-01" {
		t.Errorf("Name = %q, want the instance's short name", r.Name)
	}
	if r.Row[1] != "prod-web-backend" {
		t.Errorf("service cell = %q", r.Row[1])
	}
	if r.Row[2] != "web-mig" {
		t.Errorf("group cell = %q, want the group's short name", r.Row[2])
	}
	if r.Row[3] != "10.0.0.12:8080" {
		t.Errorf("endpoint cell = %q", r.Row[3])
	}
	if r.Status != "HEALTHY" {
		t.Errorf("Status = %q", r.Status)
	}
}

func TestNetworkEndpointHasNoInstanceToName(t *testing.T) {
	// A managed instance group backend names an instance; a network endpoint
	// group has none, so the address is the only identity the row has.
	bs := testBackendService()
	r := backendHealthResource(testProject(), bs, bs.Backends[0].Group, &compute.HealthStatus{
		HealthState: "UNHEALTHY",
		IpAddress:   "10.4.0.9",
		Port:        443,
	})

	if r.Name != "10.4.0.9" {
		t.Errorf("Name = %q, want the address to stand in", r.Name)
	}
	if r.Status != "UNHEALTHY" {
		t.Errorf("Status = %q", r.Status)
	}
}

func TestHealthStateAbsentIsNotHealthy(t *testing.T) {
	// A blank state must never render as an empty cell beside healthy rows —
	// "no answer" and "passing" are the two things that must not look alike.
	bs := testBackendService()
	r := backendHealthResource(testProject(), bs, bs.Backends[0].Group, &compute.HealthStatus{
		IpAddress: "10.4.0.9",
	})
	if r.Status != "UNKNOWN" {
		t.Errorf("Status = %q, want UNKNOWN", r.Status)
	}
	// No port reported means the endpoint is the address alone, not ":0".
	if r.Row[3] != "10.4.0.9" {
		t.Errorf("endpoint cell = %q", r.Row[3])
	}
}

func TestParseResourceURL(t *testing.T) {
	// Every reference between compute resources is a full URL, and the walk
	// needs all three parts: the collection picks the branch, and global and
	// regional resources live behind different methods.
	tests := []struct {
		name                       string
		in                         string
		collection, region, target string
	}{
		{
			"global proxy",
			"https://www.googleapis.com/compute/v1/projects/p/global/targetHttpProxies/web",
			"targetHttpProxies", "", "web",
		},
		{
			"regional proxy",
			"https://www.googleapis.com/compute/v1/projects/p/regions/us-central1/targetHttpProxies/web",
			"targetHttpProxies", "us-central1", "web",
		},
		{
			"regional backend service",
			"https://www.googleapis.com/compute/v1/projects/p/regions/europe-west4/backendServices/api",
			"backendServices", "europe-west4", "api",
		},
		{
			"zonal instance group is not regional",
			"https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/instanceGroups/web-mig",
			"instanceGroups", "", "web-mig",
		},
		{"bare name", "web", "", "", "web"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collection, region, name := parseResourceURL(tt.in)
			if collection != tt.collection || region != tt.region || name != tt.target {
				t.Errorf("parseResourceURL = (%q, %q, %q), want (%q, %q, %q)",
					collection, region, name, tt.collection, tt.region, tt.target)
			}
		})
	}
}

func TestURLMapServiceRefsCoversEveryRoute(t *testing.T) {
	// A map whose default is healthy while the service behind /api is down is
	// the exact situation this table gets opened during, so the default alone
	// is not enough.
	um := &compute.UrlMap{
		DefaultService: "projects/p/global/backendServices/web",
		PathMatchers: []*compute.PathMatcher{{
			Name:           "all",
			DefaultService: "projects/p/global/backendServices/web",
			PathRules: []*compute.PathRule{
				{Service: "projects/p/global/backendServices/api"},
				{Service: "projects/p/global/backendServices/static"},
				// A repeat must not produce a duplicate getHealth call.
				{Service: "projects/p/global/backendServices/api"},
			},
		}},
	}

	refs := urlMapServiceRefs(um)
	if len(refs) != 3 {
		t.Fatalf("got %d services, want 3 distinct: %v", len(refs), refs)
	}
	// The default leads, because it is what an unmatched request reaches.
	if lastSegment(refs[0]) != "web" {
		t.Errorf("first service = %q, want the default", refs[0])
	}
}

func TestURLMapWithNoRoutesAtAll(t *testing.T) {
	if refs := urlMapServiceRefs(&compute.UrlMap{}); len(refs) != 0 {
		t.Errorf("an empty url map produced %v", refs)
	}
	// A path matcher present but empty must not contribute a blank reference,
	// which would become a Get for a resource with no name.
	refs := urlMapServiceRefs(&compute.UrlMap{PathMatchers: []*compute.PathMatcher{{}, nil}})
	if len(refs) != 0 {
		t.Errorf("empty path matchers produced %v", refs)
	}
}

func TestHealthDrillDownRejectsAParentThatIsNotAForwardingRule(t *testing.T) {
	_, err := (LoadBalancerHealthLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-a-rule", Raw: testInstance()}, nil)
	if err == nil {
		t.Error("a non-forwarding-rule parent was accepted")
	}
}

func TestSingularReadsInASentence(t *testing.T) {
	// This ends up inside "target is a %s — health checks live on backend
	// services…", so it has to read as English rather than as an API noun.
	tests := []struct{ in, want string }{
		{"targetPools", "target pool"},
		{"targetVpnGateways", "target vpn gateway"},
		{"serviceAttachments", "service attachment"},
		{"", "target of an unrecognised kind"},
	}
	for _, tt := range tests {
		if got := singular(tt.in); got != tt.want {
			t.Errorf("singular(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRuleWithNoTargetSaysSoRatherThanFailing(t *testing.T) {
	// A forwarding rule with neither a target nor a backend service is not an
	// error — it is a rule with nothing to health check, and an empty table
	// with no explanation reads as a broken listing.
	rule := &computepb.ForwardingRule{Name: strPtr("orphan")}
	_, warnings := backendServicesFor(t.Context(), nil, testProject(), rule)

	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want one explaining the empty table: %v", len(warnings), warnings)
	}
}

func TestBackendHealthRowsAreNotSSHOrAirflowTargets(t *testing.T) {
	// These name real VMs, so the ssh check is worth pinning: the row is a
	// health record, not the instance, and s must not offer to shell into it.
	bs := testBackendService()
	r := backendHealthResource(testProject(), bs, bs.Backends[0].Group, testHealthStatus())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a backend health row is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a backend health row has an Airflow URI")
	}
}
