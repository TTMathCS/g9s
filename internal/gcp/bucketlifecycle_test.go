package gcp

import (
	"testing"

	storagev1 "google.golang.org/api/storage/v1"
)

func TestLifecycleListingCostsNoAPICall(t *testing.T) {
	// The buckets listing already carried the rules, so opening them asks
	// nothing new. A nil client and nil options here is the assertion.
	parent := bucketResource(testProject(), testBucket())

	result, err := (BucketLifecycleLister{}).List(t.Context(), nil, testProject(), parent, nil)
	if err != nil {
		t.Fatalf("lifecycle drill-down: %v", err)
	}
	if len(result.Resources) != 2 {
		t.Fatalf("listed %d rules, want 2", len(result.Resources))
	}
	// Rule order, not sorted: GCS evaluates every matching rule, and the order
	// they were written in is how the set is edited and reasoned about.
	if result.Resources[0].Name != "rule-1" || result.Resources[1].Name != "rule-2" {
		t.Errorf("rules came back as %q, %q — want them in the order they are written",
			result.Resources[0].Name, result.Resources[1].Name)
	}
}

func TestLifecycleRuleResourceShape(t *testing.T) {
	r := lifecycleRuleResource(testProject(), testBucket(), 0, testBucket().Lifecycle.Rule[0])

	// "SetStorageClass" alone does not say what to.
	if r.Row[0] != "SetStorageClass → NEARLINE" {
		t.Errorf("action cell = %q", r.Row[0])
	}
	if r.Row[1] != "30d" {
		t.Errorf("age cell = %q", r.Row[1])
	}
	if r.Row[3] != "live only" {
		t.Errorf("scope cell = %q", r.Row[3])
	}
	if r.Status != "ACTIVE" {
		t.Errorf("Status = %q, want a non-destructive rule to read as ordinary", r.Status)
	}
}

func TestDeleteRuleIsTheOneThatColours(t *testing.T) {
	// Delete is the only irreversible action, and the rule people go looking
	// for when data has gone missing. Everything else changes a bill.
	r := lifecycleRuleResource(testProject(), testBucket(), 1, testBucket().Lifecycle.Rule[1])
	if r.Status != "DELETE" {
		t.Errorf("Status = %q, want DELETE", r.Status)
	}
	if r.Row[0] != "Delete" {
		t.Errorf("action cell = %q", r.Row[0])
	}
	// The version conditions get their own column: on a versioned bucket they
	// are what actually control the bill.
	if r.Row[2] != "keep 3" {
		t.Errorf("versions cell = %q", r.Row[2])
	}
	if r.Row[3] != "exports/*" {
		t.Errorf("scope cell = %q", r.Row[3])
	}
}

func TestABucketWithNoRulesSaysSo(t *testing.T) {
	// A bucket with no rules keeps everything at its current storage class
	// forever, which is a cost decision whether or not anyone made it.
	bare := testBucket()
	bare.Lifecycle = nil
	parent := bucketResource(testProject(), bare)

	result, err := (BucketLifecycleLister{}).List(t.Context(), nil, testProject(), parent, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != 0 {
		t.Fatalf("got %d rules, want none", len(result.Resources))
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("got %d warnings, want one explaining the empty table: %v",
			len(result.Warnings), result.Warnings)
	}
}

func TestLifecycleScope(t *testing.T) {
	tests := []struct {
		name string
		in   *storagev1.BucketLifecycleRuleCondition
		want string
	}{
		// No restriction at all is the case worth being explicit about: the
		// rule reaches every object in the bucket.
		{"unrestricted", &storagev1.BucketLifecycleRuleCondition{Age: ptr(int64(30))}, "all objects"},
		// The field most often misread: a rule restricted to noncurrent objects
		// looks like it deletes everything and touches nothing you can see.
		{"noncurrent only", &storagev1.BucketLifecycleRuleCondition{IsLive: ptr(false)}, "noncurrent only"},
		{"live only", &storagev1.BucketLifecycleRuleCondition{IsLive: ptr(true)}, "live only"},
		{"storage classes", &storagev1.BucketLifecycleRuleCondition{
			MatchesStorageClass: []string{"STANDARD", "NEARLINE"},
		}, "STANDARD/NEARLINE"},
		{"prefix and suffix", &storagev1.BucketLifecycleRuleCondition{
			MatchesPrefix: []string{"logs/"}, MatchesSuffix: []string{".tmp"},
		}, "logs/* *.tmp"},
		// A rule with no condition block at all reaches everything, which is
		// the most consequential thing this column can say.
		{"no condition", nil, "all objects"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lifecycleScope(tt.in); got != tt.want {
				t.Errorf("lifecycleScope = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLifecycleVersionsCoversBothNoncurrentConditions(t *testing.T) {
	both := lifecycleVersions(&storagev1.BucketLifecycleRuleCondition{
		NumNewerVersions: 2, DaysSinceNoncurrentTime: 14,
	})
	if both != "keep 2, 14d noncurrent" {
		t.Errorf("versions cell = %q", both)
	}
	if got := lifecycleVersions(&storagev1.BucketLifecycleRuleCondition{}); got != "-" {
		t.Errorf("empty versions cell = %q, want a dash", got)
	}
}

func TestLifecycleDateConditions(t *testing.T) {
	got := lifecycleConditions(&storagev1.BucketLifecycleRuleCondition{
		CreatedBefore:       "2026-03-14",
		DaysSinceCustomTime: 90,
	})
	if got != "created before 2026-03-14, 90d since custom time" {
		t.Errorf("conditions cell = %q", got)
	}
	if got := lifecycleConditions(&storagev1.BucketLifecycleRuleCondition{}); got != "-" {
		t.Errorf("empty conditions cell = %q, want a dash", got)
	}
}

func TestLifecycleDrillDownRejectsAParentThatIsNotABucket(t *testing.T) {
	_, err := (BucketLifecycleLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-a-bucket", Raw: testInstance()}, nil)
	if err == nil {
		t.Error("a non-bucket parent was accepted")
	}
}

func TestLifecycleRulesAreNotSSHOrAirflowTargets(t *testing.T) {
	r := lifecycleRuleResource(testProject(), testBucket(), 0, testBucket().Lifecycle.Rule[0])
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a lifecycle rule is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a lifecycle rule has an Airflow URI")
	}
}
