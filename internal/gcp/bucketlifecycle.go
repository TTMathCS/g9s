package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

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
	attrs, ok := parent.Raw.(*storage.BucketAttrs)
	if !ok {
		return Result{}, fmt.Errorf("no bucket data for %s", parent.Name)
	}

	var result Result
	for i, rule := range attrs.Lifecycle.Rules {
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

func lifecycleRuleResource(p config.Project, attrs *storage.BucketAttrs, index int, rule storage.LifecycleRule) Resource {
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
func lifecycleAction(a storage.LifecycleAction) string {
	switch {
	case a.Type == "":
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
func lifecycleActionStatus(a storage.LifecycleAction) string {
	if strings.EqualFold(a.Type, "Delete") {
		return "DELETE"
	}
	return "ACTIVE"
}

// lifecycleAge renders the age condition, which is on almost every rule and is
// the number people actually compare between them.
func lifecycleAge(c storage.LifecycleCondition) string {
	if c.AgeInDays > 0 {
		return fmt.Sprintf("%dd", c.AgeInDays)
	}
	return "-"
}

// lifecycleVersions renders the noncurrent-version conditions.
//
// Separate from the general conditions column because on a versioned bucket
// these are what actually control the bill: objects nobody can see are still
// paid for, and a rule that never reaches them is the usual reason a bucket
// costs more than its visible contents.
func lifecycleVersions(c storage.LifecycleCondition) string {
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
func lifecycleScope(c storage.LifecycleCondition) string {
	var parts []string

	switch c.Liveness {
	case storage.Live:
		parts = append(parts, "live only")
	case storage.Archived:
		parts = append(parts, "noncurrent only")
	}

	if len(c.MatchesStorageClasses) > 0 {
		parts = append(parts, strings.Join(c.MatchesStorageClasses, "/"))
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
func lifecycleConditions(c storage.LifecycleCondition) string {
	var parts []string
	if !c.CreatedBefore.IsZero() {
		parts = append(parts, "created before "+shortDate(c.CreatedBefore))
	}
	if !c.CustomTimeBefore.IsZero() {
		parts = append(parts, "custom time before "+shortDate(c.CustomTimeBefore))
	}
	if c.DaysSinceCustomTime > 0 {
		parts = append(parts, fmt.Sprintf("%dd since custom time", c.DaysSinceCustomTime))
	}
	if !c.NoncurrentTimeBefore.IsZero() {
		parts = append(parts, "noncurrent before "+shortDate(c.NoncurrentTimeBefore))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// shortDate renders a lifecycle date condition, which has no time component.
func shortDate(t time.Time) string {
	return t.Format("2006-01-02")
}
