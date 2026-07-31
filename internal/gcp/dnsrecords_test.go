package gcp

import (
	"testing"

	dns "google.golang.org/api/dns/v1"
)

func TestRecordSetResourceShape(t *testing.T) {
	r := recordSetResource(testProject(), testDNSZone(), testRecordSet())

	// Every record in the zone ends with the zone's suffix, which costs a third
	// of the column to repeat on every row.
	if r.Name != "api" {
		t.Errorf("Name = %q, want the zone suffix trimmed", r.Name)
	}
	if r.Row[1] != "A" {
		t.Errorf("type cell = %q", r.Row[1])
	}
	if r.Row[2] != "300s" {
		t.Errorf("ttl cell = %q, want 300s", r.Row[2])
	}
	// Both addresses: a set with one missing value is a set that is wrong, and
	// showing only the first hides exactly that.
	if r.Row[4] != "34.120.0.10 34.120.0.11" {
		t.Errorf("data cell = %q, want both addresses", r.Row[4])
	}
}

func TestApexRecordKeepsItsName(t *testing.T) {
	// Trimming the suffix off the zone apex leaves an empty string, which reads
	// as a missing value rather than as "the zone itself".
	r := recordSetResource(testProject(), testDNSZone(), &dns.ResourceRecordSet{
		Name: "example.com.", Type: "SOA", Ttl: 21600,
	})
	if r.Name != "example.com." {
		t.Errorf("apex Name = %q, want it left whole", r.Name)
	}
}

func TestRoutingPolicyRecordsSayWhereTheValuesWent(t *testing.T) {
	// A routing policy record carries no rrdatas — the values live inside the
	// policy, and a weighted or geo split does not fit in one cell.
	r := recordSetResource(testProject(), testDNSZone(), &dns.ResourceRecordSet{
		Name:          "www.example.com.",
		Type:          "A",
		Ttl:           60,
		RoutingPolicy: &dns.RRSetRoutingPolicy{Wrr: &dns.RRSetRoutingPolicyWrrPolicy{}},
	})

	if r.Row[3] != "weighted" {
		t.Errorf("routing cell = %q, want weighted", r.Row[3])
	}
	if r.Row[4] != "(routing policy)" {
		t.Errorf("data cell = %q, want it to say where the values are", r.Row[4])
	}
}

func TestRoutingSummary(t *testing.T) {
	tests := []struct {
		name string
		rp   *dns.RRSetRoutingPolicy
		want string
	}{
		// The common case, and the one that needs no attention: the record
		// answers the same way for everyone.
		{"none", nil, "-"},
		{"geo", &dns.RRSetRoutingPolicy{Geo: &dns.RRSetRoutingPolicyGeoPolicy{}}, "geo"},
		{"weighted", &dns.RRSetRoutingPolicy{Wrr: &dns.RRSetRoutingPolicyWrrPolicy{}}, "weighted"},
		{"failover", &dns.RRSetRoutingPolicy{PrimaryBackup: &dns.RRSetRoutingPolicyPrimaryBackupPolicy{}}, "failover"},
		// A policy shape this build does not know about is still a policy, and
		// saying so beats reporting no steering at all.
		{"unknown shape", &dns.RRSetRoutingPolicy{}, "policy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := routingSummary(tt.rp); got != tt.want {
				t.Errorf("routingSummary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTTLSummary(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		// TTLs under an hour are discussed in seconds; a day read off five
		// digits is work nobody should do at a glance.
		{300, "300s"},
		{3599, "3599s"},
		{3600, "1h"},
		{86400, "1d"},
		{0, "-"},
	}
	for _, tt := range tests {
		if got := ttlSummary(tt.in); got != tt.want {
			t.Errorf("ttlSummary(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRecordSetsGroupByName(t *testing.T) {
	// A name's A and AAAA are read together. Sorting on name alone would leave
	// that to chance, and sortResources would sort on the shared zone first.
	resources := []Resource{
		recordSetResource(testProject(), testDNSZone(), &dns.ResourceRecordSet{Name: "www.example.com.", Type: "TXT"}),
		recordSetResource(testProject(), testDNSZone(), &dns.ResourceRecordSet{Name: "api.example.com.", Type: "AAAA"}),
		recordSetResource(testProject(), testDNSZone(), &dns.ResourceRecordSet{Name: "www.example.com.", Type: "A"}),
		recordSetResource(testProject(), testDNSZone(), &dns.ResourceRecordSet{Name: "api.example.com.", Type: "A"}),
	}
	sortRecordSets(resources)

	want := []struct{ name, kind string }{
		{"api", "A"}, {"api", "AAAA"}, {"www", "A"}, {"www", "TXT"},
	}
	for i, w := range want {
		if resources[i].Name != w.name || resources[i].Row[1] != w.kind {
			t.Errorf("row %d = %s/%s, want %s/%s",
				i, resources[i].Name, resources[i].Row[1], w.name, w.kind)
		}
	}
}

func TestRecordDrillDownRejectsAParentThatIsNotAZone(t *testing.T) {
	// The drill reads Raw, and a row from another kind reaching here would
	// otherwise panic on the type assertion.
	_, err := (DNSRecordLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-a-zone", Raw: testInstance()}, nil)
	if err == nil {
		t.Error("a non-zone parent was accepted")
	}
}

func TestRecordSetsAreNotSSHOrAirflowTargets(t *testing.T) {
	r := recordSetResource(testProject(), testDNSZone(), testRecordSet())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a record set is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a record set has an Airflow URI")
	}
}
