// Package gcp is the GCP Pub/Sub fanout backend for shimanism's
// pubsub service. Topic + subscriptions are first-class separate
// resources (vs Phase 3's collapsed one-queue model); multiple
// subscriptions can attach to one topic and each receives a copy
// of every published message.
//
// Uses google.golang.org/api/pubsub/v1 (the synchronous REST SDK)
// for the same reasons as Phase 3 — the streaming go/pubsub
// library is a poor fit for per-call receive contracts.
package gcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	pubsubraw "google.golang.org/api/pubsub/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e6qu/shimanism/internal/pubsub/domain"
)

type Config struct {
	ProjectID string
}

type Backend struct {
	svc     *pubsubraw.Service
	project string
}

func New(svc *pubsubraw.Service, cfg Config) *Backend {
	return &Backend{svc: svc, project: cfg.ProjectID}
}

var _ domain.Pubsub = (*Backend)(nil)

func (b *Backend) topicResource(name string) string {
	return fmt.Sprintf("projects/%s/topics/%s", b.project, name)
}

func (b *Backend) subResource(name string) string {
	return fmt.Sprintf("projects/%s/subscriptions/%s", b.project, name)
}

func nameFromResource(r string) string {
	if i := strings.LastIndex(r, "/"); i >= 0 {
		return r[i+1:]
	}
	return r
}

func translateErr(err error, kind, name string) error {
	if err == nil {
		return nil
	}
	if s, ok := status.FromError(err); ok {
		switch s.Code() {
		case codes.NotFound:
			if kind == "topic" {
				return domain.NoSuchTopic(name)
			}
			return domain.NoSuchSubscription(name)
		case codes.AlreadyExists:
			if kind == "topic" {
				return domain.TopicAlreadyExists(name)
			}
			return domain.SubscriptionAlreadyExists(name)
		case codes.InvalidArgument:
			return domain.InvalidArgument(s.Message())
		}
	}
	es := err.Error()
	switch {
	case strings.Contains(es, "404"):
		if kind == "topic" {
			return domain.NoSuchTopic(name)
		}
		return domain.NoSuchSubscription(name)
	case strings.Contains(es, "409"):
		if kind == "topic" {
			return domain.TopicAlreadyExists(name)
		}
		return domain.SubscriptionAlreadyExists(name)
	}
	return err
}

// ----------------------------------------------------------------------
// Topics
// ----------------------------------------------------------------------

func (b *Backend) CreateTopic(ctx context.Context, name string, opt domain.CreateTopicOptions) (domain.Topic, error) {
	if b.project == "" {
		return domain.Topic{}, domain.InvalidArgument("gcp project ID is required")
	}
	if _, err := b.svc.Projects.Topics.Create(b.topicResource(name), &pubsubraw.Topic{}).Context(ctx).Do(); err != nil {
		return domain.Topic{}, translateErr(err, "topic", name)
	}
	return domain.Topic{Name: name, CreatedAt: time.Now().UTC()}, nil
}

func (b *Backend) DeleteTopic(ctx context.Context, name string) error {
	if _, err := b.svc.Projects.Topics.Delete(b.topicResource(name)).Context(ctx).Do(); err != nil {
		return translateErr(err, "topic", name)
	}
	return nil
}

func (b *Backend) HeadTopic(ctx context.Context, name string) (domain.Topic, error) {
	if _, err := b.svc.Projects.Topics.Get(b.topicResource(name)).Context(ctx).Do(); err != nil {
		return domain.Topic{}, translateErr(err, "topic", name)
	}
	return domain.Topic{Name: name}, nil
}

