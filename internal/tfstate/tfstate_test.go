package tfstate

import (
	"fmt"
	"strings"
	"testing"
)

const stateWithSecrets = `{
  "version": 4,
  "terraform_version": "1.6.6",
  "resources": [
    {
      "mode": "managed",
      "type": "google_compute_instance",
      "name": "api",
      "instances": [
        {"attributes": {
          "name": "api-01",
          "zone": "us-central1-a",
          "metadata_startup_script": "export DB_PASSWORD=hunter2-do-not-leak"
        }}
      ]
    },
    {
      "mode": "managed",
      "type": "google_sql_database_instance",
      "name": "main",
      "instances": [
        {"attributes": {
          "name": "main-db",
          "root_password": "correct-horse-battery-staple",
          "server_ca_cert": [{"cert": "-----BEGIN CERTIFICATE-----"}]
        }}
      ]
    },
    {
      "mode": "data",
      "type": "google_compute_instance",
      "name": "looked-up",
      "instances": [{"attributes": {"name": "not-managed-by-us"}}]
    }
  ]
}`

func parse(t *testing.T, doc string) *Index {
	t.Helper()
	idx, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return idx
}

// The reason this package exists rather than a general state parser. A state
// file carries database passwords, private keys and every other value a
// provider round-trips; the only defence that survives future changes is that
// nothing but the identity ever enters memory in the first place.
func TestNothingFromTheAttributesSurvivesParsing(t *testing.T) {
	idx := parse(t, stateWithSecrets)

	// Everything the index can produce, rendered as text, must not contain any
	// of it.
	var everything strings.Builder
	fmt.Fprint(&everything, idx.Types())
	for _, tfType := range idx.Types() {
		for name := range idx.byType[tfType] {
			everything.WriteString(name)
		}
	}

	for _, secret := range []string{
		"hunter2-do-not-leak",
		"correct-horse-battery-staple",
		"BEGIN CERTIFICATE",
	} {
		if strings.Contains(everything.String(), secret) {
			t.Errorf("a value from the state attributes survived parsing: %q", secret)
		}
	}
}

// The name that matters is the resource's name in GCP, not the Terraform block
// label — matching on the label would mark nothing, since it is whatever the
// author decided to call it.
func TestTheIndexedNameIsTheCloudNameNotTheBlockLabel(t *testing.T) {
	idx := parse(t, stateWithSecrets)

	if !idx.Has("google_compute_instance", "api-01") {
		t.Error("the instance is not indexed under its GCP name")
	}
	if idx.Has("google_compute_instance", "api") {
		t.Error("the instance is indexed under its Terraform block label")
	}
}

// A data source is something Terraform reads, not something it owns. Counting
// one as managed would report a resource as tracked by the very config that
// merely looks at it.
func TestDataSourcesAreNotManaged(t *testing.T) {
	idx := parse(t, stateWithSecrets)
	if idx.Has("google_compute_instance", "not-managed-by-us") {
		t.Error("a data source was counted as managed")
	}
}

// Terraform 0.12 omits mode for managed resources.
func TestAMissingModeCountsAsManaged(t *testing.T) {
	idx := parse(t, `{"version":4,"resources":[
	  {"type":"google_storage_bucket","name":"b","instances":[{"attributes":{"name":"my-bucket"}}]}
	]}`)
	if !idx.Has("google_storage_bucket", "my-bucket") {
		t.Error("a resource with no mode was skipped")
	}
}

// An estate whose state is split across several files is one estate.
func TestMergeCombinesSeveralStateFiles(t *testing.T) {
	a := parse(t, `{"version":4,"resources":[
	  {"mode":"managed","type":"google_compute_instance","name":"a","instances":[{"attributes":{"name":"vm-a"}}]}]}`)
	b := parse(t, `{"version":4,"resources":[
	  {"mode":"managed","type":"google_compute_instance","name":"b","instances":[{"attributes":{"name":"vm-b"}}]}]}`)

	a.Merge(b)
	if !a.Has("google_compute_instance", "vm-a") || !a.Has("google_compute_instance", "vm-b") {
		t.Error("merging two state files lost one of them")
	}
	if a.Count() != 2 {
		t.Errorf("Count = %d after merging two single-resource states", a.Count())
	}
}

// One g9s kind can be backed by several Terraform types — a global and a
// regional flavour, or a type the provider renamed.
func TestHasAnyMatchesAcrossSeveralTypes(t *testing.T) {
	idx := parse(t, `{"version":4,"resources":[
	  {"mode":"managed","type":"google_compute_global_address","name":"g","instances":[{"attributes":{"name":"lb-ip"}}]}]}`)

	types := []string{"google_compute_address", "google_compute_global_address"}
	if !idx.HasAny(types, "lb-ip") {
		t.Error("HasAny missed the type that does manage it")
	}
	if idx.HasAny(types, "other-ip") {
		t.Error("HasAny matched a name nothing manages")
	}
}

// Old state must say so. Reporting it as empty would read as "nothing is
// managed", which is the most misleading answer this feature could give.
func TestPreTerraform012StateSaysSoRatherThanLookingEmpty(t *testing.T) {
	_, err := Parse(strings.NewReader(`{"version":3,"modules":[{"path":["root"],"resources":{}}]}`))
	if err == nil {
		t.Fatal("0.11 state parsed as an empty index")
	}
	if !strings.Contains(err.Error(), "0.11") {
		t.Errorf("error does not say what the file is: %v", err)
	}
}

// A resource with no name attribute cannot be matched, but it was still
// managed — so it counts, or a state full of them looks like an empty one.
func TestAnUnnamedResourceStillCounts(t *testing.T) {
	idx := parse(t, `{"version":4,"resources":[
	  {"mode":"managed","type":"google_project_iam_member","name":"m","instances":[{"attributes":{}}]}]}`)

	if idx.Empty() {
		t.Error("a state with a managed resource reports as empty")
	}
	if idx.Count() != 1 {
		t.Errorf("Count = %d, want 1", idx.Count())
	}
}

func TestMalformedStateIsAnErrorNotAnEmptyIndex(t *testing.T) {
	if _, err := Parse(strings.NewReader("{not json")); err == nil {
		t.Error("malformed state parsed successfully")
	}
}

// A nil index answers rather than panicking: the overlay asks it questions
// before a state file has been loaded.
func TestANilIndexAnswersEverythingFalse(t *testing.T) {
	var idx *Index
	if idx.Has("google_compute_instance", "a") || idx.HasAny([]string{"x"}, "a") {
		t.Error("a nil index claimed something was managed")
	}
	if !idx.Empty() || idx.Count() != 0 || idx.Types() != nil {
		t.Error("a nil index returned something")
	}
}
