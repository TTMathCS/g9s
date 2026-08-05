package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/api/option"
	storagev1 "google.golang.org/api/storage/v1"

	"github.com/TTMathCS/g9s/internal/config"
)

// BucketLifecycleLister is the lifecycle rules on one bucket.
//
// Costs no API call: the buckets listing already carries them.
//
// This is the answer to two questions a bucket row cannot touch. "Why did my
// data disappear" is usually a Delete rule nobody remembered; "why is nothing
// being archived" is usually a SetStorageClass rule that was never added, or
// one whose condition never matches. Both are invisible until you read the
// rules, and the Console buries them two clicks down.
type BucketLifecycleLister struct{}

func (BucketLifecycleLister) ParentKind() string { return "gcs" }

func (BucketLifecycleLister) Kind() Kind {
	return Kind{
		ID:    "lifecycle",
		Title: "Lifecycle",
		Columns: []Column{
			{Title: "ACTION", Width: 3},
			{Title: "AGE", Width: 1},
			{Title: "VERSIONS", Width: 2},
			{Title: "SCOPE", Width: 5},
			{Title: "CONDITIONS", Width: 5},
		},
	}
}

func (BucketLifecycleLister) List(_ context.Context, _ *config.Config, p config.Project, parent Resource, _ []option.ClientOption) (Result, error) {
	attrs, ok := parent.Raw.(*storagev1.Bucket)
	if !ok {
		return Result{}, fmt.Errorf("no bucket data for %s", parent.Name)
	}

	// Read rather than filled in: attrs is the cached bucket the table is still
	// showing, so assigning an empty Lifecycle here to avoid a nil check would
	// be this listing writing into another one's data.
	var rules []*storagev1.BucketLifecycleRule
	if attrs.Lifecycle != nil {
		rules = attrs.Lifecycle.Rule
	}

	var result Result
	for i, rule := range rules {
		if rule == nil {
			continue
		}
		result.Resources = append(result.Resources, lifecycleRuleResource(p, attrs, i, rule))
	}

	if len(result.Resources) == 0 {
		// Worth saying out loud: a bucket with no rules keeps everything at its
		// current storage class forever, which is a cost decision whether or
		// not anyone made it deliberately.
		result.Warnings = append(result.Warnings,
			narrowedWarning("no lifecycle rules — nothing in this bucket is ever deleted or downgraded automatically"))
	}

	// Rule order, not sorted: GCS evaluates every matching rule, and the order
	// they were written in is how the set is reasoned about and edited.
	return result, nil
}

func lifecycleRuleResource(p config.Project, attrs *storagev1.Bucket, index int, rule *storagev1.BucketLifecycleRule) Resource {
	// Rules have no id or name of their own, so the position is the only handle
	// there is — and it is the one the JSON representation uses too.
	name := fmt.Sprintf("rule-%d", index+1)

	return Resource{
		Name:     name,
		Location: attrs.Name,
		Status:   lifecycleActionStatus(rule.Action),
		Row: []string{
			lifecycleAction(rule.Action),
			lifecycleAge(rule.Condition),
			lifecycleVersions(rule.Condition),
			lifecycleScope(rule.Condition),
			lifecycleConditions(rule.Condition),
		},
		Raw: rule,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/storage/browser/%s;tab=lifecycle?project=%s",
			url.PathEscape(attrs.Name), url.QueryEscape(p.ProjectID)),
	}
}

// lifecycleAction says what the rule does, with the destination class folded in
// — "SetStorageClass" alone does not say what to.
//
// Every accessor here takes a pointer that the REST types allow to be nil,
// where the wrapper handed back a value. A rule with no action is not
// something GCS should return, but a nil dereference here would take the
// process and the terminal with it, and a dash costs nothing.
func lifecycleAction(a *storagev1.BucketLifecycleRuleAction) string {
	switch {
	case a == nil, a.Type == "":
		return "-"
	case a.StorageClass != "":
		return a.Type + " → " + a.StorageClass
	default:
		return a.Type
	}
}

// lifecycleActionStatus colours the row by how much the rule can cost you.
//
// Delete is the only irreversible one, and it is the rule people are looking
// for when data has gone missing. Everything else changes a bill, not a fact.
func lifecycleActionStatus(a *storagev1.BucketLifecycleRuleAction) string {
	if a != nil && strings.EqualFold(a.Type, "Delete") {
		return "DELETE"
	}
	return "ACTIVE"
}

