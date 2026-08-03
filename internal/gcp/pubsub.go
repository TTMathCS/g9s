package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	monitoring "google.golang.org/api/monitoring/v3"
	"google.golang.org/api/option"
	pubsub "google.golang.org/api/pubsub/v1"

	"github.com/TTMathCS/g9s/internal/config"
)

// PubSubTopicLister lists Pub/Sub topics.
//
// Global and paginated, through the generated REST client, so no new
// dependency. A topic is a thin thing — the interesting state lives on the
// subscriptions attached to it, which is the other half of this pair.
type PubSubTopicLister struct{}

func (PubSubTopicLister) Kind() Kind {
	return Kind{
		ID:    "topics",
		Title: "Pub/Sub Topics",
		Columns: []Column{
			{Title: "NAME", Width: 6},
			{Title: "STATE", Width: 2},
			{Title: "RETENTION", Width: 2},
			{Title: "ENCRYPTION", Width: 3},
			{Title: "LABELS", Width: 3},
		},
	}
}

func (PubSubTopicLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := pubsub.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("pubsub client: %w", err)
	}

	var result Result
	err = svc.Projects.Topics.List("projects/"+p.ProjectID).Pages(ctx, func(page *pubsub.ListTopicsResponse) error {
		for _, t := range page.Topics {
			result.Resources = append(result.Resources, topicResource(p, t))
		}
		return nil
	})
	if err != nil {
		return result, err
	}

	sortResources(result.Resources)
	return result, nil
}

func topicResource(p config.Project, t *pubsub.Topic) Resource {
	name := lastSegment(t.Name)

	// A topic reports ACTIVE, or INGESTION_RESOURCE_ERROR when an import
	// source it was told to pull from is broken. Absent means the ordinary
	// case, which is worth stating rather than leaving blank.
	state := t.State
	if state == "" {
		state = "ACTIVE"
	}

	// Customer-managed encryption is a compliance answer, and the key name is
	// long; that it is set is the part that fits in a column.
	encryption := "google-managed"
	if t.KmsKeyName != "" {
		encryption = lastSegment(t.KmsKeyName)
	}

	return Resource{
		Name:     name,
		Location: "global",
		Status:   state,
		Row: []string{
			name,
			state,
			retentionSummary(t.MessageRetentionDuration),
			encryption,
			formatLabels(t.Labels),
		},
		Raw: t,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/cloudpubsub/topic/detail/%s?project=%s",
			url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// PubSubSubscriptionLister lists Pub/Sub subscriptions with their backlog.
//
// The backlog is the number people actually open this table for — a
// subscription nobody is draining is the failure that goes unnoticed until a
// bill or an SLA does the noticing. It is not on the subscription resource:
// num_undelivered_messages is a Monitoring metric, so it takes a second call.
//
// One second call, not one per subscription. A single timeSeries.list filtered
// on the metric returns every subscription in the project, and if Monitoring
// is unreachable or not enabled the backlog column degrades to "-" with a
// warning rather than failing the listing that did work.
type PubSubSubscriptionLister struct{}

// backlogWindow is how far back to look for a backlog sample. Pub/Sub writes
// this metric roughly once a minute; a few minutes of slack covers a late
// sample without reporting a number old enough to mislead.
const backlogWindow = 10 * time.Minute

func (PubSubSubscriptionLister) Kind() Kind {
	return Kind{
		ID:    "subs",
		Title: "Pub/Sub Subscriptions",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "TOPIC", Width: 4},
			{Title: "TYPE", Width: 2},
			{Title: "BACKLOG", Width: 2},
			{Title: "ACK", Width: 2},
			{Title: "RETENTION", Width: 2},
			{Title: "STATE", Width: 2},
		},
	}
}

func (PubSubSubscriptionLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := pubsub.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("pubsub client: %w", err)
	}

	var (
		subs   []*pubsub.Subscription
		result Result
	)
	err = svc.Projects.Subscriptions.List("projects/"+p.ProjectID).Pages(ctx, func(page *pubsub.ListSubscriptionsResponse) error {
		subs = append(subs, page.Subscriptions...)
		return nil
	})
	if err != nil {
		return result, err
	}

	// Best effort: a failure here costs the backlog column, not the table.
	backlog, warning, hasWarning := subscriptionBacklog(ctx, p, opts)
	if hasWarning {
		result.Warnings = append(result.Warnings, warning)
	}

	for _, s := range subs {
		result.Resources = append(result.Resources, subscriptionResource(p, s, backlog))
	}
	sortResources(result.Resources)
	return result, nil
}

