package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	monitoring "google.golang.org/api/monitoring/v3"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// AlertPolicyLister lists Cloud Monitoring alert policies.
//
// Two findings drive the columns, and neither is visible in the console's
// default list:
//
//   - A **disabled** policy. Someone silences an alert during an incident and
//     nothing ever reminds them to turn it back on. It keeps existing, keeps
//     appearing in inventories, and never fires again.
//   - A policy with **no notification channels**. It is enabled, it evaluates,
//     it opens incidents — and nobody is told. This is worse than a disabled
//     policy because everything looks correct.
//
// Both are the same shape of failure: monitoring that exists on paper and does
// nothing in practice. The status column is what surfaces them, so a table of
// green rows means something.
//
// The firing state itself is deliberately not here. Open incidents are not
// available from the alertPolicies API, and inventing a column that is empty
// for every row would suggest nothing is firing rather than that g9s did not
// look.
type AlertPolicyLister struct{}

func (AlertPolicyLister) Kind() Kind {
	return Kind{
		ID:    "alerts",
		Title: "Alert Policies",
		Columns: []Column{
			{Title: "NAME", Width: 6},
			{Title: "CONDITIONS", Width: 2},
			{Title: "NOTIFIES", Width: 2},
			{Title: "COMBINER", Width: 2},
			{Title: "STATUS", Width: 3},
		},
	}
}

func (AlertPolicyLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := monitoring.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("monitoring client: %w", err)
	}

	var result Result
	err = svc.Projects.AlertPolicies.List("projects/"+p.ProjectID).
		Pages(ctx, func(page *monitoring.ListAlertPoliciesResponse) error {
			for _, a := range page.AlertPolicies {
				if a != nil {
					result.Resources = append(result.Resources, alertPolicyResource(p, a))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	sortResources(result.Resources)
	return result, nil
}

func alertPolicyResource(p config.Project, a *monitoring.AlertPolicy) Resource {
	name := a.DisplayName
	if name == "" {
		name = lastSegment(a.Name)
	}

	channels := len(a.NotificationChannels)

	return Resource{
		Name:     name,
		Location: "global",
		Status:   alertPolicyStatus(a, channels),
		Row: []string{
			name,
			fmt.Sprint(len(a.Conditions)),
			fmt.Sprint(channels),
			alertCombiner(a.Combiner),
			alertPolicyStatus(a, channels),
		},
		Raw: a,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/monitoring/alerting/policies/%s?project=%s",
			url.PathEscape(lastSegment(a.Name)), url.QueryEscape(p.ProjectID)),
	}
}

// alertPolicyStatus ranks the ways a policy can be doing nothing.
//
// Disabled first because it is unambiguous. NO_NOTIFICATIONS second because it
// is the one that looks healthy: enabled, evaluating, opening incidents nobody
// receives. A policy with no conditions cannot fire at all and is the rarest
// but most complete failure, so it outranks both.
func alertPolicyStatus(a *monitoring.AlertPolicy, channels int) string {
	if len(a.Conditions) == 0 {
		return "NO_CONDITIONS"
	}
	// A plain bool, so an absent field reads as disabled. The API always
	// sends it, and treating a missing value as enabled would be the wrong way
	// to be wrong: claiming an alert is live is worse than claiming it is off.
	if !a.Enabled {
		return "DISABLED"
	}
	if channels == 0 {
		return "NO_NOTIFICATIONS"
	}
	return "ENABLED"
}

// alertCombiner shortens the enum to what distinguishes the policies.
//
// It decides whether every condition has to be true or any one of them, which
// on a multi-condition policy is the difference between an alert that fires
// constantly and one that never does.
func alertCombiner(combiner string) string {
	switch combiner {
	case "AND", "AND_WITH_MATCHING_RESOURCE":
		return strings.ToLower(strings.TrimSuffix(combiner, "_WITH_MATCHING_RESOURCE"))
	case "OR":
		return "or"
	default:
		return "-"
	}
}
