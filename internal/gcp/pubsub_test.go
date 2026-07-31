package gcp

import (
	"testing"

	monitoring "google.golang.org/api/monitoring/v3"
	pubsub "google.golang.org/api/pubsub/v1"
)

func TestTopicResourceShape(t *testing.T) {
	r := topicResource(testProject(), testPubSubTopic())

	if r.Name != "orders-events" {
		t.Errorf("Name = %q, want the last path segment", r.Name)
	}
	// Topics carry no state field until something is wrong with an ingestion
	// source, and a blank cell is not an answer.
	if r.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE", r.Status)
	}
	if r.Row[2] != "7d" {
		t.Errorf("retention cell = %q, want 7d", r.Row[2])
	}
	if r.Row[3] != "google-managed" {
		t.Errorf("encryption cell = %q", r.Row[3])
	}
}

func TestTopicReportsCustomerManagedEncryption(t *testing.T) {
	// Whether a key is set is the compliance answer; the full key path is far
	// too long for a column, so the key name stands in for it.
	r := topicResource(testProject(), &pubsub.Topic{
		Name:       "projects/sandbox-123/topics/t",
		KmsKeyName: "projects/p/locations/us/keyRings/r/cryptoKeys/pubsub-key",
	})
	if r.Row[3] != "pubsub-key" {
		t.Errorf("encryption cell = %q, want the key name", r.Row[3])
	}
}

func TestSubscriptionResourceShape(t *testing.T) {
	backlog := map[string]int64{"orders-events-etl": 41}
	r := subscriptionResource(testProject(), testPubSubSubscription(), backlog)

	if r.Name != "orders-events-etl" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Row[1] != "orders-events" {
		t.Errorf("topic cell = %q, want the short topic name", r.Row[1])
	}
	if r.Row[2] != "PULL" {
		t.Errorf("type cell = %q, want PULL", r.Row[2])
	}
	// The number the table exists for.
	if r.Row[3] != "41" {
		t.Errorf("backlog cell = %q, want 41", r.Row[3])
	}
	if r.Row[4] != "60s" {
		t.Errorf("ack cell = %q, want 60s", r.Row[4])
	}
}

func TestSubscriptionBacklogAbsentIsNotZero(t *testing.T) {
	// No sample is a different answer from an empty backlog, and showing 0
	// where the metric was unavailable is the one genuinely misleading value:
	// it says "nothing is stuck" when nothing is known.
	r := subscriptionResource(testProject(), testPubSubSubscription(), nil)
	if r.Row[3] != "-" {
		t.Errorf("backlog cell = %q, want a dash when there is no sample", r.Row[3])
	}

	zero := subscriptionResource(testProject(), testPubSubSubscription(),
		map[string]int64{"orders-events-etl": 0})
	if zero.Row[3] != "0" {
		t.Errorf("a real zero backlog = %q, want 0", zero.Row[3])
	}
}

func TestSubscriptionDeliveryNamesTheDestination(t *testing.T) {
	// What "stuck" means depends on this: a pull subscription waits for a
	// consumer, a push one is only as healthy as the endpoint it posts to.
	tests := []struct {
		name string
		sub  *pubsub.Subscription
		want string
	}{
		{"pull is the default", &pubsub.Subscription{}, "PULL"},
		{"push", &pubsub.Subscription{PushConfig: &pubsub.PushConfig{PushEndpoint: "https://x/y"}}, "PUSH"},
		// An empty PushConfig comes back on ordinary pull subscriptions.
		{"empty push config is still pull", &pubsub.Subscription{PushConfig: &pubsub.PushConfig{}}, "PULL"},
		{"bigquery", &pubsub.Subscription{BigqueryConfig: &pubsub.BigQueryConfig{Table: "p:d.t"}}, "BIGQUERY"},
		{"cloud storage", &pubsub.Subscription{CloudStorageConfig: &pubsub.CloudStorageConfig{Bucket: "b"}}, "STORAGE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subscriptionDelivery(tt.sub); got != tt.want {
				t.Errorf("subscriptionDelivery = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSubscriptionDetachedOutranksState(t *testing.T) {
	// A detached subscription exists, retains nothing and delivers nothing.
	// Nothing else in the row says that.
	r := subscriptionResource(testProject(), &pubsub.Subscription{
		Name:     "projects/sandbox-123/subscriptions/s",
		State:    "ACTIVE",
		Detached: true,
	}, nil)
	if r.Status != "DETACHED" {
		t.Errorf("Status = %q, want DETACHED", r.Status)
	}
}

func TestLatestInt64TakesTheFirstUsablePoint(t *testing.T) {
	// Points come back newest first, so the first one carrying a value is the
	// current backlog.
	n := int64(7)
	series := &monitoring.TimeSeries{Points: []*monitoring.Point{
		{Value: &monitoring.TypedValue{}},
		{Value: &monitoring.TypedValue{Int64Value: &n}},
	}}
	if got, ok := latestInt64(series); !ok || got != 7 {
		t.Errorf("latestInt64 = (%d, %v), want (7, true)", got, ok)
	}

	if _, ok := latestInt64(&monitoring.TimeSeries{}); ok {
		t.Error("a series with no points reported a value")
	}
}

func TestRetentionSummary(t *testing.T) {
	tests := []struct{ in, want string }{
		{"604800s", "7d"},
		{"3600s", "1h"},
		{"600s", "10m"},
		{"", "-"},
		// Anything the API sends that is not a duration is passed through
		// rather than swallowed.
		{"forever", "forever"},
	}
	for _, tt := range tests {
		if got := retentionSummary(tt.in); got != tt.want {
			t.Errorf("retentionSummary(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPubSubResourcesAreNotSSHOrAirflowTargets(t *testing.T) {
	for _, r := range []Resource{
		topicResource(testProject(), testPubSubTopic()),
		subscriptionResource(testProject(), testPubSubSubscription(), nil),
	} {
		if _, _, ok := SSHTarget(r); ok {
			t.Errorf("%s is an ssh target", r.Name)
		}
		if _, ok := AirflowURI(r); ok {
			t.Errorf("%s has an Airflow URI", r.Name)
		}
	}
}
