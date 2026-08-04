package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestLimitsDefaultToTheOldConstants is the upgrade guarantee. These numbers
// used to be compiled into each lister; a config that says nothing about limits
// has to behave exactly as it did before the block existed.
func TestLimitsDefaultToTheOldConstants(t *testing.T) {
	c := &Config{}

	tests := []struct {
		name string
		got  int
		want int
	}{
		{"bigquery jobs", c.LimitBigQueryJobs(), 500},
		{"dataflow jobs", c.LimitDataflowJobs(), 500},
		{"dataproc jobs per region", c.LimitDataprocJobsPerRegion(), 200},
		{"cluster jobs", c.LimitClusterJobs(), 200},
		{"bigquery tables", c.LimitBigQueryTables(), 1000},
		{"dns record sets", c.LimitDNSRecordSets(), 1000},
		{"backend groups", c.LimitBackendGroups(), 40},
		{"service account key lookups", c.LimitServiceAccountKeyLookups(), 200},
		{"kms key rings", c.LimitKMSKeyRings(), 100},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s default = %d, want %d", tt.name, tt.got, tt.want)
		}
	}
}

// TestNilConfigYieldsDefaults is not hypothetical. Drill-downs that need no
// configuration are called with a nil *Config, and reading a field off one
// panics — which would take down a refresh rather than a cell.
func TestNilConfigYieldsDefaults(t *testing.T) {
	var c *Config

	if got := c.LimitDNSRecordSets(); got != DefaultDNSRecordSets {
		t.Errorf("nil config DNS limit = %d, want the default", got)
	}
	if got := c.LimitBigQueryTables(); got != DefaultBigQueryTables {
		t.Errorf("nil config table limit = %d, want the default", got)
	}
	if got := c.LimitBackendGroups(); got != DefaultBackendGroups {
		t.Errorf("nil config backend group limit = %d, want the default", got)
	}
	if got := c.LimitKMSKeyRings(); got != DefaultKMSKeyRings {
		t.Errorf("nil config key ring limit = %d, want the default", got)
	}
}

func TestConfiguredLimitWins(t *testing.T) {
	c := &Config{Defaults: Defaults{Limits: Limits{
		BigQueryJobs:  5000,
		DNSRecordSets: 50,
	}}}

	if got := c.LimitBigQueryJobs(); got != 5000 {
		t.Errorf("configured limit = %d, want 5000", got)
	}
	// Lowering one is as valid as raising it — a slow link is a reason to see
	// fewer rows, not more.
	if got := c.LimitDNSRecordSets(); got != 50 {
		t.Errorf("configured limit = %d, want 50", got)
	}
	// Untouched limits keep their defaults rather than collapsing to zero,
	// which is the bug a single shared "max rows" setting would have.
	if got := c.LimitBigQueryTables(); got != DefaultBigQueryTables {
		t.Errorf("unset limit = %d, want the default", got)
	}
}

// TestNegativeMeansUncapped: the escape hatch has to be asked for. Zero is what
// an absent field decodes to, so zero cannot mean "no limit" — that would turn
// every unset field into an unbounded fetch.
func TestNegativeMeansUncapped(t *testing.T) {
	c := &Config{Defaults: Defaults{Limits: Limits{DNSRecordSets: -1}}}

	got := c.LimitDNSRecordSets()
	if got <= DefaultDNSRecordSets {
		t.Fatalf("uncapped limit = %d, want something no listing reaches", got)
	}
	// The callers all compare `len(rows) >= limit`, so "uncapped" has to be a
	// number a real listing never reaches rather than a special case each
	// lister remembers to handle.
	if got != maxInt {
		t.Errorf("uncapped limit = %d, want maxInt", got)
	}
}

func TestZeroIsNotUncapped(t *testing.T) {
	// The failure this guards against is silent and expensive: a config with
	// `limits:` present but a key mistyped decodes to zero, and if zero meant
	// unlimited the next refresh would try to pull every record in the zone.
	c := &Config{Defaults: Defaults{Limits: Limits{DNSRecordSets: 0}}}
	if got := c.LimitDNSRecordSets(); got != DefaultDNSRecordSets {
		t.Errorf("zero limit = %d, want the default rather than uncapped", got)
	}
}

func TestLimitsRoundTripThroughYAML(t *testing.T) {
	// The field names are the user interface, so they are worth pinning: a
	// renamed key does not error, it silently reverts to the default.
	raw := `
defaults:
  limits:
    bigquery_jobs: 2000
    dataflow_jobs: 1500
    dataproc_jobs_per_region: 300
    cluster_jobs: 400
    bigquery_tables: 5000
    dns_record_sets: -1
    backend_groups: 80
    service_account_key_lookups: 500
    kms_key_rings: 250
`
	var c Config
	if err := yaml.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("decoding limits: %v", err)
	}

	tests := []struct {
		name string
		got  int
		want int
	}{
		{"bigquery_jobs", c.LimitBigQueryJobs(), 2000},
		{"dataflow_jobs", c.LimitDataflowJobs(), 1500},
		{"dataproc_jobs_per_region", c.LimitDataprocJobsPerRegion(), 300},
		{"cluster_jobs", c.LimitClusterJobs(), 400},
		{"bigquery_tables", c.LimitBigQueryTables(), 5000},
		{"dns_record_sets", c.LimitDNSRecordSets(), maxInt},
		{"backend_groups", c.LimitBackendGroups(), 80},
		{"service_account_key_lookups", c.LimitServiceAccountKeyLookups(), 500},
		{"kms_key_rings", c.LimitKMSKeyRings(), 250},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %d, want %d — is the yaml tag still spelled that way?", tt.name, tt.got, tt.want)
		}
	}
}

