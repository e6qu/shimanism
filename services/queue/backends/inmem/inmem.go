// Package inmem is an in-process queue backend used by the
// conformance harness as the always-on baseline. Not a production
// backend — `shim queue -backend=inmem` is for tests only.
//
// Each queue is a sorted list of pending messages + a side list of
// in-flight messages (received but not yet ack'd or visibility-
// expired). A background sweep reclaims expired in-flight entries
// back to pending on the next ReceiveMessages call (no goroutine
// timer; the laziness keeps the backend pure-function).
package inmem

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/queue/domain"
)

// Backend implements domain.Queues entirely in memory.
type Backend struct {
	mu     sync.Mutex
	queues map[string]*queueState
}

type queueState struct {
	name       string
	createdAt  time.Time
	attributes domain.QueueAttributes
	pending    []*messageState
	inflight   map[string]*messageState // keyed by receipt handle
}

type messageState struct {
	id            string
	body          []byte
	attributes    map[string]string
	enqueuedAt    time.Time
	visibleAfter  time.Time
	deliveryCount int
	// receiptHandle is non-empty only while the message is in-flight.
	receiptHandle string
	visibleAt     time.Time // when this message returns to pending if not deleted
}

// New constructs an empty backend.
func New() *Backend {
	return &Backend{queues: map[string]*queueState{}}
}

var _ domain.Queues = (*Backend)(nil)

func copyAttrs(a domain.QueueAttributes) domain.QueueAttributes {
	return a // value type — already a copy
}

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

// reclaimInflight moves any in-flight messages whose visibility has
// expired back to pending. Called lazily by Receive.
func (s *queueState) reclaimInflight(now time.Time) {
	for h, m := range s.inflight {
		if !now.Before(m.visibleAt) {
			m.receiptHandle = ""
			m.visibleAt = time.Time{}
			s.pending = append(s.pending, m)
			delete(s.inflight, h)
		}
	}
}

// pendingNowVisible returns the pending messages whose visibleAfter
// has passed, leaving the others in place for later.
func (s *queueState) takeVisible(now time.Time, n int) []*messageState {
	if n <= 0 {
		return nil
	}
	// Stable order: oldest enqueuedAt first.
	sort.SliceStable(s.pending, func(i, j int) bool {
		return s.pending[i].enqueuedAt.Before(s.pending[j].enqueuedAt)
	})
	var picked []*messageState
	var rest []*messageState
	for _, m := range s.pending {
		if len(picked) < n && !now.Before(m.visibleAfter) {
			picked = append(picked, m)
		} else {
			rest = append(rest, m)
		}
	}
	s.pending = rest
	return picked
}

func (b *Backend) CreateQueue(ctx context.Context, name string, opt domain.CreateQueueOptions) (domain.Queue, error) {
	if name == "" {
		return domain.Queue{}, domain.InvalidArgument("queue name is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.queues[name]; ok {
		return domain.Queue{}, domain.QueueAlreadyExists(name)
	}
	now := time.Now().UTC()
	st := &queueState{
		name:       name,
		createdAt:  now,
		attributes: opt.Attributes,
		inflight:   map[string]*messageState{},
	}
	if st.attributes.VisibilityTimeoutSeconds == 0 {
		st.attributes.VisibilityTimeoutSeconds = 30
	}
	if st.attributes.MessageRetentionSeconds == 0 {
		st.attributes.MessageRetentionSeconds = 4 * 24 * 60 * 60 // 4 days, AWS default
	}
	if st.attributes.MaxMessageSizeBytes == 0 {
		st.attributes.MaxMessageSizeBytes = 256 * 1024
	}
	st.attributes.CreatedAt = now
	b.queues[name] = st
	return queueFromState(st), nil
}

func queueFromState(st *queueState) domain.Queue {
	a := st.attributes
	a.ApproximateMessageCount = len(st.pending) + len(st.inflight)
	return domain.Queue{Name: st.name, Attributes: a}
}

func (b *Backend) DeleteQueue(ctx context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.queues[name]; !ok {
		return domain.NoSuchQueue(name)
	}
	delete(b.queues, name)
	return nil
}

func (b *Backend) SetQueueAttributes(ctx context.Context, name string, attrs domain.QueueAttributes) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.queues[name]
	if !ok {
		return domain.NoSuchQueue(name)
	}
	// AWS SetQueueAttributes merges — zero values mean "leave as-is".
	if attrs.VisibilityTimeoutSeconds > 0 {
		st.attributes.VisibilityTimeoutSeconds = attrs.VisibilityTimeoutSeconds
	}
	if attrs.MessageRetentionSeconds > 0 {
		st.attributes.MessageRetentionSeconds = attrs.MessageRetentionSeconds
	}
	if attrs.MaxMessageSizeBytes > 0 {
		st.attributes.MaxMessageSizeBytes = attrs.MaxMessageSizeBytes
	}
	if attrs.DelaySeconds > 0 {
		st.attributes.DelaySeconds = attrs.DelaySeconds
	}
	return nil
}

