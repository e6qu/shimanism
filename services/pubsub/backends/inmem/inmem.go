// Package inmem is an in-process pub/sub backend used by the
// conformance harness as the always-on baseline. Not a production
// backend — `shim pubsub -backend=inmem` is for tests only.
//
// Topics own a set of named subscriptions; each subscription has
// its own delivery queue (pending + in-flight), so a single Publish
// fanouts a copy into every subscription. Lazy in-flight
// reclamation mirrors the Phase 3 queue inmem — no goroutine timer.
package inmem

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/pubsub/domain"
)

// Backend implements domain.Pubsub entirely in memory.
type Backend struct {
	mu     sync.Mutex
	topics map[string]*topicState
	subs   map[string]*subscriptionState // by sub name (global)
}

type topicState struct {
	name      string
	createdAt time.Time
	// subs is the set of subscription names attached to this topic.
	subs map[string]struct{}
}

type subscriptionState struct {
	name      string
	topic     string
	ackDeadlineSeconds int
	durable   bool
	createdAt time.Time

	pending  []*messageState
	inflight map[string]*messageState // keyed by receipt handle
}

type messageState struct {
	id            string
	body          []byte
	attributes    map[string]string
	enqueuedAt    time.Time
	deliveryCount int
	receiptHandle string
	visibleAt     time.Time
}

func New() *Backend {
	return &Backend{
		topics: map[string]*topicState{},
		subs:   map[string]*subscriptionState{},
	}
}

var _ domain.Pubsub = (*Backend)(nil)

func copyStrMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ----------------------------------------------------------------------
// Topic ops
// ----------------------------------------------------------------------

func (b *Backend) CreateTopic(ctx context.Context, name string, opt domain.CreateTopicOptions) (domain.Topic, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.topics[name]; ok {
		return domain.Topic{}, domain.TopicAlreadyExists(name)
	}
	now := time.Now().UTC()
	b.topics[name] = &topicState{
		name:      name,
		createdAt: now,
		subs:      map[string]struct{}{},
	}
	return domain.Topic{Name: name, CreatedAt: now}, nil
}

func (b *Backend) DeleteTopic(ctx context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.topics[name]
	if !ok {
		return domain.NoSuchTopic(name)
	}
	for sn := range t.subs {
		delete(b.subs, sn)
	}
	delete(b.topics, name)
	return nil
}

func (b *Backend) HeadTopic(ctx context.Context, name string) (domain.Topic, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.topics[name]
	if !ok {
		return domain.Topic{}, domain.NoSuchTopic(name)
	}
	return domain.Topic{Name: t.name, CreatedAt: t.createdAt}, nil
}

