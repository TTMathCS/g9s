package gcp

import (
	"testing"

	redis "google.golang.org/api/redis/v1"
)

func TestRedisResourceShape(t *testing.T) {
	r := redisResource(testProject(), testRedisInstance())

	if r.Name != "session-cache" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Location != "us-central1" {
		t.Errorf("Location = %q, want it parsed out of the resource name", r.Location)
	}
	if r.Row[2] != "standard HA" {
		t.Errorf("tier cell = %q", r.Row[2])
	}
	if r.Row[3] != "16GB" {
		t.Errorf("size cell = %q", r.Row[3])
	}
	if r.Row[4] != "7_0" {
		t.Errorf("version cell = %q, want the REDIS_ prefix stripped", r.Row[4])
	}
	if r.Status != "READY" {
		t.Errorf("Status = %q", r.Status)
	}
}

// TestBasicTierHasNoReplica is the durability finding. A BASIC instance is
// working exactly as designed and loses everything if its single node restarts,
// and it reports the same READY as a replicated one.
func TestBasicTierHasNoReplica(t *testing.T) {
	i := testRedisInstance()
	i.Tier = "BASIC"

	r := redisResource(testProject(), i)
	if r.Status != "NO_REPLICA" {
		t.Errorf("Status = %q, want the missing replica to outrank READY", r.Status)
	}
	if r.Row[2] != "basic" {
		t.Errorf("tier cell = %q", r.Row[2])
	}
	// The state column still tells the truth about the instance itself.
	if r.Row[6] != "READY" {
		t.Errorf("state cell = %q, want the instance's own state kept", r.Row[6])
	}
}

func TestAuthDisabledIsFlagged(t *testing.T) {
	// Memorystore is only reachable inside the VPC, which is why people leave
	// AUTH off — and why an instance with it off trusts every workload there.
	i := testRedisInstance()
	i.AuthEnabled = false

	r := redisResource(testProject(), i)
	if r.Status != "NO_AUTH" {
		t.Errorf("Status = %q, want NO_AUTH", r.Status)
	}
	if r.Row[5] != "off" {
		t.Errorf("auth cell = %q", r.Row[5])
	}
}

// TestReplicaFindingOutranksAuth: a basic-tier instance with AUTH off has two
// problems, and losing the data is the larger one.
func TestReplicaFindingOutranksAuth(t *testing.T) {
	i := testRedisInstance()
	i.Tier = "BASIC"
	i.AuthEnabled = false

	if got := redisResource(testProject(), i).Status; got != "NO_REPLICA" {
		t.Errorf("Status = %q, want NO_REPLICA", got)
	}
}

// TestInFlightStateOutranksBothFindings: an instance mid-create has no replica
// yet either, and reporting that as a durability finding is noise.
func TestInFlightStateOutranksBothFindings(t *testing.T) {
	i := testRedisInstance()
	i.Tier = "BASIC"
	i.State = "CREATING"

	if got := redisResource(testProject(), i).Status; got != "CREATING" {
		t.Errorf("Status = %q, want the lifecycle state to win", got)
	}
}

func TestRedisWithoutAState(t *testing.T) {
	for _, state := range []string{"", "STATE_UNSPECIFIED"} {
		i := testRedisInstance()
		i.State = state
		if got := redisResource(testProject(), i).Row[6]; got != "UNKNOWN" {
			t.Errorf("state cell for %q = %q, want UNKNOWN", state, got)
		}
	}
}

func TestInstanceRegionComesFromTheName(t *testing.T) {
	// The wildcard listing returns no location field of its own, so the name is
	// the only place the region appears.
	tests := map[string]string{
		"projects/p/locations/europe-west4/instances/i": "europe-west4",
		"projects/p/locations/asia-east1/instances/i":   "asia-east1",
		"instances/i":          "-",
		"":                     "-",
		"projects/p/locations": "-",
	}
	for name, want := range tests {
		if got := instanceRegion(name); got != want {
			t.Errorf("instanceRegion(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestRedisTierAndVersionFallbacks(t *testing.T) {
	if got := redisTier(&redis.Instance{}); got != "-" {
		t.Errorf("redisTier with no tier = %q, want a dash", got)
	}
	if got := redisVersion(&redis.Instance{}); got != "-" {
		t.Errorf("redisVersion with no version = %q, want a dash", got)
	}
	// An unknown tier is a new one, and showing it beats hiding it.
	if got := redisTier(&redis.Instance{Tier: "SOMETHING_NEW"}); got != "something_new" {
		t.Errorf("redisTier = %q", got)
	}
}

func TestRedisInstancesAreNotSSHOrAirflowTargets(t *testing.T) {
	// A Redis instance carries a host and a region, which is most of what an
	// ssh target looks like from the outside.
	r := redisResource(testProject(), testRedisInstance())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a Redis instance is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a Redis instance has an Airflow URI")
	}
}