func (b *Backend) HeadQueue(ctx context.Context, name string) (domain.Queue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.queues[name]
	if !ok {
		return domain.Queue{}, domain.NoSuchQueue(name)
	}
	st.reclaimInflight(time.Now().UTC())
	return queueFromState(st), nil
}

func (b *Backend) ListQueues(ctx context.Context, opt domain.ListQueuesOptions) (domain.ListQueuesResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	names := make([]string, 0, len(b.queues))
	for n := range b.queues {
		if opt.Prefix != "" && !strings.HasPrefix(n, opt.Prefix) {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	res := domain.ListQueuesResult{Queues: make([]domain.Queue, 0, len(names))}
	for _, n := range names {
		res.Queues = append(res.Queues, queueFromState(b.queues[n]))
	}
	if opt.MaxResults > 0 && len(res.Queues) > opt.MaxResults {
		res.Queues = res.Queues[:opt.MaxResults]
	}
	return res, nil
}

func (b *Backend) SendMessage(ctx context.Context, queueName string, opt domain.SendMessageOptions) (domain.SendMessageResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.queues[queueName]
	if !ok {
		return domain.SendMessageResult{}, domain.NoSuchQueue(queueName)
	}
	if st.attributes.MaxMessageSizeBytes > 0 && len(opt.Body) > st.attributes.MaxMessageSizeBytes {
		return domain.SendMessageResult{}, domain.MessageTooLarge("message body exceeds the queue maximum size")
	}
	now := time.Now().UTC()
	delay := opt.DelaySeconds
	if delay == 0 {
		delay = st.attributes.DelaySeconds
	}
	m := &messageState{
		id:           newID(),
		body:         copyBytes(opt.Body),
		attributes:   copyStrMap(opt.Attributes),
		enqueuedAt:   now,
		visibleAfter: now.Add(time.Duration(delay) * time.Second),
	}
	st.pending = append(st.pending, m)
	return domain.SendMessageResult{MessageID: m.id}, nil
}

func (b *Backend) ReceiveMessages(ctx context.Context, queueName string, opt domain.ReceiveMessagesOptions) ([]domain.Message, error) {
	deadline := time.Now().Add(time.Duration(opt.WaitTime) * time.Second)
	for {
		msgs, err := b.tryReceive(queueName, opt)
		if err != nil {
			return nil, err
		}
		if len(msgs) > 0 || opt.WaitTime == 0 || !time.Now().Before(deadline) {
			return msgs, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (b *Backend) tryReceive(queueName string, opt domain.ReceiveMessagesOptions) ([]domain.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.queues[queueName]
	if !ok {
		return nil, domain.NoSuchQueue(queueName)
	}
	now := time.Now().UTC()
	st.reclaimInflight(now)
	n := opt.MaxMessages
	if n <= 0 || n > 10 {
		n = 10
	}
	picked := st.takeVisible(now, n)
	if len(picked) == 0 {
		return nil, nil
	}
	visibility := opt.VisibilityTimeout
	if visibility == 0 {
		visibility = st.attributes.VisibilityTimeoutSeconds
	}
	out := make([]domain.Message, 0, len(picked))
	for _, m := range picked {
		m.deliveryCount++
		m.receiptHandle = newID()
		m.visibleAt = now.Add(time.Duration(visibility) * time.Second)
		st.inflight[m.receiptHandle] = m
		out = append(out, domain.Message{
			MessageID:     m.id,
			Body:          copyBytes(m.body),
			Attributes:    copyStrMap(m.attributes),
			ReceiptHandle: m.receiptHandle,
			ReceivedAt:    now,
			DeliveryCount: m.deliveryCount,
		})
	}
	return out, nil
}

func (b *Backend) DeleteMessage(ctx context.Context, queueName string, receiptHandle string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.queues[queueName]
	if !ok {
		return domain.NoSuchQueue(queueName)
	}
	if _, ok := st.inflight[receiptHandle]; !ok {
		return domain.InvalidReceiptHandle("receipt handle is not in flight on " + queueName)
	}
	delete(st.inflight, receiptHandle)
	return nil
}

func (b *Backend) ChangeVisibility(ctx context.Context, queueName string, receiptHandle string, visibilityTimeout int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.queues[queueName]
	if !ok {
		return domain.NoSuchQueue(queueName)
	}
	m, ok := st.inflight[receiptHandle]
	if !ok {
		return domain.InvalidReceiptHandle("receipt handle is not in flight on " + queueName)
	}
	now := time.Now().UTC()
	if visibilityTimeout <= 0 {
		// Return to pending immediately.
		m.receiptHandle = ""
		m.visibleAt = time.Time{}
		m.visibleAfter = now
		st.pending = append(st.pending, m)
		delete(st.inflight, receiptHandle)
		return nil
	}
	m.visibleAt = now.Add(time.Duration(visibilityTimeout) * time.Second)
	return nil
}

// ensure copyAttrs is referenced (used by future extensions); silence
// unused-function lint.
var _ = copyAttrs
