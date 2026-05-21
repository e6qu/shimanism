// Package gcp is the GCP Pub/Sub backend for shimanism's queue
// service. Each domain queue maps to a Pub/Sub topic + subscription
// pair sharing the queue's name. Receipt handles are AckIds, passed
// through unchanged.
package gcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	pubsubraw "google.golang.org/api/pubsub/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e6qu/shimanism/internal/queue/domain"
)

// Config holds GCP-specific knobs.
type Config struct {
	ProjectID string
}

// Backend implements domain.Queues via GCP Pub/Sub.
type Backend struct {
	svc     *pubsubraw.Service
	project string
}

// New wraps a configured Pub/Sub REST service.
func New(svc *pubsubraw.Service, cfg Config) *Backend {
	return &Backend{svc: svc, project: cfg.ProjectID}
}

var _ domain.Queues = (*Backend)(nil)

func (b *Backend) topicName(queue string) string {
	return fmt.Sprintf("projects/%s/topics/%s", b.project, queue)
}

func (b *Backend) subscriptionName(queue string) string {
	return fmt.Sprintf("projects/%s/subscriptions/%s", b.project, queue)
}

func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	if s, ok := status.FromError(err); ok {
		switch s.Code() {
		case codes.NotFound:
			return domain.NoSuchQueue(name)
		case codes.AlreadyExists:
			return domain.QueueAlreadyExists(name)
		case codes.InvalidArgument:
			return domain.InvalidArgument(s.Message())
		}
	}
	es := err.Error()
	switch {
	case strings.Contains(es, "404"):
		return domain.NoSuchQueue(name)
	case strings.Contains(es, "409"):
		return domain.QueueAlreadyExists(name)
	}
	return err
}

func (b *Backend) CreateQueue(ctx context.Context, name string, opt domain.CreateQueueOptions) (domain.Queue, error) {
	if b.project == "" {
		return domain.Queue{}, domain.InvalidArgument("gcp project ID is required")
	}
	if _, err := b.svc.Projects.Topics.Create(b.topicName(name), &pubsubraw.Topic{}).Context(ctx).Do(); err != nil {
		return domain.Queue{}, translateErr(err, name)
	}
	ack := opt.Attributes.VisibilityTimeoutSeconds
	if ack <= 0 {
		ack = 10
	}
	if ack > 600 {
		ack = 600
	}
	sub := &pubsubraw.Subscription{
		Topic:              b.topicName(name),
		AckDeadlineSeconds: int64(ack),
	}
	if _, err := b.svc.Projects.Subscriptions.Create(b.subscriptionName(name), sub).Context(ctx).Do(); err != nil {
		// Best-effort: clean up the topic we just created.
		_, _ = b.svc.Projects.Topics.Delete(b.topicName(name)).Context(ctx).Do()
		return domain.Queue{}, translateErr(err, name)
	}
	return domain.Queue{Name: name, Attributes: opt.Attributes}, nil
}

func (b *Backend) SetQueueAttributes(ctx context.Context, name string, attrs domain.QueueAttributes) error {
	// GCP Pub/Sub subscriptions support in-place patch of
	// ackDeadlineSeconds + messageRetentionDuration via
	// subscriptions.patch. DelaySeconds + MaxMessageSize have no GCP
	// analog; per services/queue/APPLY_INTERSECTION.md they must
	// surface a source-shaped unsupported error rather than silently
	// no-op (caller would think the attribute was honored while the
	// next read returns the GCP default).
	if attrs.DelaySeconds > 0 {
		return domain.InvalidArgument("DelaySeconds has no GCP Pub/Sub analog; out of cross-cloud intersection")
	}
	if attrs.MaxMessageSizeBytes > 0 {
		return domain.InvalidArgument("MaxMessageSize has no GCP Pub/Sub analog; out of cross-cloud intersection")
	}
	if attrs.VisibilityTimeoutSeconds <= 0 && attrs.MessageRetentionSeconds <= 0 {
		// No-op when nothing in-contract is being set. Still verify
		// the subscription exists so callers see NoSuchQueue if it
		// doesn't, instead of a silently-successful empty call.
		if _, err := b.svc.Projects.Subscriptions.Get(b.subscriptionName(name)).Context(ctx).Do(); err != nil {
			return translateErr(err, name)
		}
		return nil
	}
	sub := &pubsubraw.Subscription{}
	var mask []string
	if attrs.VisibilityTimeoutSeconds > 0 {
		ack := attrs.VisibilityTimeoutSeconds
		if ack > 600 {
			ack = 600
		}
		sub.AckDeadlineSeconds = int64(ack)
		mask = append(mask, "ackDeadlineSeconds")
	}
	if attrs.MessageRetentionSeconds > 0 {
		sub.MessageRetentionDuration = strconv.Itoa(attrs.MessageRetentionSeconds) + "s"
		mask = append(mask, "messageRetentionDuration")
	}
	req := &pubsubraw.UpdateSubscriptionRequest{
		Subscription: sub,
		UpdateMask:   strings.Join(mask, ","),
	}
	_, err := b.svc.Projects.Subscriptions.Patch(b.subscriptionName(name), req).Context(ctx).Do()
	return translateErr(err, name)
}

