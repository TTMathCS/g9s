package gcp

import (
	"strings"
	"testing"

	"github.com/TTMathCS/g9s/internal/tfstate"
)

func indexOf(t *testing.T, doc string) *tfstate.Index {
	t.Helper()
	idx, err := tfstate.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return idx
}

// The safety property the whole overlay rests on. A kind g9s has no Terraform
// type for must never read as unmanaged — somebody acting on that reads "not
// in Terraform" and deletes something the state does manage.
func TestAnUnmappedKindIsUnknownRatherThanUnmanaged(t *testing.T) {
	idx := indexOf(t, `{"version":4,"resources":[]}`)

	if _, mapped := TerraformTypesFor("objects"); mapped {
		t.Skip("storage objects gained a mapping; pick another unmapped kind")
	}
	if got := ManagedBy(idx, "objects", "anything"); got != ManagedUnknown {
		t.Errorf("an unmapped kind reports %v, want unknown", got)
	}
	if got := ManagedUnknown.String(); got == ManagedNo.String() {
		t.Error("unknown and unmanaged render the same, so a reader cannot tell them apart")
	}
}

func TestAManagedResourceReadsAsManaged(t *testing.T) {
	idx := indexOf(t, `{"version":4,"resources":[
	  {"mode":"managed","type":"google_compute_instance","name":"api",
	   "instances":[{"attributes":{"name":"api-01"}}]}]}`)

	if got := ManagedBy(idx, "vm", "api-01"); got != ManagedYes {
		t.Errorf("ManagedBy = %v, want managed", got)
	}
	if got := ManagedBy(idx, "vm", "api-02"); got != ManagedNo {
		t.Errorf("a name the state does not carry reports %v, want unmanaged", got)
	}
}

// Several Terraform types back one kind — a global and a regional flavour of
// the same resource. Matching only the first would report half an estate as
// unmanaged.
func TestBothFlavoursOfAKindMatch(t *testing.T) {
	idx := indexOf(t, `{"version":4,"resources":[
	  {"mode":"managed","type":"google_compute_global_address","name":"g",
	   "instances":[{"attributes":{"name":"lb-ip"}}]},
	  {"mode":"managed","type":"google_compute_address","name":"r",
	   "instances":[{"attributes":{"name":"nat-ip"}}]}]}`)

	for _, name := range []string{"lb-ip", "nat-ip"} {
		if got := ManagedBy(idx, "addresses", name); got != ManagedYes {
			t.Errorf("%s reports %v, want managed", name, got)
		}
	}
}

// A nil index is what the overlay holds before the state has loaded, and what
// it holds when reading it failed. Neither may claim anything is managed.
func TestNoStateNeverClaimsSomethingIsManaged(t *testing.T) {
	if got := ManagedBy(nil, "vm", "api-01"); got != ManagedNo {
		t.Errorf("with no state loaded a mapped kind reports %v, want unmanaged", got)
	}
	if got := ManagedBy(nil, "objects", "whatever"); got != ManagedUnknown {
		t.Errorf("with no state loaded an unmapped kind reports %v, want unknown", got)
	}
}

// Every kind the overlay claims to understand must be a kind that exists. A
// mapping for a kind id that was renamed is a mapping that silently never
// fires, and the table then reports a whole estate as unmanaged.
func TestEveryMappedKindExists(t *testing.T) {
	real := map[string]bool{}
	for _, l := range Listers() {
		real[l.Kind().ID] = true
	}
	for _, c := range Children() {
		real[c.Kind().ID] = true
	}

	for _, id := range TerraformKinds() {
		if !real[id] {
			t.Errorf("terraformTypes maps %q, which is not a kind g9s has", id)
		}
	}
}

// Every mapped type has to look like a Terraform GCP resource type. A typo
// here is invisible: the lookup simply never matches, and the table reports
// everything as unmanaged with no sign anything is wrong.
func TestEveryMappedTypeLooksLikeAGoogleProviderType(t *testing.T) {
	for _, id := range TerraformKinds() {
		types, _ := TerraformTypesFor(id)
		if len(types) == 0 {
			t.Errorf("kind %q is mapped to no types at all", id)
		}
		for _, tfType := range types {
			if !strings.HasPrefix(tfType, "google_") {
				t.Errorf("kind %q maps to %q, which is not a google provider type", id, tfType)
			}
			if strings.ToLower(tfType) != tfType {
				t.Errorf("terraform type %q is not lowercase", tfType)
			}
		}
	}
}

// A type appearing under two kinds would make one of them wrong: the same
// state entry cannot manage both a VM and a disk.
func TestNoTerraformTypeIsClaimedByTwoKinds(t *testing.T) {
	owner := map[string]string{}
	for _, id := range TerraformKinds() {
		types, _ := TerraformTypesFor(id)
		for _, tfType := range types {
			if prev, seen := owner[tfType]; seen {
				t.Errorf("%q is mapped by both %q and %q", tfType, prev, id)
			}
			owner[tfType] = id
		}
	}
}
