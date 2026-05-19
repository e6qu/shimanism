// Package nats is the NATS JetStream backend for shimanism's
// queue service, acting as the K8s peer per PLAN.md Phase 3.
// Queues map to JetStream streams + a per-stream pull consumer
// the shim calls "default". Subjects mirror queue names.
//
// Receipt-handle handling. JetStream messages carry a reply
// subject the consumer publishes ack/in-progress/nack messages to.
// The shim uses the reply subject as its opaque receipt handle —
// ack/extend/nack publishes happen via the long-lived NATS
// connection without holding the original *nats.Msg across
// requests. State stays in JetStream. See AGENTS.md § The shim is
// stateless.
package nats

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	natsapi "github.com/nats-io/nats.go"

	"github.com/e6qu/shimanism/internal/queue/domain"
)

// withDeadline ensures the context handed to the NATS library has a
// deadline. The NATS Go SDK rejects deadline-less contexts in
// FlushWithContext / nats.Context with "nats: context requires a
// deadline". Backend ops invoked from tests or matrix loops use
// context.Background(); wrap them with a sensible default.
func withDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, 30*time.Second)
}

// Backend implements domain.Queues via NATS JetStream.
type Backend struct {
	nc *natsapi.Conn
	js natsapi.JetStreamContext
}

// New wraps an already-connected NATS connection. The caller is
// responsible for credential / URL configuration.
func New(nc *natsapi.Conn) (*Backend, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("attach JetStream context: %w", err)
	}
	return &Backend{nc: nc, js: js}, nil
}

var _ domain.Queues = (*Backend)(nil)

// streamName returns the JetStream stream name for a domain queue.
// Stream names must match `[A-Za-z0-9_-]+`; queue names that contain
// other characters get hex-escaped (rare in practice — AWS SQS and
// the other clouds use restrictive name sets too).
func streamName(queue string) string {
	// Replace any character outside [A-Za-z0-9_-] with '_' so the
	// stream name is valid. The shim doesn't try to round-trip
	// arbitrary names; callers using names with weird characters
	// see them collapse to '_'.
	var b strings.Builder
	b.Grow(len(queue))
	for i := 0; i < len(queue); i++ {
		c := queue[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '-':
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// consumerName is the default per-stream pull consumer.
const consumerName = "shim-consumer"

func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, natsapi.ErrStreamNotFound):
		return domain.NoSuchQueue(name)
	case errors.Is(err, natsapi.ErrStreamNameAlreadyInUse):
		return domain.QueueAlreadyExists(name)
	case errors.Is(err, natsapi.ErrConsumerNotFound):
		return domain.NoSuchQueue(name)
	}
	return err
}

func (b *Backend) CreateQueue(ctx context.Context, name string, opt domain.CreateQueueOptions) (domain.Queue, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	stream := streamName(name)
	maxAge := time.Duration(opt.Attributes.MessageRetentionSeconds) * time.Second
	if maxAge == 0 {
		maxAge = 4 * 24 * time.Hour
	}
	_, err := b.js.AddStream(&natsapi.StreamConfig{
		Name:      stream,
		Subjects:  []string{stream},
		Retention: natsapi.WorkQueuePolicy,
		MaxAge:    maxAge,
		Storage:   natsapi.FileStorage,
	}, natsapi.Context(ctx))
	if err != nil {
		return domain.Queue{}, translateErr(err, name)
	}
	ackWait := time.Duration(opt.Attributes.VisibilityTimeoutSeconds) * time.Second
	if ackWait == 0 {
		ackWait = 30 * time.Second
	}
	if _, err := b.js.AddConsumer(stream, &natsapi.ConsumerConfig{
		Durable:    consumerName,
		AckPolicy:  natsapi.AckExplicitPolicy,
		AckWait:    ackWait,
		MaxDeliver: -1,
	}, natsapi.Context(ctx)); err != nil {
		return domain.Queue{}, translateErr(err, name)
	}
	return domain.Queue{Name: name, Attributes: opt.Attributes}, nil
}

func (b *Backend) DeleteQueue(ctx context.Context, name string) error {
	if err := b.js.DeleteStream(streamName(name)); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) HeadQueue(ctx context.Context, name string) (domain.Queue, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	info, err := b.js.StreamInfo(streamName(name), natsapi.Context(ctx))
	if err != nil {
		return domain.Queue{}, translateErr(err, name)
	}
	cinfo, _ := b.js.ConsumerInfo(streamName(name), consumerName, natsapi.Context(ctx))
	attrs := domain.QueueAttributes{
		MessageRetentionSeconds: int(info.Config.MaxAge / time.Second),
		MaxMessageSizeBytes:     int(info.Config.MaxMsgSize),
		ApproximateMessageCount: int(info.State.Msgs),
		CreatedAt:               info.Created,
	}
	if cinfo != nil {
		attrs.VisibilityTimeoutSeconds = int(cinfo.Config.AckWait / time.Second)
	}
	return domain.Queue{Name: name, Attributes: attrs}, nil
}

