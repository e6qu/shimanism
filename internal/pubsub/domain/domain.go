// Package domain holds shimanism's neutral pub/sub interface and
// types. The interface is the lingua franca between three frontend
// protocols (AWS SNS+SQS, GCP Pub/Sub fanout, Azure Service Bus
// topics) and four backends (the three clouds + NATS core/JetStream
// as the K8s peer); each frontend translates its wire types into
// this domain, each backend translates this domain into its
// cloud's native API.
//
// **Pub/Sub vs Queue.** Phase 3's queue domain has one resource
// type (Queue) and point-to-point delivery: one message → one
// consumer. Phase 4's pubsub domain has two resource types (Topic
// and Subscription) and fanout delivery: one message → every
// subscription. Receive is per-Subscription, not per-Topic.
//
// The shim is stateless ([AGENTS.md § The shim is stateless](../../../AGENTS.md#the-shim-is-stateless)):
// no receipt-handle index lives in the shim. Receipt handles are
// opaque tokens whose native form (AWS ReceiptHandle from the
// backing SQS, GCP AckId, Azure LockToken + MessageId composite,
// NATS JetStream reply_subject) lives in the backend; the shim
// passes them through unchanged or in a per-backend round-trip-safe
// encoding.
//
// See services/pubsub/OPERATIONS.md for the intersection-set
// rationale and per-cloud mapping.
package domain

import (
	"context"
	"time"
)

// Topic describes a topic's metadata.
type Topic struct {
	Name      string
	CreatedAt time.Time
}

// Subscription describes one subscription attached to a topic.
type Subscription struct {
	Name  string
	Topic string
	// AckDeadlineSeconds is the lock duration applied to received
	// messages on this subscription. 0 = backend default. Capped at
	// 600s by the domain (GCP's max) so behaviour is uniform.
	AckDeadlineSeconds int
	// Durable indicates whether the subscription survives
	// subscriber disconnects. NATS core fanout is non-durable; the
	// NATS backend toggles to JetStream consumers when Durable=true.
	// AWS / GCP / Azure subscriptions are always durable; the field
	// is silently ignored on those backends.
	Durable   bool
	CreatedAt time.Time
}

// Message is a single message delivered to a subscription.
type Message struct {
	// MessageID is the producer-side message identifier (assigned by
	// the backend at Publish time).
	MessageID string
	// Body is the message payload bytes.
	Body []byte
	// Attributes is the per-message metadata (largest common
	// denominator: map[string]string).
	Attributes map[string]string
	// ReceiptHandle is the opaque token the consumer presents to
	// Ack / ChangeVisibility.
	ReceiptHandle string
	// ReceivedAt is when the shim returned this message.
	ReceivedAt time.Time
	// DeliveryCount is approximate (≥ 1 on first receive).
	DeliveryCount int
}

// CreateTopicOptions controls CreateTopic.
type CreateTopicOptions struct {
	// Attributes are reserved for forward-compat (KMS, FIFO, etc. —
	// all currently out of intersection). Empty for now.
	Attributes map[string]string
}

// CreateSubscriptionOptions controls CreateSubscription.
type CreateSubscriptionOptions struct {
	AckDeadlineSeconds int
	Durable            bool
}

// PublishOptions controls Publish.
type PublishOptions struct {
	Body       []byte
	Attributes map[string]string
}

// PublishResult is the Publish response.
type PublishResult struct {
	MessageID string
}

// ReceiveOptions controls Receive.
type ReceiveOptions struct {
	// MaxMessages is the requested batch size cap. Capped at 10 by
	// the domain so behaviour is uniform.
	MaxMessages int
	// AckDeadline overrides the subscription-default lock duration
	// for the returned batch. Seconds; 0 = use subscription default.
	// Capped at 600s by the domain.
	AckDeadline int
	// WaitTime is the long-poll budget in seconds. Capped at 20s by
	// the domain.
	WaitTime int
}

// ListTopicsOptions controls ListTopics pagination + filtering.
type ListTopicsOptions struct {
	Prefix     string
	MaxResults int
	NextToken  string
}

// ListTopicsResult is the ListTopics response.
type ListTopicsResult struct {
	Topics    []Topic
	NextToken string
}

// ListSubscriptionsOptions controls ListSubscriptions.
type ListSubscriptionsOptions struct {
	// Topic, if set, filters to subscriptions on that topic.
	Topic      string
	MaxResults int
	NextToken  string
}

// ListSubscriptionsResult is the ListSubscriptions response.
type ListSubscriptionsResult struct {
	Subscriptions []Subscription
	NextToken     string
}

// Pubsub is the interface every pubsub backend implements.
// Implementations must be safe for concurrent use across goroutines.
type Pubsub interface {
	// CreateTopic creates a new topic. Returns TopicAlreadyExists if
	// a topic with that name exists.
	CreateTopic(ctx context.Context, name string, opt CreateTopicOptions) (Topic, error)

	// DeleteTopic removes a topic and all its subscriptions. Returns
	// NoSuchTopic if the topic doesn't exist.
	DeleteTopic(ctx context.Context, name string) error

	// ListTopics lists topics, optionally filtered by name prefix.
	ListTopics(ctx context.Context, opt ListTopicsOptions) (ListTopicsResult, error)

	// HeadTopic returns a topic's metadata. Returns NoSuchTopic if
	// it doesn't exist.
	HeadTopic(ctx context.Context, name string) (Topic, error)

	// CreateSubscription attaches a new subscription to an existing
	// topic. Returns NoSuchTopic if the topic doesn't exist;
	// SubscriptionAlreadyExists if a subscription with that name
	// already exists.
	CreateSubscription(ctx context.Context, topic, sub string, opt CreateSubscriptionOptions) (Subscription, error)

	// DeleteSubscription removes a subscription. Returns
	// NoSuchSubscription if it doesn't exist.
	DeleteSubscription(ctx context.Context, sub string) error

	// ListSubscriptions lists subscriptions, optionally filtered by
	// topic name.
	ListSubscriptions(ctx context.Context, opt ListSubscriptionsOptions) (ListSubscriptionsResult, error)

	// HeadSubscription returns a subscription's metadata. Returns
	// NoSuchSubscription if it doesn't exist.
	HeadSubscription(ctx context.Context, sub string) (Subscription, error)

	// Publish broadcasts a single message to every subscription on
	// the topic. Returns NoSuchTopic if the topic doesn't exist.
	Publish(ctx context.Context, topic string, opt PublishOptions) (PublishResult, error)

	// Receive pulls up to opt.MaxMessages from a subscription's
	// delivery queue, blocking up to opt.WaitTime seconds. Returns
	// NoSuchSubscription if the subscription doesn't exist.
	Receive(ctx context.Context, sub string, opt ReceiveOptions) ([]Message, error)

	// Ack removes the message identified by the receipt handle from
	// the subscription's delivery queue.
	Ack(ctx context.Context, sub string, receiptHandle string) error

	// ChangeVisibility extends or shortens the lock duration on a
	// received message. visibilityTimeout is seconds; 0 returns the
	// message to the subscription immediately.
	ChangeVisibility(ctx context.Context, sub string, receiptHandle string, visibilityTimeout int) error
}
