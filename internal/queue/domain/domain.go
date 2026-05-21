// Package domain holds shimanism's neutral queue interface and
// types. The interface is the lingua franca between three frontend
// protocols (AWS SQS, GCP Pub/Sub pull, Azure Service Bus queue)
// and four backends (the three clouds + NATS JetStream as K8s
// peer); each frontend translates its wire types into this domain,
// each backend translates this domain into its cloud's native API.
//
// The shim is stateless ([AGENTS.md § The shim is stateless](../../../AGENTS.md#the-shim-is-stateless)):
// no receipt-handle index lives in the shim. Receipt handles are
// opaque tokens whose native form (AWS ReceiptHandle, GCP AckId,
// Azure LockToken + MessageId composite, NATS reply_subject) lives
// in the backend; the shim passes them through unchanged or in a
// per-backend round-trip-safe encoding.
//
// See services/queue/OPERATIONS.md for the intersection-set
// rationale and per-cloud mapping.
package domain

import (
	"context"
	"time"
)

// Queue describes a queue's metadata (no messages).
type Queue struct {
	Name       string
	Attributes QueueAttributes
}

// QueueAttributes carries the common-denominator queue settings
// that map cleanly across all four backends.
type QueueAttributes struct {
	// VisibilityTimeoutSeconds is the default lock duration for a
	// received message. Defaults differ per cloud (AWS 30s, GCP 10s,
	// Azure 60s, NATS configurable); the domain accepts 0 = "use the
	// backend's natural default".
	VisibilityTimeoutSeconds int
	// MessageRetentionSeconds is how long messages stay in the queue
	// before expiry. AWS / Azure honour this; GCP Pub/Sub uses a
	// subscription-side retention separate from message lifetime;
	// NATS uses stream-level retention. 0 = backend default.
	MessageRetentionSeconds int
	// MaxMessageSizeBytes caps individual messages. The cross-cloud
	// floor is AWS SQS's 256 KiB. Backends may accept smaller caps.
	MaxMessageSizeBytes int
	// DelaySeconds is an optional pre-visible delay applied to every
	// message sent to this queue. AWS / Azure honour this natively;
	// GCP + NATS treat as 0.
	DelaySeconds int
	// ApproximateMessageCount is read-only — populated by HeadQueue.
	ApproximateMessageCount int
	// CreatedAt is read-only.
	CreatedAt time.Time
}

// Message is a single retrieved message.
type Message struct {
	// MessageID is the producer-side message identifier (assigned by
	// the backend at SendMessage time).
	MessageID string
	// Body is the message payload bytes.
	Body []byte
	// Attributes is the per-message metadata.
	Attributes map[string]string
	// ReceiptHandle is the opaque token the consumer presents to
	// DeleteMessage / ChangeVisibility. Its native form is
	// backend-specific (AWS ReceiptHandle, GCP AckId, Azure LockToken
	// composite, NATS reply_subject); the shim treats it as opaque.
	ReceiptHandle string
	// ReceivedAt is when the shim returned this message.
	ReceivedAt time.Time
	// DeliveryCount is an approximate count of times this message has
	// been delivered (≥ 1 on first receive). Backends without a
	// native counter return 1.
	DeliveryCount int
}

// SendMessageOptions controls SendMessage.
type SendMessageOptions struct {
	Body         []byte
	Attributes   map[string]string
	DelaySeconds int // per-message delay; AWS / Azure honour; GCP + NATS ignore
}

// SendMessageResult is the SendMessage response.
type SendMessageResult struct {
	MessageID string
}

// ReceiveMessagesOptions controls ReceiveMessages.
type ReceiveMessagesOptions struct {
	// MaxMessages is the requested batch size cap. Capped at 10 (AWS
	// SQS limit) by the domain so behaviour is uniform.
	MaxMessages int
	// VisibilityTimeout overrides the queue-default lock duration for
	// the returned batch. Seconds; 0 = use queue default. Capped at
	// 600s (GCP's max) by the domain so behaviour is uniform.
	VisibilityTimeout int
	// WaitTime is the long-poll budget in seconds. Capped at 20s (AWS
	// SQS limit). Backends without native per-receive wait busy-poll
	// up to the budget.
	WaitTime int
}

// CreateQueueOptions controls CreateQueue.
type CreateQueueOptions struct {
	Attributes QueueAttributes
}

// ListQueuesOptions controls ListQueues pagination + filtering.
type ListQueuesOptions struct {
	Prefix     string
	MaxResults int
	NextToken  string
}

// ListQueuesResult is the ListQueues response.
type ListQueuesResult struct {
	Queues    []Queue
	NextToken string
}

// Queues is the interface every queue backend implements.
// Implementations must be safe for concurrent use across goroutines.
type Queues interface {
	// CreateQueue creates a new queue with the given name +
	// attributes. Returns QueueAlreadyExists if a queue with that
	// name exists.
	CreateQueue(ctx context.Context, name string, opt CreateQueueOptions) (Queue, error)

	// DeleteQueue removes a queue and all its messages. Returns
	// NoSuchQueue if the queue doesn't exist.
	DeleteQueue(ctx context.Context, name string) error

	// HeadQueue returns the queue's attributes (including the
	// approximate message count). Returns NoSuchQueue if the queue
	// doesn't exist.
	HeadQueue(ctx context.Context, name string) (Queue, error)

	// SetQueueAttributes patches the queue's attributes. Used by the
	// AWS SQS provider's post-create reconciliation path
	// (CreateQueue → SetQueueAttributes). Zero-valued fields in the
	// supplied Attributes block leave the corresponding backend
	// attribute unchanged (consistent with the AWS API's
	// merge-rather-than-replace semantics). Returns NoSuchQueue if
	// the queue doesn't exist.
	SetQueueAttributes(ctx context.Context, name string, attrs QueueAttributes) error

	// ListQueueTags returns the queue's tag set. Returns an empty
	// map (not nil) for a queue with no tags configured.
	ListQueueTags(ctx context.Context, name string) (map[string]string, error)

	// TagQueue merges the supplied tags onto the queue (additive;
	// existing tags not in the map are preserved). AWS-shape
	// semantics. Returns NoSuchQueue if the queue doesn't exist.
	TagQueue(ctx context.Context, name string, tags map[string]string) error

	// UntagQueue removes the named tags from the queue. Keys that
	// aren't currently present are a silent no-op (matches AWS).
	// Returns NoSuchQueue if the queue doesn't exist.
	UntagQueue(ctx context.Context, name string, keys []string) error

	// ListQueues lists queues, optionally filtered by name prefix.
	ListQueues(ctx context.Context, opt ListQueuesOptions) (ListQueuesResult, error)

	// SendMessage publishes a single message to the queue.
	SendMessage(ctx context.Context, queueName string, opt SendMessageOptions) (SendMessageResult, error)

	// ReceiveMessages pulls up to opt.MaxMessages messages, blocking
	// up to opt.WaitTime seconds for new messages to arrive. Each
	// returned message carries an opaque ReceiptHandle the consumer
	// must present to DeleteMessage / ChangeVisibility.
	ReceiveMessages(ctx context.Context, queueName string, opt ReceiveMessagesOptions) ([]Message, error)

	// DeleteMessage acks (and removes from the queue) the message
	// identified by the receipt handle.
	DeleteMessage(ctx context.Context, queueName string, receiptHandle string) error

	// ChangeVisibility extends or shortens the lock duration on a
	// received message. visibilityTimeout is seconds; 0 returns the
	// message to the queue immediately.
	ChangeVisibility(ctx context.Context, queueName string, receiptHandle string, visibilityTimeout int) error
}
