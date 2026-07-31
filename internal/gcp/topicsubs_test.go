package gcp

import (
	"testing"

	pubsub "google.golang.org/api/pubsub/v1"
)

func TestTopicSubscriptionDropsTheTopicColumn(t *testing.T) {
	// The topic is the same on every row under a topic, which is the whole
	// point of being under one — so the column that repeats it goes.
	r := topicSubscriptionResource(testProject(), testPubSubSubscription(),
		map[string]int64{"orders-events-etl": 41})

	if r.Name != "orders-events-etl" {
		t.Errorf("Name = %q", r.Name)
	}
	if len(r.Row) != len((TopicSubscriptionLister{}).Kind().Columns) {
		t.Fatalf("row has %d cells, want %d", len(r.Row), len((TopicSubscriptionLister{}).Kind().Columns))
	}
	if r.Row[1] != "PULL" {
		t.Errorf("type cell = %q, want the topic cell dropped and TYPE second", r.Row[1])
	}
	// The number the table exists for, still in place after the reshape.
	if r.Row[2] != "41" {
		t.Errorf("backlog cell = %q, want 41", r.Row[2])
	}
}

func TestTopicSubscriptionReadsTheSameFromEitherTable(t *testing.T) {
	// A subscription must not look different depending on which table it is
	// seen from, so the status and delivery rules are shared with the
	// project-wide kind rather than reimplemented.
	detached := &pubsub.Subscription{
		Name:     "projects/sandbox-123/subscriptions/s",
		Topic:    "projects/sandbox-123/topics/orders-events",
		State:    "ACTIVE",
		Detached: true,
	}

	wide := subscriptionResource(testProject(), detached, nil)
	under := topicSubscriptionResource(testProject(), detached, nil)

	if under.Status != wide.Status {
		t.Errorf("status differs between tables: %q vs %q", under.Status, wide.Status)
	}
	if under.Status != "DETACHED" {
		t.Errorf("Status = %q, want DETACHED", under.Status)
	}
}

func TestTopicSubscriptionLocationIsTheTopic(t *testing.T) {
	// The drill trail names the parent, and the location is what a merged view
	// would show — the topic is the honest answer for both.
	r := topicSubscriptionResource(testProject(), testPubSubSubscription(), nil)
	if r.Location != "orders-events" {
		t.Errorf("Location = %q, want the topic", r.Location)
	}
}

func TestTopicSubscriptionBacklogAbsentIsStillNotZero(t *testing.T) {
	// Same rule as the project-wide table: showing 0 where the metric was
	// unavailable says "nothing is stuck" when nothing is known.
	r := topicSubscriptionResource(testProject(), testPubSubSubscription(), nil)
	if r.Row[2] != "-" {
		t.Errorf("backlog cell = %q, want a dash", r.Row[2])
	}
}

func TestTopicSubscriptionDrillDownRejectsAParentThatIsNotATopic(t *testing.T) {
	_, err := (TopicSubscriptionLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-a-topic", Raw: testInstance()}, nil)
	if err == nil {
		t.Error("a non-topic parent was accepted")
	}
}

func TestTopicSubscriptionsAreNotSSHOrAirflowTargets(t *testing.T) {
	r := topicSubscriptionResource(testProject(), testPubSubSubscription(), nil)
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a subscription is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a subscription has an Airflow URI")
	}
}

func TestBothTopicListingsHangOffTheTopicsKind(t *testing.T) {
	// A topic now offers one drill-down; the assertion that matters is that it
	// is reachable at all, since ChildrenOf keys off the parent's kind id.
	children := ChildrenOf("topics")
	if len(children) != 1 {
		t.Fatalf("ChildrenOf(topics) returned %d listings, want 1", len(children))
	}
	if children[0].Kind().ID != "topicsubs" {
		t.Errorf("child = %q", children[0].Kind().ID)
	}
}