func subscriptionResource(p config.Project, s *pubsub.Subscription, backlog map[string]int64) Resource {
	name := lastSegment(s.Name)

	// A subscription whose topic was deleted reports the literal string
	// "_deleted-topic_" rather than an empty field, and it is passed straight
	// through: that is the anomaly the column exists to show.
	topic := lastSegment(s.Topic)
	if topic == "" {
		topic = "-"
	}

	state := s.State
	if state == "" {
		state = "ACTIVE"
	}
	// Detached outranks the state: the subscription exists, retains nothing
	// and delivers nothing, which no other field says.
	if s.Detached {
		state = "DETACHED"
	}

	count := "-"
	if n, ok := backlog[name]; ok {
		count = fmt.Sprintf("%d", n)
	}

	return Resource{
		Name:     name,
		Location: "global",
		Status:   state,
		Row: []string{
			name,
			topic,
			subscriptionDelivery(s),
			count,
			fmt.Sprintf("%ds", s.AckDeadlineSeconds),
			retentionSummary(s.MessageRetentionDuration),
			state,
		},
		Raw: s,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/cloudpubsub/subscription/detail/%s?project=%s",
			url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// subscriptionDelivery names where messages go, which decides what "stuck"
// even means: a pull subscription waits for a consumer, a push one is only as
// healthy as the endpoint it posts to.
func subscriptionDelivery(s *pubsub.Subscription) string {
	switch {
	case s.PushConfig != nil && s.PushConfig.PushEndpoint != "":
		return "PUSH"
	case s.BigqueryConfig != nil:
		return "BIGQUERY"
	case s.CloudStorageConfig != nil:
		return "STORAGE"
	default:
		return "PULL"
	}
}

// subscriptionBacklog fetches num_undelivered_messages for every subscription
// in the project, keyed by subscription id.
//
// Returns a warning rather than an error: Monitoring not being enabled is a
// perfectly ordinary state for a project, and it must not cost the table.
func subscriptionBacklog(ctx context.Context, p config.Project, opts []option.ClientOption) (map[string]int64, Warning, bool) {
	svc, err := monitoring.NewService(ctx, opts...)
	if err != nil {
		return nil, scopeWarning("backlog", ReasonUnknown, "unavailable: "+clip(err.Error(), 90)), true
	}

	now := time.Now()
	backlog := map[string]int64{}

	call := svc.Projects.TimeSeries.List("projects/" + p.ProjectID).
		Filter(`metric.type="pubsub.googleapis.com/subscription/num_undelivered_messages"`).
		IntervalStartTime(now.Add(-backlogWindow).Format(time.RFC3339)).
		IntervalEndTime(now.Format(time.RFC3339))

	err = call.Pages(ctx, func(page *monitoring.ListTimeSeriesResponse) error {
		for _, series := range page.TimeSeries {
			if series.Resource == nil {
				continue
			}
			id := series.Resource.Labels["subscription_id"]
			if id == "" {
				continue
			}
			if n, ok := latestInt64(series); ok {
				backlog[id] = n
			}
		}
		return nil
	})
	if err != nil {
		// Suppressed the same way a disabled service is elsewhere: this is
		// an extra, and a project without Monitoring is not misconfigured.
		if w, ok := describeFailure("backlog", err); ok {
			return backlog, w, true
		}
		return backlog, Warning{}, false
	}
	return backlog, Warning{}, false
}

// latestInt64 takes the most recent point of a series. Points come back newest
// first, so the first usable one is the current value.
func latestInt64(series *monitoring.TimeSeries) (int64, bool) {
	for _, point := range series.Points {
		if point.Value != nil && point.Value.Int64Value != nil {
			return *point.Value.Int64Value, true
		}
	}
	return 0, false
}

// retentionSummary renders a protobuf duration string ("604800s") as something
// readable. Pub/Sub reports retention in seconds, and "7d" is the form the
// setting is actually reasoned about in.
func retentionSummary(duration string) string {
	if duration == "" {
		return "-"
	}
	seconds := strings.TrimSuffix(duration, "s")
	d, err := time.ParseDuration(seconds + "s")
	if err != nil {
		return duration
	}
	return shortDuration(d)
}