func (b *Backend) ListQueues(ctx context.Context, opt domain.ListQueuesOptions) (domain.ListQueuesResult, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	names := b.js.StreamNames(natsapi.Context(ctx))
	res := domain.ListQueuesResult{}
	for n := range names {
		if opt.Prefix != "" && !strings.HasPrefix(n, opt.Prefix) {
			continue
		}
		q, err := b.HeadQueue(ctx, n)
		if err != nil {
			continue
		}
		res.Queues = append(res.Queues, q)
		if opt.MaxResults > 0 && len(res.Queues) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) SendMessage(ctx context.Context, queueName string, opt domain.SendMessageOptions) (domain.SendMessageResult, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	stream := streamName(queueName)
	msg := &natsapi.Msg{
		Subject: stream,
		Data:    append([]byte(nil), opt.Body...),
		Header:  natsapi.Header{},
	}
	for k, v := range opt.Attributes {
		msg.Header.Set(k, v)
	}
	// DelaySeconds: NATS JetStream doesn't natively support scheduled
	// delivery; this is out of intersection on this backend.
	_ = opt.DelaySeconds
	ack, err := b.js.PublishMsg(msg, natsapi.Context(ctx))
	if err != nil {
		return domain.SendMessageResult{}, translateErr(err, queueName)
	}
	return domain.SendMessageResult{
		MessageID: strconv.FormatUint(ack.Sequence, 10),
	}, nil
}

func (b *Backend) ReceiveMessages(ctx context.Context, queueName string, opt domain.ReceiveMessagesOptions) ([]domain.Message, error) {
	stream := streamName(queueName)
	sub, err := b.js.PullSubscribe(stream, consumerName, natsapi.Bind(stream, consumerName))
	if err != nil {
		return nil, translateErr(err, queueName)
	}
	defer func() { _ = sub.Unsubscribe() }()
	n := opt.MaxMessages
	if n <= 0 || n > 10 {
		n = 10
	}
	wait := time.Duration(opt.WaitTime) * time.Second
	if wait <= 0 {
		wait = 100 * time.Millisecond
	}
	msgs, err := sub.Fetch(n, natsapi.MaxWait(wait))
	if err != nil && !errors.Is(err, natsapi.ErrTimeout) {
		return nil, translateErr(err, queueName)
	}
	out := make([]domain.Message, 0, len(msgs))
	now := time.Now().UTC()
	for _, m := range msgs {
		md, _ := m.Metadata()
		attrs := map[string]string{}
		for k, v := range m.Header {
			if len(v) > 0 {
				attrs[k] = v[0]
			}
		}
		dc := 1
		var msgID string
		if md != nil {
			dc = int(md.NumDelivered)
			msgID = strconv.FormatUint(md.Sequence.Stream, 10)
		}
		out = append(out, domain.Message{
			MessageID:     msgID,
			Body:          append([]byte(nil), m.Data...),
			Attributes:    attrs,
			ReceiptHandle: m.Reply, // NATS ack subject; opaque to the caller
			ReceivedAt:    now,
			DeliveryCount: dc,
		})
	}
	return out, nil
}

// AckAck etc. — the well-known JetStream ack payloads. We publish
// directly to the reply subject from the receipt handle without
// holding the original *nats.Msg, keeping the shim stateless.
var (
	ackAck        = []byte("+ACK")
	ackInProgress = []byte("+WPI")
	ackTerm       = []byte("+TERM") //nolint:unused
)

func (b *Backend) DeleteMessage(ctx context.Context, queueName string, receiptHandle string) error {
	if receiptHandle == "" {
		return domain.InvalidReceiptHandle("receipt handle is empty")
	}
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if err := b.nc.Publish(receiptHandle, ackAck); err != nil {
		return translateErr(err, queueName)
	}
	return b.nc.FlushWithContext(ctx)
}

func (b *Backend) ChangeVisibility(ctx context.Context, queueName string, receiptHandle string, visibilityTimeout int) error {
	if receiptHandle == "" {
		return domain.InvalidReceiptHandle("receipt handle is empty")
	}
	// NATS JetStream's InProgress resets the deadline to the
	// consumer's AckWait — it doesn't accept a per-call timeout.
	// The new timeout value is silently ignored on this backend
	// (consumer-level AckWait is the source of truth). Documented
	// in OPERATIONS.md.
	_ = visibilityTimeout
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if err := b.nc.Publish(receiptHandle, ackInProgress); err != nil {
		return translateErr(err, queueName)
	}
	return b.nc.FlushWithContext(ctx)
}
