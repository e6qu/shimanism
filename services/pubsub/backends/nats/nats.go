// Package nats is the NATS JetStream backend for shimanism's
// pubsub service, acting as the K8s peer per PLAN.md Phase 4.
// Topics map to JetStream streams; subscriptions map to durable
// pull consumers attached to that stream. Publishing fans out via
// JetStream's stream/consumer fanout — every consumer attached to
// the topic's stream receives a copy of every message.
//
// The Phase 4 OPERATIONS.md drafted core NATS (in-memory subject
// pub/sub) for non-durable fanout; in practice JetStream consumers
// give us durable cross-cloud-equivalent semantics for both modes,
// matching AWS / GCP / Azure (where subscriptions are always
// durable). The `Subscription.Durable` flag is recorded but
// doesn't change wire behaviour on this backend.
//
// Same stateless rules + per-call deadline wrapping as the Phase 3
// NATS queue backend.
package nats

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	natsapi "github.com/nats-io/nats.go"

	"github.com/e6qu/shimanism/internal/pubsub/domain"
)

type Backend struct {
	nc *natsapi.Conn
	js natsapi.JetStreamContext
}

func New(nc *natsapi.Conn) (*Backend, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("attach JetStream context: %w", err)
	}
	return &Backend{nc: nc, js: js}, nil
}

var _ domain.Pubsub = (*Backend)(nil)

func withDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, 30*time.Second)
}

func streamName(topic string) string {
	var b strings.Builder
	b.Grow(len(topic))
	for i := 0; i < len(topic); i++ {
		c := topic[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '-':
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func translateErr(err error, kind string, name string) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, natsapi.ErrStreamNotFound):
		return domain.NoSuchTopic(name)
	case errors.Is(err, natsapi.ErrConsumerNotFound):
		return domain.NoSuchSubscription(name)
	case errors.Is(err, natsapi.ErrStreamNameAlreadyInUse):
		return domain.TopicAlreadyExists(name)
	}
	_ = kind
	return err
}

// ----------------------------------------------------------------------
// Topic ops
// ----------------------------------------------------------------------

func (b *Backend) CreateTopic(ctx context.Context, name string, opt domain.CreateTopicOptions) (domain.Topic, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	stream := streamName(name)
	info, err := b.js.AddStream(&natsapi.StreamConfig{
		Name:      stream,
		Subjects:  []string{stream},
		Retention: natsapi.InterestPolicy,
		MaxAge:    4 * 24 * time.Hour,
		Storage:   natsapi.FileStorage,
	}, natsapi.Context(ctx))
	if err != nil {
		return domain.Topic{}, translateErr(err, "topic", name)
	}
	return domain.Topic{Name: name, CreatedAt: info.Created}, nil
}

func (b *Backend) DeleteTopic(ctx context.Context, name string) error {
	if err := b.js.DeleteStream(streamName(name)); err != nil {
		return translateErr(err, "topic", name)
	}
	return nil
}

func (b *Backend) HeadTopic(ctx context.Context, name string) (domain.Topic, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	info, err := b.js.StreamInfo(streamName(name), natsapi.Context(ctx))
	if err != nil {
		return domain.Topic{}, translateErr(err, "topic", name)
	}
	return domain.Topic{Name: name, CreatedAt: info.Created}, nil
}