func (b *Backend) ListTopics(ctx context.Context, opt domain.ListTopicsOptions) (domain.ListTopicsResult, error) {
	parent := fmt.Sprintf("projects/%s", b.project)
	out, err := b.svc.Projects.Topics.List(parent).Context(ctx).Do()
	if err != nil {
		return domain.ListTopicsResult{}, translateErr(err, "topic", "")
	}
	res := domain.ListTopicsResult{}
	for _, t := range out.Topics {
		name := nameFromResource(t.Name)
		if opt.Prefix != "" && !strings.HasPrefix(name, opt.Prefix) {
			continue
		}
		res.Topics = append(res.Topics, domain.Topic{Name: name})
		if opt.MaxResults > 0 && len(res.Topics) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

// ----------------------------------------------------------------------
// Subscriptions
// ----------------------------------------------------------------------

func (b *Backend) CreateSubscription(ctx context.Context, topic, sub string, opt domain.CreateSubscriptionOptions) (domain.Subscription, error) {
	if b.project == "" {
		return domain.Subscription{}, domain.InvalidArgument("gcp project ID is required")
	}
	body := &pubsubraw.Subscription{
		Topic:              b.topicResource(topic),
		AckDeadlineSeconds: int64(opt.AckDeadlineSeconds),
	}
	if body.AckDeadlineSeconds == 0 {
		body.AckDeadlineSeconds = 10
	}
	if _, err := b.svc.Projects.Subscriptions.Create(b.subResource(sub), body).Context(ctx).Do(); err != nil {
		// 404 means the topic doesn't exist.
		if strings.Contains(err.Error(), "404") {
			return domain.Subscription{}, domain.NoSuchTopic(topic)
		}
		return domain.Subscription{}, translateErr(err, "subscription", sub)
	}
	return domain.Subscription{
		Name:               sub,
		Topic:              topic,
		AckDeadlineSeconds: int(body.AckDeadlineSeconds),
		Durable:            true,
	}, nil
}

func (b *Backend) DeleteSubscription(ctx context.Context, sub string) error {
	if _, err := b.svc.Projects.Subscriptions.Delete(b.subResource(sub)).Context(ctx).Do(); err != nil {
		return translateErr(err, "subscription", sub)
	}
	return nil
}

func (b *Backend) HeadSubscription(ctx context.Context, sub string) (domain.Subscription, error) {
	out, err := b.svc.Projects.Subscriptions.Get(b.subResource(sub)).Context(ctx).Do()
	if err != nil {
		return domain.Subscription{}, translateErr(err, "subscription", sub)
	}
	return domain.Subscription{
		Name:               sub,
		Topic:              nameFromResource(out.Topic),
		AckDeadlineSeconds: int(out.AckDeadlineSeconds),
		Durable:            true,
	}, nil
}

func (b *Backend) ListSubscriptions(ctx context.Context, opt domain.ListSubscriptionsOptions) (domain.ListSubscriptionsResult, error) {
	parent := fmt.Sprintf("projects/%s", b.project)
	out, err := b.svc.Projects.Subscriptions.List(parent).Context(ctx).Do()
	if err != nil {
		return domain.ListSubscriptionsResult{}, translateErr(err, "subscription", "")
	}
	res := domain.ListSubscriptionsResult{}
	for _, s := range out.Subscriptions {
		topic := nameFromResource(s.Topic)
		if opt.Topic != "" && topic != opt.Topic {
			continue
		}
		res.Subscriptions = append(res.Subscriptions, domain.Subscription{
			Name:               nameFromResource(s.Name),
			Topic:              topic,
			AckDeadlineSeconds: int(s.AckDeadlineSeconds),
			Durable:            true,
		})
		if opt.MaxResults > 0 && len(res.Subscriptions) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

// ----------------------------------------------------------------------
// Publish / Receive / Ack / ChangeVisibility
// ----------------------------------------------------------------------

func (b *Backend) Publish(ctx context.Context, topic string, opt domain.PublishOptions) (domain.PublishResult, error) {
	req := &pubsubraw.PublishRequest{
		Messages: []*pubsubraw.PubsubMessage{{
			Data:       base64.StdEncoding.EncodeToString(opt.Body),
			Attributes: opt.Attributes,
		}},
	}
	out, err := b.svc.Projects.Topics.Publish(b.topicResource(topic), req).Context(ctx).Do()
	if err != nil {
		return domain.PublishResult{}, translateErr(err, "topic", topic)
	}
	if len(out.MessageIds) == 0 {
		return domain.PublishResult{}, nil
	}
	return domain.PublishResult{MessageID: out.MessageIds[0]}, nil
}

func (b *Backend) Receive(ctx context.Context, sub string, opt domain.ReceiveOptions) ([]domain.Message, error) {
	max := opt.MaxMessages
	if max <= 0 || max > 10 {
		max = 10
	}
	req := &pubsubraw.PullRequest{
		MaxMessages:       int64(max),
		ReturnImmediately: opt.WaitTime <= 0,
	}
	pull, err := b.svc.Projects.Subscriptions.Pull(b.subResource(sub), req).Context(ctx).Do()
	if err != nil {
		return nil, translateErr(err, "subscription", sub)
	}
	res := make([]domain.Message, 0, len(pull.ReceivedMessages))
	now := time.Now().UTC()
	for _, rm := range pull.ReceivedMessages {
		data, _ := base64.StdEncoding.DecodeString(rm.Message.Data)
		res = append(res, domain.Message{
			MessageID:     rm.Message.MessageId,
			Body:          data,
			Attributes:    rm.Message.Attributes,
			ReceiptHandle: rm.AckId,
			ReceivedAt:    now,
			DeliveryCount: int(rm.DeliveryAttempt),
		})
	}
	return res, nil
}

func (b *Backend) Ack(ctx context.Context, sub string, receiptHandle string) error {
	_, err := b.svc.Projects.Subscriptions.Acknowledge(b.subResource(sub), &pubsubraw.AcknowledgeRequest{
		AckIds: []string{receiptHandle},
	}).Context(ctx).Do()
	if err != nil {
		return translateErr(err, "subscription", sub)
	}
	return nil
}

func (b *Backend) ChangeVisibility(ctx context.Context, sub string, receiptHandle string, visibilityTimeout int) error {
	_, err := b.svc.Projects.Subscriptions.ModifyAckDeadline(b.subResource(sub), &pubsubraw.ModifyAckDeadlineRequest{
		AckIds:             []string{receiptHandle},
		AckDeadlineSeconds: int64(visibilityTimeout),
	}).Context(ctx).Do()
	if err != nil {
		return translateErr(err, "subscription", sub)
	}
	return nil
}
