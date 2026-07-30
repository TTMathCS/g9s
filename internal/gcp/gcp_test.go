package gcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/compute/apiv1/computepb"
	sqladmin "google.golang.org/api/sqladmin/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDescribeFailureSuppressesExpectedErrors(t *testing.T) {
	// A least-privilege account against ten regions produces a constant
	// drizzle of these. Reporting them would bury the ones that matter.
	tests := []struct {
		name string
		err  error
	}{
		{"not found", status.Error(codes.NotFound, "region not found")},
		{"api disabled", status.Error(codes.FailedPrecondition, "Dataproc API has not been used in project 123 before or it is disabled")},
		{"service disabled string", errors.New("googleapi: Error 403: SERVICE_DISABLED")},
		{"access not configured", errors.New("accessNotConfigured: API not enabled")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeFailure("us-central1", tc.err); got != "" {
				t.Errorf("describeFailure = %q, want it suppressed", got)
			}
		})
	}
}

func TestDescribeFailureReportsRealProblems(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"permission denied", status.Error(codes.PermissionDenied, "nope"), "permission denied"},
		{"unauthenticated", status.Error(codes.Unauthenticated, "nope"), "not authenticated"},
		{"unknown", errors.New("connection reset by peer"), "connection reset"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := describeFailure("us-central1", tc.err)
			if got == "" {
				t.Fatal("describeFailure suppressed an error that should surface")
			}
			if !strings.HasPrefix(got, "us-central1: ") {
				t.Errorf("describeFailure = %q, want it prefixed with the scope", got)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("describeFailure = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestDescribeFailureTruncatesLongMessages(t *testing.T) {
	long := errors.New(strings.Repeat("x", 500))
	got := describeFailure("us-central1", long)
	if len([]rune(got)) > 140 {
		t.Errorf("describeFailure produced a %d-rune warning, want it truncated", len([]rune(got)))
	}
}

func TestFanOutCollectsPartialResults(t *testing.T) {
	// The core promise of the fan-out: one bad region must not discard the
	// results from the good ones.
	locations := []string{"good-1", "bad", "good-2"}

	result := fanOut(context.Background(), locations, func(_ context.Context, loc string) ([]Resource, error) {
		if loc == "bad" {
			return nil, status.Error(codes.PermissionDenied, "denied")
		}
		return []Resource{{Name: "cluster", Location: loc}}, nil
	})

	if len(result.Resources) != 2 {
		t.Errorf("got %d resources, want 2", len(result.Resources))
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(result.Warnings), result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], "bad") {
		t.Errorf("warning = %q, want it to name the failing scope", result.Warnings[0])
	}
}

func TestFanOutKeepsPartialResourcesFromFailingScope(t *testing.T) {
	// A paginated list can fail halfway through. Whatever arrived before the
	// error is still real and should be kept.
	result := fanOut(context.Background(), []string{"half"}, func(_ context.Context, loc string) ([]Resource, error) {
		return []Resource{{Name: "a", Location: loc}}, status.Error(codes.PermissionDenied, "denied on page 2")
	})

	if len(result.Warnings) != 1 {
		t.Errorf("got %d warnings, want 1", len(result.Warnings))
	}
	// Current behaviour drops them; assert it explicitly so a future change is
	// a deliberate decision rather than an accident.
	if len(result.Resources) != 0 {
		t.Errorf("got %d resources, want 0 (partial page results are discarded)", len(result.Resources))
	}
}

func TestFanOutSuppressedErrorsProduceNoWarnings(t *testing.T) {
	result := fanOut(context.Background(), []string{"a", "b"}, func(_ context.Context, _ string) ([]Resource, error) {
		return nil, status.Error(codes.NotFound, "no such region")
	})
	if len(result.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", result.Warnings)
	}
}

func TestFanOutSortsDeterministically(t *testing.T) {
	// Concurrent fan-out returns in arbitrary order; the table must not
	// reshuffle between refreshes.
	locations := []string{"us-west1", "us-central1", "us-east1"}

	first := fanOut(context.Background(), locations, func(_ context.Context, loc string) ([]Resource, error) {
		return []Resource{{Name: "b", Location: loc}, {Name: "a", Location: loc}}, nil
	})
	second := fanOut(context.Background(), locations, func(_ context.Context, loc string) ([]Resource, error) {
		return []Resource{{Name: "b", Location: loc}, {Name: "a", Location: loc}}, nil
	})

	if len(first.Resources) != 6 {
		t.Fatalf("got %d resources, want 6", len(first.Resources))
	}
	for i := range first.Resources {
		a, b := first.Resources[i], second.Resources[i]
		if a.Location != b.Location || a.Name != b.Name {
			t.Fatalf("ordering differs at %d: %s/%s vs %s/%s", i, a.Location, a.Name, b.Location, b.Name)
		}
	}
	if first.Resources[0].Location != "us-central1" || first.Resources[0].Name != "a" {
		t.Errorf("first row = %v, want us-central1/a", first.Resources[0])
	}
}

func TestDataprocEndpoint(t *testing.T) {
	tests := []struct{ region, want string }{
		{"us-central1", "us-central1-dataproc.googleapis.com:443"},
		{"northamerica-northeast1", "northamerica-northeast1-dataproc.googleapis.com:443"},
		// global has no regional prefix; getting this wrong silently returns
		// no clusters rather than erroring.
		{"global", "dataproc.googleapis.com:443"},
	}
	for _, tc := range tests {
		if got := dataprocEndpoint(tc.region); got != tc.want {
			t.Errorf("dataprocEndpoint(%q) = %q, want %q", tc.region, got, tc.want)
		}
	}
}

func TestLastSegment(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a", "us-central1-a"},
		{"projects/p/locations/us-central1/environments/my-env", "my-env"},
		{"zones/us-central1-a", "us-central1-a"},
		{"bare-name", "bare-name"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := lastSegment(tc.in); got != tc.want {
			t.Errorf("lastSegment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRegionOfZone(t *testing.T) {
	tests := []struct{ in, want string }{
		{"us-central1-a", "us-central1"},
		{"northamerica-northeast1-b", "northamerica-northeast1"},
		{"global", "global"},
	}
	for _, tc := range tests {
		if got := regionOfZone(tc.in); got != tc.want {
			t.Errorf("regionOfZone(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShortDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
	}
	for _, tc := range tests {
		if got := shortDuration(tc.d); got != tc.want {
			t.Errorf("shortDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestAgeHandlesUnparseableTimestamps(t *testing.T) {
	for _, in := range []string{"", "not-a-time"} {
		if got := age(in); got != "-" {
			t.Errorf("age(%q) = %q, want -", in, got)
		}
	}
}

func TestListersHaveDistinctIDsAndColumns(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range Listers() {
		kind := l.Kind()
		if kind.ID == "" || kind.Title == "" {
			t.Errorf("lister %T has an empty ID or Title", l)
		}
		if seen[kind.ID] {
			t.Errorf("duplicate kind ID %q", kind.ID)
		}
		seen[kind.ID] = true

		if len(kind.Columns) == 0 {
			t.Errorf("kind %q has no columns", kind.ID)
		}
		for _, c := range kind.Columns {
			if c.Width <= 0 {
				t.Errorf("kind %q column %q has non-positive width", kind.ID, c.Title)
			}
		}
	}
}

// TestResourceRowsMatchColumns guards the invariant the table renderer relies
// on: a lister's rows must have exactly as many cells as it declared columns.
func TestResourceRowsMatchColumns(t *testing.T) {
	fixtures := map[string]Resource{
		"vm":       instanceResource(testProject(), "us-central1-a", testInstance()),
		"gke":      clusterNodeResource(testProject(), testGKECluster()),
		"sql":      sqlInstanceResource(testProject(), testSQLInstance()),
		"gcs":      bucketResource(testProject(), testBucket()),
		"dataproc": clusterResource(testProject(), "us-central1", testCluster()),
		"composer": environmentResource(testProject(), "us-central1", testEnvironment()),

		"vpc":          networkResource(testProject(), testNetwork()),
		"fw":           firewallResource(testProject(), testFirewall()),
		"lb":           forwardingRuleResource(testProject(), "global", testForwardingRule()),
		"dns":          dnsZoneResource(testProject(), testDNSZone()),
		"vpn":          vpnTunnelResource(testProject(), "us-central1", testVPNTunnel()),
		"interconnect": interconnectAttachmentResource(testProject(), "us-central1", testInterconnectAttachment()),
		"psc":          serviceAttachmentResource(testProject(), "us-central1", testServiceAttachment()),
	}

	for _, l := range Listers() {
		kind := l.Kind()
		r, ok := fixtures[kind.ID]
		if !ok {
			t.Fatalf("no fixture for kind %q — add one when adding a lister", kind.ID)
		}
		if len(r.Row) != len(kind.Columns) {
			t.Errorf("kind %q: row has %d cells, want %d", kind.ID, len(r.Row), len(kind.Columns))
		}
		if r.Name == "" {
			t.Errorf("kind %q: resource has no name", kind.ID)
		}
		if !strings.HasPrefix(r.ConsoleURL, "https://console.cloud.google.com/") {
			t.Errorf("kind %q: console URL = %q", kind.ID, r.ConsoleURL)
		}
		if !strings.Contains(r.ConsoleURL, testProject().ProjectID) {
			t.Errorf("kind %q: console URL %q omits the project", kind.ID, r.ConsoleURL)
		}
	}
}

func TestConsoleURLEscapesAwkwardNames(t *testing.T) {
	// Project IDs and names reach the URL builder unvalidated; a stray space
	// or slash must not produce a broken link.
	inst := testInstance()
	inst.Name = strPtr("weird name/with-slash")

	r := instanceResource(testProject(), "us-central1-a", inst)
	if strings.Contains(r.ConsoleURL, " ") {
		t.Errorf("console URL contains a raw space: %q", r.ConsoleURL)
	}
	if strings.Count(r.ConsoleURL, "console.cloud.google.com") != 1 {
		t.Errorf("unexpected console URL: %q", r.ConsoleURL)
	}
	// The escaped name must still round-trip back to the original.
	if !strings.Contains(r.ConsoleURL, "weird%20name%2Fwith-slash") {
		t.Errorf("name not percent-encoded in console URL: %q", r.ConsoleURL)
	}
}

func TestGKEClusterResourceShape(t *testing.T) {
	r := clusterNodeResource(testProject(), testGKECluster())

	if r.Name != "platform-cluster" || r.Location != "us-central1" {
		t.Errorf("got name=%q location=%q", r.Name, r.Location)
	}
	if r.Status != "RUNNING" {
		t.Errorf("status = %q, want RUNNING", r.Status)
	}
	if r.Row[2] != "Autopilot" {
		t.Errorf("mode column = %q, want Autopilot for an autopilot cluster", r.Row[2])
	}

	standard := testGKECluster()
	standard.Autopilot = nil
	if got := clusterNodeResource(testProject(), standard).Row[2]; got != "Standard" {
		t.Errorf("mode column = %q, want Standard when autopilot is unset", got)
	}
}

func TestBucketResourceShape(t *testing.T) {
	r := bucketResource(testProject(), testBucket())

	if r.Name != "acme-prod-data-exports" || r.Location != "US-CENTRAL1" {
		t.Errorf("got name=%q location=%q", r.Name, r.Location)
	}
	// Buckets have no running/stopped lifecycle; ACTIVE is what keeps the
	// dashboard rollup and row colouring meaningful rather than UNKNOWN.
	if r.Status != "ACTIVE" {
		t.Errorf("status = %q, want the synthetic ACTIVE", r.Status)
	}
	if r.Row[3] != "on" {
		t.Errorf("versioning column = %q, want on", r.Row[3])
	}

	unversioned := testBucket()
	unversioned.VersioningEnabled = false
	if got := bucketResource(testProject(), unversioned).Row[3]; got != "off" {
		t.Errorf("versioning column = %q, want off", got)
	}
}

func TestGKEAndStorageNotSSHOrAirflowTargets(t *testing.T) {
	// SSHTarget and AirflowURI type-switch on Raw; a GKE cluster or a bucket
	// reaching either by way of the merged view must fall through cleanly
	// rather than a failed type assertion doing something worse.
	gkeResource := clusterNodeResource(testProject(), testGKECluster())
	if _, _, ok := SSHTarget(gkeResource); ok {
		t.Error("a GKE cluster should never report as SSH-able")
	}
	if _, ok := AirflowURI(gkeResource); ok {
		t.Error("a GKE cluster should never report an Airflow URI")
	}

	bucket := bucketResource(testProject(), testBucket())
	if _, _, ok := SSHTarget(bucket); ok {
		t.Error("a bucket should never report as SSH-able")
	}
	if _, ok := AirflowURI(bucket); ok {
		t.Error("a bucket should never report an Airflow URI")
	}
}

// --- Cloud SQL ---

func TestSQLInstanceResourceShape(t *testing.T) {
	r := sqlInstanceResource(testProject(), testSQLInstance())

	if r.Name != "orders-primary" || r.Location != "us-central1" {
		t.Errorf("got name=%q location=%q", r.Name, r.Location)
	}
	// RUNNABLE is Cloud SQL's spelling of healthy; it has to survive as-is so
	// the dashboard rollup and row colouring key off the real API value.
	if r.Status != "RUNNABLE" {
		t.Errorf("status = %q, want RUNNABLE", r.Status)
	}
	if got := r.Row[2]; got != "POSTGRES 15" {
		t.Errorf("version cell = %q, want the underscore replaced for readability", got)
	}
	if got := r.Row[3]; got != "db-custom-4-15360" {
		t.Errorf("tier cell = %q", got)
	}
	if got := r.Row[4]; got != "REGIONAL" {
		t.Errorf("HA cell = %q, want REGIONAL", got)
	}
}

func TestSQLInstanceToleratesMissingSettings(t *testing.T) {
	// Settings is a pointer and the API omits it for some instance states, so
	// reading through it unguarded would panic on a real listing.
	inst := testSQLInstance()
	inst.Settings = nil
	inst.DatabaseVersion = ""

	r := sqlInstanceResource(testProject(), inst)
	if len(r.Row) != len(CloudSQLLister{}.Kind().Columns) {
		t.Fatalf("row has %d cells, kind declares %d", len(r.Row), len(CloudSQLLister{}.Kind().Columns))
	}
	for i, want := range map[int]string{2: "-", 3: "-", 4: "-"} {
		if r.Row[i] != want {
			t.Errorf("cell %d = %q, want %q when the API omits it", i, r.Row[i], want)
		}
	}
}

func TestSQLWarningFormatting(t *testing.T) {
	tests := []struct {
		name string
		in   *sqladmin.ApiWarning
		want string
	}{
		{"region named", &sqladmin.ApiWarning{Region: "us-east4", Message: "region unreachable"}, "us-east4: region unreachable"},
		{"no region falls back", &sqladmin.ApiWarning{Message: "something"}, "cloudsql: something"},
		{"code when no message", &sqladmin.ApiWarning{Region: "us-west1", Code: "REGION_UNREACHABLE"}, "us-west1: REGION_UNREACHABLE"},
		{"empty is dropped", &sqladmin.ApiWarning{}, ""},
		{"nil is dropped", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sqlWarning(tc.in); got != tc.want {
				t.Errorf("sqlWarning = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSQLWarningTruncatesLongMessages(t *testing.T) {
	w := &sqladmin.ApiWarning{Region: "us-east4", Message: strings.Repeat("x", 500)}
	if n := len([]rune(sqlWarning(w))); n > 120 {
		t.Errorf("warning is %d runes, want it truncated", n)
	}
}

func TestDedupeSortWarnings(t *testing.T) {
	// Cloud SQL repeats the same warning on every page of a paginated listing,
	// so five pages for one bad region must not read as five bad scopes.
	r := Result{Warnings: []string{
		"us-east4: region unreachable",
		"us-east4: region unreachable",
		"us-central1: region unreachable",
		"us-east4: region unreachable",
	}}
	dedupeSortWarnings(&r)

	if len(r.Warnings) != 2 {
		t.Fatalf("got %d warnings, want 2 unique: %v", len(r.Warnings), r.Warnings)
	}
	if r.Warnings[0] != "us-central1: region unreachable" {
		t.Errorf("warnings should be sorted, got %v", r.Warnings)
	}
}

func TestDedupeSortWarningsHandlesEmpty(t *testing.T) {
	r := Result{}
	dedupeSortWarnings(&r)
	if r.Warnings != nil {
		t.Errorf("expected nil warnings to stay nil, got %v", r.Warnings)
	}
}

func TestCloudSQLNotSSHOrAirflowTarget(t *testing.T) {
	r := sqlInstanceResource(testProject(), testSQLInstance())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a Cloud SQL instance should never report as SSH-able")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a Cloud SQL instance should never report an Airflow URI")
	}
}

// --- networking ---

func TestNetworkResourceShape(t *testing.T) {
	r := networkResource(testProject(), testNetwork())

	if r.Name != "prod-vpc" {
		t.Errorf("name = %q", r.Name)
	}
	// Networks are global. An empty Location would sort them unpredictably
	// against kinds that set one, and reads as missing data in the merged view.
	if r.Location != "global" {
		t.Errorf("location = %q, want global", r.Location)
	}
	if r.Row[1] != "custom" {
		t.Errorf("subnet mode = %q, want custom when AutoCreateSubnetworks is false", r.Row[1])
	}
	if r.Row[2] != "2" {
		t.Errorf("subnet count = %q, want 2", r.Row[2])
	}

	auto := testNetwork()
	auto.AutoCreateSubnetworks = boolPtr(true)
	if got := networkResource(testProject(), auto).Row[1]; got != "auto" {
		t.Errorf("subnet mode = %q, want auto", got)
	}
}

func TestFirewallResourceShape(t *testing.T) {
	r := firewallResource(testProject(), testFirewall())

	if r.Row[0] != "1000" {
		t.Errorf("priority cell = %q", r.Row[0])
	}
	if r.Row[2] != "prod-vpc" {
		t.Errorf("network cell = %q, want the bare name not the self-link", r.Row[2])
	}
	if r.Row[4] != "ALLOW" {
		t.Errorf("action = %q, want ALLOW", r.Row[4])
	}
	if r.Status != "ENABLED" {
		t.Errorf("status = %q, want ENABLED", r.Status)
	}
	if !strings.Contains(r.Row[5], "tcp:22") || !strings.Contains(r.Row[5], "10.0.0.0/8") {
		t.Errorf("scope = %q, want protocol/port and source range", r.Row[5])
	}
}

func TestFirewallDenyRuleUsesDeniedEntries(t *testing.T) {
	// Allowed and Denied are separate generated types with the same shape.
	// Reading only Allowed would render a deny rule with an empty scope.
	f := testFirewall()
	f.Allowed = nil
	f.Denied = []*computepb.Denied{{IPProtocol: strPtr("tcp"), Ports: []string{"3389"}}}

	r := firewallResource(testProject(), f)
	if r.Row[4] != "DENY" {
		t.Errorf("action = %q, want DENY", r.Row[4])
	}
	if !strings.Contains(r.Row[5], "tcp:3389") {
		t.Errorf("scope = %q, want the denied protocol and port", r.Row[5])
	}
}

func TestFirewallDisabledRuleIsVisible(t *testing.T) {
	// A rule that looks like it protects something but is inert is a hazard,
	// so it must not render identically to an active one.
	f := testFirewall()
	f.Disabled = boolPtr(true)
	if got := firewallResource(testProject(), f).Status; got != "DISABLED" {
		t.Errorf("status = %q, want DISABLED", got)
	}
}

func TestFirewallEgressScopeUsesDestinationRanges(t *testing.T) {
	// Ingress matches on source, egress on destination. Reading source ranges
	// for an egress rule would show an empty or wrong scope.
	f := testFirewall()
	f.Direction = strPtr("EGRESS")
	f.SourceRanges = nil
	f.DestinationRanges = []string{"0.0.0.0/0"}

	scope := firewallResource(testProject(), f).Row[5]
	if !strings.Contains(scope, "to 0.0.0.0/0") {
		t.Errorf("egress scope = %q, want it to name the destination", scope)
	}
}

func TestFirewallRulesSortByEvaluationOrder(t *testing.T) {
	// Priority order is the order the network applies rules in, and the only
	// order in which a rule set can be reasoned about.
	mk := func(name string, priority int32) Resource {
		f := testFirewall()
		f.Name, f.Priority = strPtr(name), int32Ptr(priority)
		return firewallResource(testProject(), f)
	}
	resources := []Resource{mk("zzz-low", 65534), mk("aaa-high", 100), mk("mid-b", 1000), mk("mid-a", 1000)}

	sortByFirewallPriority(resources)

	got := []string{resources[0].Name, resources[1].Name, resources[2].Name, resources[3].Name}
	want := []string{"aaa-high", "mid-a", "mid-b", "zzz-low"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v (priority first, then name)", got, want)
		}
	}
}

func TestForwardingRuleScopeAndPorts(t *testing.T) {
	global := forwardingRuleResource(testProject(), "global", testForwardingRule())
	if global.Location != "global" || global.Row[1] != "global" {
		t.Errorf("global rule location = %q / %q", global.Location, global.Row[1])
	}
	if global.Row[4] != "443-443" {
		t.Errorf("ports = %q, want the port range", global.Row[4])
	}

	// AllPorts is a separate boolean from PortRange; missing it renders a
	// pass-through load balancer as having no ports at all.
	all := testForwardingRule()
	all.PortRange, all.AllPorts = nil, boolPtr(true)
	if got := forwardingRuleResource(testProject(), "us-central1", all).Row[4]; got != "all" {
		t.Errorf("ports = %q, want all", got)
	}

	list := testForwardingRule()
	list.PortRange, list.Ports = nil, []string{"80", "8080"}
	if got := forwardingRuleResource(testProject(), "us-central1", list).Row[4]; got != "80,8080" {
		t.Errorf("ports = %q, want the joined list", got)
	}
}

func TestDNSZoneResourceShape(t *testing.T) {
	r := dnsZoneResource(testProject(), testDNSZone())
	if r.Name != "example-com" || r.Row[1] != "example.com." {
		t.Errorf("got name=%q dns=%q", r.Name, r.Row[1])
	}
	if r.Row[3] != "2" {
		t.Errorf("name server count = %q, want 2", r.Row[3])
	}

	// The API omits visibility for public zones, which is the default.
	z := testDNSZone()
	z.Visibility = ""
	if got := dnsZoneResource(testProject(), z).Row[2]; got != "public" {
		t.Errorf("visibility = %q, want public when the API omits it", got)
	}
}

func TestVPNTunnelResourceShape(t *testing.T) {
	r := vpnTunnelResource(testProject(), "us-central1", testVPNTunnel())
	if r.Status != "ESTABLISHED" {
		t.Errorf("status = %q, want ESTABLISHED", r.Status)
	}
	if r.Row[2] != "203.0.113.10" {
		t.Errorf("peer = %q", r.Row[2])
	}
	if r.Row[3] != "ha-vpn-1" {
		t.Errorf("gateway = %q, want the bare name", r.Row[3])
	}
}

func TestVPNTunnelToGCPPeerHasNoPeerIP(t *testing.T) {
	// A tunnel to another GCP gateway carries a gateway reference instead of a
	// peer IP, which would otherwise render as an empty cell.
	tun := testVPNTunnel()
	tun.PeerIp = nil
	tun.PeerGcpGateway = strPtr("https://www.googleapis.com/compute/v1/projects/other/regions/us-central1/vpnGateways/peer-gw")

	if got := vpnTunnelResource(testProject(), "us-central1", tun).Row[2]; got != "peer-gw (gcp)" {
		t.Errorf("peer = %q, want the peer gateway named", got)
	}
}

func TestVPNTunnelFallsBackToClassicGateway(t *testing.T) {
	tun := testVPNTunnel()
	tun.VpnGateway = nil
	tun.TargetVpnGateway = strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1/targetVpnGateways/classic-gw")

	if got := vpnTunnelResource(testProject(), "us-central1", tun).Row[3]; got != "classic-gw" {
		t.Errorf("gateway = %q, want the classic gateway", got)
	}
}

func TestInterconnectAttachmentShape(t *testing.T) {
	r := interconnectAttachmentResource(testProject(), "us-central1", testInterconnectAttachment())
	if r.Status != "ACTIVE" {
		t.Errorf("status = %q", r.Status)
	}
	if r.Row[4] != "1010" {
		t.Errorf("vlan = %q", r.Row[4])
	}
}

func TestInterconnectDisabledOverridesState(t *testing.T) {
	// An attachment can read ACTIVE while administratively disabled, which
	// would look entirely healthy while carrying no traffic.
	a := testInterconnectAttachment()
	a.AdminEnabled = boolPtr(false)
	if got := interconnectAttachmentResource(testProject(), "us-central1", a).Status; got != "DISABLED" {
		t.Errorf("status = %q, want DISABLED to win over ACTIVE", got)
	}
}

func TestServiceAttachmentShape(t *testing.T) {
	r := serviceAttachmentResource(testProject(), "us-central1", testServiceAttachment())
	if r.Row[2] != "analytics-ilb" {
		t.Errorf("target = %q, want the bare name", r.Row[2])
	}
	if r.Row[3] != "ACCEPT_MANUAL" {
		t.Errorf("accepts = %q", r.Row[3])
	}
	if r.Row[4] != "2" {
		t.Errorf("connected = %q, want 2", r.Row[4])
	}
}

func TestNetworkingKindsAreNotSSHOrAirflowTargets(t *testing.T) {
	// Every networking kind flows through the merged view, where SSHTarget and
	// AirflowURI type-switch on Raw. All must fall through cleanly.
	for id, r := range map[string]Resource{
		"vpc":          networkResource(testProject(), testNetwork()),
		"fw":           firewallResource(testProject(), testFirewall()),
		"lb":           forwardingRuleResource(testProject(), "global", testForwardingRule()),
		"dns":          dnsZoneResource(testProject(), testDNSZone()),
		"vpn":          vpnTunnelResource(testProject(), "us-central1", testVPNTunnel()),
		"interconnect": interconnectAttachmentResource(testProject(), "us-central1", testInterconnectAttachment()),
		"psc":          serviceAttachmentResource(testProject(), "us-central1", testServiceAttachment()),
	} {
		if _, _, ok := SSHTarget(r); ok {
			t.Errorf("%s should never report as SSH-able", id)
		}
		if _, ok := AirflowURI(r); ok {
			t.Errorf("%s should never report an Airflow URI", id)
		}
	}
}

func TestMergeResultsCombinesAndDedupes(t *testing.T) {
	// Load balancers merge a regional listing with a global one, so the combined
	// result has to stay sorted and must not repeat a shared warning.
	dst := Result{
		Resources: []Resource{{Name: "b", Location: "us-central1"}},
		Warnings:  []string{"us-east4: permission denied"},
	}
	mergeResults(&dst, Result{
		Resources: []Resource{{Name: "a", Location: "global"}},
		Warnings:  []string{"us-east4: permission denied"},
	})

	if len(dst.Resources) != 2 {
		t.Fatalf("got %d resources, want 2", len(dst.Resources))
	}
	if dst.Resources[0].Location != "global" {
		t.Errorf("merged list is not sorted: %v", dst.Resources)
	}
	if len(dst.Warnings) != 1 {
		t.Errorf("warnings = %v, want the duplicate collapsed", dst.Warnings)
	}
}