func (b *Backend) ListTopics(ctx context.Context, opt domain.ListTopicsOptions) (domain.ListTopicsResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	names := make([]string, 0, len(b.topics))
	for n := range b.topics {
		if opt.Prefix != "" && !strings.HasPrefix(n, opt.Prefix) {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	res := domain.ListTopicsResult{}
	for _, n := range names {
		t := b.topics[n]
		res.Topics = append(res.Topics, domain.Topic{Name: t.name, CreatedAt: t.createdAt})
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
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.topics[topic]
	if !ok {
		return domain.Subscription{}, domain.NoSuchTopic(topic)
	}
	if _, ok := b.subs[sub]; ok {
		return domain.Subscription{}, domain.SubscriptionAlreadyExists(sub)
	}
	now := time.Now().UTC()
	deadline := opt.AckDeadlineSeconds
	if deadline <= 0 {
		deadline = 30
	}
	if deadline > 600 {
		deadline = 600
	}
	s := &subscriptionState{
		name:               sub,
		topic:              topic,
		ackDeadlineSeconds: deadline,
		durable:            opt.Durable,
		createdAt:          now,
		inflight:           map[string]*messageState{},
	}
	b.subs[sub] = s
	t.subs[sub] = struct{}{}
	return b.subscriptionFromState(s), nil
}

func (b *Backend) DeleteSubscription(ctx context.Context, sub string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.subs[sub]
	if !ok {
		return domain.NoSuchSubscription(sub)
	}
	if t, ok := b.topics[s.topic]; ok {
		delete(t.subs, sub)
	}
	delete(b.subs, sub)
	return nil
}

func (b *Backend) HeadSubscription(ctx context.Context, sub string) (domain.Subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.subs[sub]
	if !ok {
		return domain.Subscription{}, domain.NoSuchSubscription(sub)
	}
	return b.subscriptionFromState(s), nil
}

func (b *Backend) ListSubscriptions(ctx context.Context, opt domain.ListSubscriptionsOptions) (domain.ListSubscriptionsResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	names := make([]string, 0, len(b.subs))
	for n, s := range b.subs {
		if opt.Topic != "" && s.topic != opt.Topic {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	res := domain.ListSubscriptionsResult{}
	for _, n := range names {
		res.Subscriptions = append(res.Subscriptions, b.subscriptionFromState(b.subs[n]))
		if opt.MaxResults > 0 && len(res.Subscriptions) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) subscriptionFromState(s *subscriptionState) domain.Subscription {
	return domain.Subscription{
		Name:               s.name,
		Topic:              s.topic,
		AckDeadlineSeconds: s.ackDeadlineSeconds,
		Durable:            s.durable,
		CreatedAt:          s.createdAt,
	}
}

// ----------------------------------------------------------------------
// Publish / Receive / Ack / ChangeVisibility
// ----------------------------------------------------------------------

func (b *Backend) Publish(ctx context.Context, topic string, opt domain.PublishOptions) (domain.PublishResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.topics[topic]
	if !ok {
		return domain.PublishResult{}, domain.NoSuchTopic(topic)
	}
	id := newID()
	now := time.Now().UTC()
	for sn := range t.subs {
		s := b.subs[sn]
		if s == nil {
			continue
		}
		s.pending = append(s.pending, &messageState{
			id:         id,
			body:       copyBytes(opt.Body),
			attributes: copyStrMap(opt.Attributes),
			enqueuedAt: now,
		})
	}
	return domain.PublishResult{MessageID: id}, nil
}

func (b *Backend) Receive(ctx context.Context, sub string, opt domain.ReceiveOptions) ([]domain.Message, error) {
	deadline := time.Now().Add(time.Duration(opt.WaitTime) * time.Second)
	max := opt.MaxMessages
	if max <= 0 || max > 10 {
		max = 10
	}
	for {
		b.mu.Lock()
		s, ok := b.subs[sub]
		if !ok {
			b.mu.Unlock()
			return nil, domain.NoSuchSubscription(sub)
		}
		now := time.Now().UTC()
		s.reclaimInflight(now)
		visibility := opt.AckDeadline
		if visibility <= 0 {
			visibility = s.ackDeadlineSeconds
		}
		if visibility > 600 {
			visibility = 600
		}
		out := make([]domain.Message, 0, max)
		remaining := make([]*messageState, 0, len(s.pending))
		for _, m := range s.pending {
			if len(out) < max {
				rh := newID()
				m.receiptHandle = rh
				m.visibleAt = now.Add(time.Duration(visibility) * time.Second)
				m.deliveryCount++
				s.inflight[rh] = m
				out = append(out, domain.Message{
					MessageID:     m.id,
					Body:          copyBytes(m.body),
					Attributes:    copyStrMap(m.attributes),
					ReceiptHandle: rh,
					ReceivedAt:    now,
					DeliveryCount: m.deliveryCount,
				})
			} else {
				remaining = append(remaining, m)
			}
		}
		s.pending = remaining
		b.mu.Unlock()
		if len(out) > 0 || opt.WaitTime <= 0 || !time.Now().Before(deadline) {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (b *Backend) Ack(ctx context.Context, sub string, receiptHandle string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.subs[sub]
	if !ok {
		return domain.NoSuchSubscription(sub)
	}
	if _, ok := s.inflight[receiptHandle]; !ok {
		return domain.InvalidReceiptHandle("receipt handle is not in flight on " + sub)
	}
	delete(s.inflight, receiptHandle)
	return nil
}

func (b *Backend) ChangeVisibility(ctx context.Context, sub string, receiptHandle string, visibilityTimeout int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.subs[sub]
	if !ok {
		return domain.NoSuchSubscription(sub)
	}
	m, ok := s.inflight[receiptHandle]
	if !ok {
		return domain.InvalidReceiptHandle("receipt handle is not in flight on " + sub)
	}
	now := time.Now().UTC()
	if visibilityTimeout <= 0 {
		m.receiptHandle = ""
		m.visibleAt = time.Time{}
		s.pending = append(s.pending, m)
		delete(s.inflight, receiptHandle)
		return nil
	}
	if visibilityTimeout > 600 {
		visibilityTimeout = 600
	}
	m.visibleAt = now.Add(time.Duration(visibilityTimeout) * time.Second)
	return nil
}

func (s *subscriptionState) reclaimInflight(now time.Time) {
	for h, m := range s.inflight {
		if !now.Before(m.visibleAt) {
			m.receiptHandle = ""
			m.visibleAt = time.Time{}
			s.pending = append(s.pending, m)
			delete(s.inflight, h)
		}
	}
}