func (b *Backend) ListTopics(ctx context.Context, opt domain.ListTopicsOptions) (domain.ListTopicsResult, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	names := b.js.StreamNames(natsapi.Context(ctx))
	res := domain.ListTopicsResult{}
	for n := range names {
		if opt.Prefix != "" && !strings.HasPrefix(n, opt.Prefix) {
			continue
		}
		t, err := b.HeadTopic(ctx, n)
		if err != nil {
			continue
		}
		res.Topics = append(res.Topics, t)
		if opt.MaxResults > 0 && len(res.Topics) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

// ----------------------------------------------------------------------
// Subscription ops
// ----------------------------------------------------------------------

func (b *Backend) CreateSubscription(ctx context.Context, topic, sub string, opt domain.CreateSubscriptionOptions) (domain.Subscription, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	stream := streamName(topic)
	ackWait := time.Duration(opt.AckDeadlineSeconds) * time.Second
	if ackWait == 0 {
		ackWait = 30 * time.Second
	}
	if _, err := b.js.AddConsumer(stream, &natsapi.ConsumerConfig{
		Durable:    sub,
		AckPolicy:  natsapi.AckExplicitPolicy,
		AckWait:    ackWait,
		MaxDeliver: -1,
	}, natsapi.Context(ctx)); err != nil {
		// JetStream returns a specific error if the consumer already
		// exists; surface it as SubscriptionAlreadyExists.
		if strings.Contains(err.Error(), "already in use") {
			return domain.Subscription{}, domain.SubscriptionAlreadyExists(sub)
		}
		return domain.Subscription{}, translateErr(err, "subscription", topic)
	}
	return domain.Subscription{
		Name:               sub,
		Topic:              topic,
		AckDeadlineSeconds: int(ackWait / time.Second),
		Durable:            opt.Durable,
		CreatedAt:          time.Now().UTC(),
	}, nil
}

func (b *Backend) DeleteSubscription(ctx context.Context, sub string) error {
	// JetStream consumers are addressed by (stream, consumer); the
	// shim's domain identifies subs by name alone, so we must locate
	// the owning stream. Iterate stream names + try DeleteConsumer
	// on each. The first one to succeed wins.
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	for n := range b.js.StreamNames(natsapi.Context(ctx)) {
		if err := b.js.DeleteConsumer(n, sub); err == nil {
			return nil
		}
	}
	return domain.NoSuchSubscription(sub)
}

func (b *Backend) HeadSubscription(ctx context.Context, sub string) (domain.Subscription, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	for n := range b.js.StreamNames(natsapi.Context(ctx)) {
		info, err := b.js.ConsumerInfo(n, sub, natsapi.Context(ctx))
		if err == nil && info != nil {
			return domain.Subscription{
				Name:               sub,
				Topic:              info.Stream,
				AckDeadlineSeconds: int(info.Config.AckWait / time.Second),
				Durable:            true,
				CreatedAt:          info.Created,
			}, nil
		}
	}
	return domain.Subscription{}, domain.NoSuchSubscription(sub)
}

func (b *Backend) ListSubscriptions(ctx context.Context, opt domain.ListSubscriptionsOptions) (domain.ListSubscriptionsResult, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	res := domain.ListSubscriptionsResult{}
	for n := range b.js.StreamNames(natsapi.Context(ctx)) {
		if opt.Topic != "" && n != streamName(opt.Topic) {
			continue
		}
		for cinfo := range b.js.Consumers(n, natsapi.Context(ctx)) {
			res.Subscriptions = append(res.Subscriptions, domain.Subscription{
				Name:               cinfo.Name,
				Topic:              n,
				AckDeadlineSeconds: int(cinfo.Config.AckWait / time.Second),
				Durable:            true,
				CreatedAt:          cinfo.Created,
			})
			if opt.MaxResults > 0 && len(res.Subscriptions) >= opt.MaxResults {
				return res, nil
			}
		}
	}
	return res, nil
}

// ----------------------------------------------------------------------
// Publish / Receive / Ack / ChangeVisibility
// ----------------------------------------------------------------------

func (b *Backend) Publish(ctx context.Context, topic string, opt domain.PublishOptions) (domain.PublishResult, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	stream := streamName(topic)
	msg := &natsapi.Msg{
		Subject: stream,
		Data:    append([]byte(nil), opt.Body...),
		Header:  natsapi.Header{},
	}
	for k, v := range opt.Attributes {
		msg.Header.Set(k, v)
	}
	ack, err := b.js.PublishMsg(msg, natsapi.Context(ctx))
	if err != nil {
		return domain.PublishResult{}, translateErr(err, "topic", topic)
	}
	return domain.PublishResult{MessageID: strconv.FormatUint(ack.Sequence, 10)}, nil
}

func (b *Backend) Receive(ctx context.Context, sub string, opt domain.ReceiveOptions) ([]domain.Message, error) {
	// Find the stream that owns this consumer.
	ctxd, cancel := withDeadline(ctx)
	defer cancel()
	var ownerStream string
	for n := range b.js.StreamNames(natsapi.Context(ctxd)) {
		if _, err := b.js.ConsumerInfo(n, sub, natsapi.Context(ctxd)); err == nil {
			ownerStream = n
			break
		}
	}
	if ownerStream == "" {
		return nil, domain.NoSuchSubscription(sub)
	}
	pullSub, err := b.js.PullSubscribe("", sub, natsapi.Bind(ownerStream, sub))
	if err != nil {
		return nil, translateErr(err, "subscription", sub)
	}
	defer func() { _ = pullSub.Unsubscribe() }()
	n := opt.MaxMessages
	if n <= 0 || n > 10 {
		n = 10
	}
	wait := time.Duration(opt.WaitTime) * time.Second
	if wait <= 0 {
		wait = 100 * time.Millisecond
	}
	msgs, err := pullSub.Fetch(n, natsapi.MaxWait(wait))
	if err != nil && !errors.Is(err, natsapi.ErrTimeout) {
		return nil, translateErr(err, "subscription", sub)
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
			ReceiptHandle: m.Reply,
			ReceivedAt:    now,
			DeliveryCount: dc,
		})
	}
	return out, nil
}

func (b *Backend) Ack(ctx context.Context, sub string, receiptHandle string) error {
	if receiptHandle == "" {
		return domain.InvalidReceiptHandle("receipt handle is empty")
	}
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if err := b.nc.Publish(receiptHandle, []byte("+ACK")); err != nil {
		return translateErr(err, "subscription", sub)
	}
	return b.nc.FlushWithContext(ctx)
}

func (b *Backend) ChangeVisibility(ctx context.Context, sub string, receiptHandle string, visibilityTimeout int) error {
	if receiptHandle == "" {
		return domain.InvalidReceiptHandle("receipt handle is empty")
	}
	_ = visibilityTimeout
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if err := b.nc.Publish(receiptHandle, []byte("+WPI")); err != nil {
		return translateErr(err, "subscription", sub)
	}
	return b.nc.FlushWithContext(ctx)
}