// lifecycleAge renders the age condition, which is on almost every rule and is
// the number people actually compare between them.
//
// A pointer on the REST type, and the distinction is real: `age: 0` is a valid
// rule that matches every object the moment it is written, while an absent age
// is no age condition at all. The wrapper's int64 could not tell them apart and
// rendered both as no condition.
func lifecycleAge(c *storagev1.BucketLifecycleRuleCondition) string {
	if c == nil || c.Age == nil {
		return "-"
	}
	return fmt.Sprintf("%dd", *c.Age)
}

// lifecycleVersions renders the noncurrent-version conditions.
//
// Separate from the general conditions column because on a versioned bucket
// these are what actually control the bill: objects nobody can see are still
// paid for, and a rule that never reaches them is the usual reason a bucket
// costs more than its visible contents.
func lifecycleVersions(c *storagev1.BucketLifecycleRuleCondition) string {
	if c == nil {
		return "-"
	}
	var parts []string
	if c.NumNewerVersions > 0 {
		parts = append(parts, fmt.Sprintf("keep %d", c.NumNewerVersions))
	}
	if c.DaysSinceNoncurrentTime > 0 {
		parts = append(parts, fmt.Sprintf("%dd noncurrent", c.DaysSinceNoncurrentTime))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// lifecycleScope says which objects the rule can reach.
//
// Liveness is the field most often misread: a rule restricted to noncurrent
// objects looks like it deletes everything and touches nothing you can see.
func lifecycleScope(c *storagev1.BucketLifecycleRuleCondition) string {
	if c == nil {
		// No condition at all means the rule matches everything, which is the
		// most consequential thing this column can say and must not be a dash.
		return "all objects"
	}
	var parts []string

	// isLive is a *bool because unset means "both", which is neither of the
	// other two answers and is the default.
	if c.IsLive != nil {
		if *c.IsLive {
			parts = append(parts, "live only")
		} else {
			parts = append(parts, "noncurrent only")
		}
	}

	if len(c.MatchesStorageClass) > 0 {
		parts = append(parts, strings.Join(c.MatchesStorageClass, "/"))
	}
	for _, prefix := range c.MatchesPrefix {
		parts = append(parts, prefix+"*")
	}
	for _, suffix := range c.MatchesSuffix {
		parts = append(parts, "*"+suffix)
	}

	if len(parts) == 0 {
		// No restriction at all, which is the case worth being explicit about:
		// the rule applies to every object in the bucket.
		return "all objects"
	}
	return strings.Join(parts, " ")
}

// lifecycleConditions is everything left: the date and custom-time conditions,
// which are rarer and would crowd the columns that matter more.
//
// The dates arrive as "2013-01-15" strings rather than as time.Time, which is
// what the column wanted anyway — a lifecycle date has no time component and
// the wrapper's time.Time was formatted straight back down to this.
func lifecycleConditions(c *storagev1.BucketLifecycleRuleCondition) string {
	if c == nil {
		return "-"
	}
	var parts []string
	if c.CreatedBefore != "" {
		parts = append(parts, "created before "+c.CreatedBefore)
	}
	if c.CustomTimeBefore != "" {
		parts = append(parts, "custom time before "+c.CustomTimeBefore)
	}
	if c.DaysSinceCustomTime > 0 {
		parts = append(parts, fmt.Sprintf("%dd since custom time", c.DaysSinceCustomTime))
	}
	if c.NoncurrentTimeBefore != "" {
		parts = append(parts, "noncurrent before "+c.NoncurrentTimeBefore)
	}
	// Size conditions, which the wrapper's type did not carry at all. A rule
	// that only deletes objects above a size is a rule whose scope column looks
	// unrestricted and is not — exactly the misreading this table exists to
	// prevent.
	if c.SizeAboveBytes > 0 {
		parts = append(parts, "larger than "+humanBytes(c.SizeAboveBytes))
	}
	if c.SizeBelowBytes > 0 {
		parts = append(parts, "smaller than "+humanBytes(c.SizeBelowBytes))
	}
	if c.MatchesPattern != "" {
		parts = append(parts, "matching "+clip(c.MatchesPattern, 30))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}