// TestEveryLimitFieldHasAnAccessor catches the half-done addition: a field
// added to Limits with no accessor is a setting the user can write and nothing
// reads, which fails silently and looks exactly like the cap not working.
func TestEveryLimitFieldHasAnAccessor(t *testing.T) {
	// Reflection over the struct rather than a hand-kept list, since a
	// hand-kept list is the thing that goes stale.
	fields := limitFieldNames()
	if len(fields) != 10 {
		t.Fatalf("Limits has %d fields (%v) — add the accessor and update this count", len(fields), fields)
	}

	c := &Config{}
	accessors := map[string]int{
		"BigQueryJobs":             c.LimitBigQueryJobs(),
		"DataflowJobs":             c.LimitDataflowJobs(),
		"DataprocJobsPerRegion":    c.LimitDataprocJobsPerRegion(),
		"ClusterJobs":              c.LimitClusterJobs(),
		"BigQueryTables":           c.LimitBigQueryTables(),
		"DNSRecordSets":            c.LimitDNSRecordSets(),
		"BackendGroups":            c.LimitBackendGroups(),
		"ServiceAccountKeyLookups": c.LimitServiceAccountKeyLookups(),
		"KMSKeyRings":              c.LimitKMSKeyRings(),
		"CloudBuilds":              c.LimitCloudBuilds(),
	}
	for _, name := range fields {
		if _, ok := accessors[name]; !ok {
			t.Errorf("Limits.%s has no Limit%s() accessor — the setting would be silently ignored", name, name)
		}
	}
}

// TestLimitYAMLTagsAreSnakeCase keeps the config file readable in one style.
func TestLimitYAMLTagsAreSnakeCase(t *testing.T) {
	for _, tag := range limitYAMLTags() {
		if tag != strings.ToLower(tag) {
			t.Errorf("limits yaml tag %q is not lowercase", tag)
		}
		if strings.Contains(tag, "-") {
			t.Errorf("limits yaml tag %q uses a hyphen; the rest of the file uses underscores", tag)
		}
	}
}

// limitFieldNames lists the Go field names on Limits.
func limitFieldNames() []string {
	t := reflect.TypeOf(Limits{})
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		names = append(names, t.Field(i).Name)
	}
	return names
}

// limitYAMLTags lists the yaml keys Limits decodes from.
func limitYAMLTags() []string {
	t := reflect.TypeOf(Limits{})
	tags := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tags = append(tags, t.Field(i).Tag.Get("yaml"))
	}
	return tags
}

// TestLimitsSurviveTheStrictLoader is the one that actually matters for a user.
// The loader rejects unknown keys on purpose, so a limits block that decodes
// fine under a plain unmarshal could still be refused by the real path — and
// the error a user would see is "unknown field", on a key the docs told them
// to write.
func TestLimitsSurviveTheStrictLoader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "g9s.yaml")
	os.WriteFile(path, []byte(`
defaults:
  regions: [us-central1]
  limits:
    bigquery_jobs: 3000
    dns_record_sets: -1
projects:
  - name: p
    project_id: p-1
`), 0o600)

	c, err := Load(path)
	if err != nil {
		t.Fatalf("strict loader rejected limits: %v", err)
	}
	if got := c.LimitBigQueryJobs(); got != 3000 {
		t.Errorf("bigquery_jobs = %d, want 3000", got)
	}
	if got := c.LimitDNSRecordSets(); got != maxInt {
		t.Errorf("dns_record_sets = %d, want uncapped", got)
	}
}

// TestEveryLimitIsDocumented catches the drift that keeps happening: a setting
// that exists in the struct and nowhere a user would look, or docs naming a key
// the loader would reject. Both fail silently — the first as a knob nobody
// discovers, the second as "unknown field" on a key the README told them to
// write.
//
// Reading the docs from a test is unusual. It earns its place because the
// mismatch is invisible from either side alone: the code compiles, the docs
// render, and only a user hits the gap.
func TestEveryLimitIsDocumented(t *testing.T) {
	docs := map[string]string{}
	for _, name := range []string{"README.md", "ROADMAP.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		docs[name] = string(raw)
	}

	tags := limitYAMLTags()
	if len(tags) == 0 {
		t.Fatal("no yaml tags found on Limits — the reflection is not working")
	}
	for _, tag := range tags {
		for name, body := range docs {
			if !strings.Contains(body, tag) {
				t.Errorf("%s does not mention limits key %q — a setting nobody can find", name, tag)
			}
		}
	}

	// The other direction: a documented key that is not a real field would be
	// rejected by the strict loader, so the docs would be telling users to
	// write something that fails to load.
	real := map[string]bool{}
	for _, tag := range tags {
		real[tag] = true
	}
	for name, body := range docs {
		for _, m := range documentedLimitKeys(body) {
			if !real[m] {
				t.Errorf("%s documents limits.%s, which is not a field — the loader would reject it", name, m)
			}
		}
	}
}

// documentedLimitKeys finds `limits.<key>` references in prose.
func documentedLimitKeys(body string) []string {
	var keys []string
	for _, m := range limitKeyPattern.FindAllStringSubmatch(body, -1) {
		keys = append(keys, m[1])
	}
	return keys
}

var limitKeyPattern = regexp.MustCompile(`limits\.([a-z_]+)`)