// Tags map to GCP subscription labels. GCP label keys + values are
// constrained (lowercase, dashes, underscores, ≤63 chars) — invalid
// tags surface as GCP's native InvalidArgument from the patch RPC,
// which translateErr surfaces honestly. No silent normalisation.
func (b *Backend) ListQueueTags(ctx context.Context, name string) (map[string]string, error) {
	sub, err := b.svc.Projects.Subscriptions.Get(b.subscriptionName(name)).Context(ctx).Do()
	if err != nil {
		return nil, translateErr(err, name)
	}
	if sub.Labels == nil {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(sub.Labels))
	for k, v := range sub.Labels {
		out[k] = v
	}
	return out, nil
}

func (b *Backend) TagQueue(ctx context.Context, name string, tags map[string]string) error {
	if len(tags) == 0 {
		return nil
	}
	cur, err := b.svc.Projects.Subscriptions.Get(b.subscriptionName(name)).Context(ctx).Do()
	if err != nil {
		return translateErr(err, name)
	}
	labels := map[string]string{}
	for k, v := range cur.Labels {
		labels[k] = v
	}
	for k, v := range tags {
		labels[k] = v
	}
	req := &pubsubraw.UpdateSubscriptionRequest{
		Subscription: &pubsubraw.Subscription{Labels: labels},
		UpdateMask:   "labels",
	}
	_, err = b.svc.Projects.Subscriptions.Patch(b.subscriptionName(name), req).Context(ctx).Do()
	return translateErr(err, name)
}

func (b *Backend) UntagQueue(ctx context.Context, name string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	cur, err := b.svc.Projects.Subscriptions.Get(b.subscriptionName(name)).Context(ctx).Do()
	if err != nil {
		return translateErr(err, name)
	}
	labels := map[string]string{}
	for k, v := range cur.Labels {
		labels[k] = v
	}
	for _, k := range keys {
		delete(labels, k)
	}
	req := &pubsubraw.UpdateSubscriptionRequest{
		Subscription: &pubsubraw.Subscription{Labels: labels},
		UpdateMask:   "labels",
	}
	_, err = b.svc.Projects.Subscriptions.Patch(b.subscriptionName(name), req).Context(ctx).Do()
	return translateErr(err, name)
}

func (b *Backend) DeleteQueue(ctx context.Context, name string) error {
	_, err := b.svc.Projects.Subscriptions.Delete(b.subscriptionName(name)).Context(ctx).Do()
	if err != nil {
		var de *domain.Error
		te := translateErr(err, name)
		if errors.As(te, &de) && de.Kind == domain.KindNoSuchQueue {
			// fall through to topic delete; the sub may have been
			// deleted out of band.
		} else {
			return te
		}
	}
	if _, err := b.svc.Projects.Topics.Delete(b.topicName(name)).Context(ctx).Do(); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) HeadQueue(ctx context.Context, name string) (domain.Queue, error) {
	sub, err := b.svc.Projects.Subscriptions.Get(b.subscriptionName(name)).Context(ctx).Do()
	if err != nil {
		return domain.Queue{}, translateErr(err, name)
	}
	attrs := domain.QueueAttributes{
		VisibilityTimeoutSeconds: int(sub.AckDeadlineSeconds),
	}
	return domain.Queue{Name: name, Attributes: attrs}, nil
}

