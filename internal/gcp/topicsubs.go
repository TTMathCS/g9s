package gcp

import (
	"context"
	"fmt"
	"net/url"

	"google.golang.org/api/option"
	pubsub "google.golang.org/api/pubsub/v1"

	"github.com/TTMathCS/g9s/internal/config"
)

// TopicSubscriptionLister is the subscriptions attached to one topic.
//
// The subscriptions kind lists them project-wide; this is the other axis, and
// the one "who is actually reading this" is asked on. A topic with no
// subscriptions is a topic publishing into nothing, which is invisible from the
// topics table and obvious here.
//
// Deliberately not topics.subscriptions.list: that returns names only, which
// would mean a Get per subscription to fill in a row. Listing the project's
// subscriptions and filtering costs one call and returns whole objects — the
// same trick the subnets drill-down uses.
type TopicSubscriptionLister struct{}

func (TopicSubscriptionLister) ParentKind() string { return "topics" }

func (TopicSubscriptionLister) Kind() Kind {
	return Kind{
		ID:    "topicsubs",
		Title: "Subscriptions",
		Columns: []Column{
			// No TOPIC column: it is the same on every row here, which is the
			// whole point of being under one.
			{Title: "NAME", Width: 5},
			{Title: "TYPE", Width: 2},
			{Title: "BACKLOG", Width: 2},
			{Title: "ACK", Width: 1},
			{Title: "RETENTION", Width: 2},
			{Title: "STATE", Width: 2},
		},
	}
}

func (TopicSubscriptionLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	topic, ok := parent.Raw.(*pubsub.Topic)
	if !ok {
		return Result{}, fmt.Errorf("no topic data for %s", parent.Name)
	}

	svc, err := pubsub.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("pubsub client: %w", err)
	}

	var subs []*pubsub.Subscription
	err = svc.Projects.Subscriptions.List("projects/"+p.ProjectID).
		Pages(ctx, func(page *pubsub.ListSubscriptionsResponse) error {
			for _, s := range page.Subscriptions {
				if s != nil && s.Topic == topic.Name {
					subs = append(subs, s)
				}
			}
			return nil
		})
	if err != nil {
		return Result{}, err
	}

	// Same best-effort backlog as the project-wide table: a failure costs the
	// column, not the listing.
	backlog, warning := subscriptionBacklog(ctx, p, opts)

	var result Result
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	for _, s := range subs {
		result.Resources = append(result.Resources, topicSubscriptionResource(p, s, backlog))
	}

	if len(result.Resources) == 0 {
		// The finding this listing exists to surface. An empty table with no
		// explanation reads as a failed call rather than as a topic nobody
		// is reading.
		result.Warnings = append(result.Warnings,
			"no subscriptions on this topic — anything published to it is discarded")
	}

	sortResources(result.Resources)
	return result, nil
}

// topicSubscriptionResource is the project-wide row minus the topic cell.
//
// The status, delivery type and retention rules are shared with the top-level
// kind on purpose: the same subscription must not read differently depending on
// which table it is seen from.
func topicSubscriptionResource(p config.Project, s *pubsub.Subscription, backlog map[string]int64) Resource {
	full := subscriptionResource(p, s, backlog)

	// Row is NAME TOPIC TYPE BACKLOG ACK RETENTION STATE; drop the topic.
	full.Row = []string{
		full.Row[0],
		full.Row[2],
		full.Row[3],
		full.Row[4],
		full.Row[5],
		full.Row[6],
	}
	full.Location = lastSegment(s.Topic)
	full.ConsoleURL = fmt.Sprintf(
		"https://console.cloud.google.com/cloudpubsub/subscription/detail/%s?project=%s",
		url.PathEscape(full.Name), url.QueryEscape(p.ProjectID))
	return full
}