func (b *Backend) ListQueues(ctx context.Context, opt domain.ListQueuesOptions) (domain.ListQueuesResult, error) {
	res := domain.ListQueuesResult{}
	call := b.svc.Projects.Subscriptions.List(fmt.Sprintf("projects/%s", b.project))
	if opt.MaxResults > 0 {
		call = call.PageSize(int64(opt.MaxResults))
	}
	out, err := call.Context(ctx).Do()
	if err != nil {
		return domain.ListQueuesResult{}, translateErr(err, "")
	}
	for _, s := range out.Subscriptions {
		short := s.Name
		if i := strings.LastIndexByte(short, '/'); i >= 0 {
			short = short[i+1:]
		}
		if opt.Prefix != "" && !strings.HasPrefix(short, opt.Prefix) {
			continue
		}
		res.Queues = append(res.Queues, domain.Queue{
			Name: short,
			Attributes: domain.QueueAttributes{
				VisibilityTimeoutSeconds: int(s.AckDeadlineSeconds),
			},
		})
	}
	return res, nil
}

func (b *Backend) SendMessage(ctx context.Context, queueName string, opt domain.SendMessageOptions) (domain.SendMessageResult, error) {
	msg := &pubsubraw.PubsubMessage{
		Data:       base64.StdEncoding.EncodeToString(opt.Body),
		Attributes: opt.Attributes,
	}
	out, err := b.svc.Projects.Topics.Publish(b.topicName(queueName), &pubsubraw.PublishRequest{
		Messages: []*pubsubraw.PubsubMessage{msg},
	}).Context(ctx).Do()
	if err != nil {
		return domain.SendMessageResult{}, translateErr(err, queueName)
	}
	if len(out.MessageIds) == 0 {
		return domain.SendMessageResult{}, fmt.Errorf("gcp: publish returned no message ids")
	}
	return domain.SendMessageResult{MessageID: out.MessageIds[0]}, nil
}

func (b *Backend) ReceiveMessages(ctx context.Context, queueName string, opt domain.ReceiveMessagesOptions) ([]domain.Message, error) {
	maxN := opt.MaxMessages
	if maxN <= 0 || maxN > 10 {
		maxN = 10
	}
	req := &pubsubraw.PullRequest{
		MaxMessages:       int64(maxN),
		ReturnImmediately: false,
	}
	pctx := ctx
	if opt.WaitTime > 0 {
		var cancel context.CancelFunc
		pctx, cancel = context.WithTimeout(ctx, time.Duration(opt.WaitTime)*time.Second)
		defer cancel()
	}
	out, err := b.svc.Projects.Subscriptions.Pull(b.subscriptionName(queueName), req).Context(pctx).Do()
	if err != nil {
		if strings.Contains(err.Error(), "context deadline exceeded") {
			return nil, nil
		}
		return nil, translateErr(err, queueName)
	}
	now := time.Now().UTC()
	res := make([]domain.Message, 0, len(out.ReceivedMessages))
	for _, rm := range out.ReceivedMessages {
		data, _ := base64.StdEncoding.DecodeString(rm.Message.Data)
		var mid string
		if rm.Message != nil {
			mid = rm.Message.MessageId
		}
		var attrs map[string]string
		if rm.Message != nil && len(rm.Message.Attributes) > 0 {
			attrs = make(map[string]string, len(rm.Message.Attributes))
			for k, v := range rm.Message.Attributes {
				attrs[k] = v
			}
		}
		res = append(res, domain.Message{
			MessageID:     mid,
			Body:          data,
			Attributes:    attrs,
			ReceiptHandle: rm.AckId,
			ReceivedAt:    now,
			DeliveryCount: int(rm.DeliveryAttempt),
		})
	}
	return res, nil
}

func (b *Backend) DeleteMessage(ctx context.Context, queueName string, receiptHandle string) error {
	_, err := b.svc.Projects.Subscriptions.Acknowledge(b.subscriptionName(queueName), &pubsubraw.AcknowledgeRequest{
		AckIds: []string{receiptHandle},
	}).Context(ctx).Do()
	return translateErr(err, queueName)
}

func (b *Backend) ChangeVisibility(ctx context.Context, queueName string, receiptHandle string, visibilityTimeout int) error {
	if visibilityTimeout > 600 {
		visibilityTimeout = 600
	}
	_, err := b.svc.Projects.Subscriptions.ModifyAckDeadline(b.subscriptionName(queueName), &pubsubraw.ModifyAckDeadlineRequest{
		AckIds:             []string{receiptHandle},
		AckDeadlineSeconds: int64(visibilityTimeout),
	}).Context(ctx).Do()
	return translateErr(err, queueName)
}
