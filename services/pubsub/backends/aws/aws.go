// Package aws is the AWS SNS+SQS passthrough backend for
// shimanism's pubsub service. SNS handles topics + subscriptions +
// publishing; SQS handles per-subscription receive against the
// backing queue that SNS subscriptions deliver to.
//
// **CreateSubscription is the interesting op.** Real AWS requires
// (1) the SQS queue to exist, (2) an SNS Subscribe call with
// Protocol=sqs + Endpoint=<queue arn>. The shim auto-creates the
// backing queue in step 1 (queue name == subscription name) and
// fires the Subscribe in step 2. DeleteSubscription reverses both:
// Unsubscribe + DeleteQueue.
//
// Stateless. Topic/subscription/queue identity rides through the
// wire calls — no shim-side mapping persisted.
package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/e6qu/shimanism/internal/pubsub/domain"
)

type Backend struct {
	sns *sns.Client
	sqs *sqs.Client
	// region + account are needed to construct ARNs for Subscribe.
	region  string
	account string
}

type Config struct {
	Region  string
	Account string
}

func New(snsClient *sns.Client, sqsClient *sqs.Client, cfg Config) *Backend {
	r := cfg.Region
	if r == "" {
		r = "us-east-1"
	}
	a := cfg.Account
	if a == "" {
		a = "000000000000"
	}
	return &Backend{sns: snsClient, sqs: sqsClient, region: r, account: a}
}

var _ domain.Pubsub = (*Backend)(nil)

func (b *Backend) topicArn(name string) string {
	return fmt.Sprintf("arn:aws:sns:%s:%s:%s", b.region, b.account, name)
}

func (b *Backend) queueArn(name string) string {
	return fmt.Sprintf("arn:aws:sqs:%s:%s:%s", b.region, b.account, name)
}

func (b *Backend) topicNameFromArn(arn string) string {
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

// ----------------------------------------------------------------------
// Topics
// ----------------------------------------------------------------------

func (b *Backend) CreateTopic(ctx context.Context, name string, opt domain.CreateTopicOptions) (domain.Topic, error) {
	if _, err := b.sns.CreateTopic(ctx, &sns.CreateTopicInput{
		Name: awsapi.String(name),
	}); err != nil {
		return domain.Topic{}, fmt.Errorf("sns CreateTopic: %w", err)
	}
	return domain.Topic{Name: name, CreatedAt: time.Now().UTC()}, nil
}

func (b *Backend) DeleteTopic(ctx context.Context, name string) error {
	_, err := b.sns.DeleteTopic(ctx, &sns.DeleteTopicInput{
		TopicArn: awsapi.String(b.topicArn(name)),
	})
	if err != nil {
		var nfe *snstypes.NotFoundException
		if errors.As(err, &nfe) {
			return domain.NoSuchTopic(name)
		}
		return fmt.Errorf("sns DeleteTopic: %w", err)
	}
	return nil
}

func (b *Backend) HeadTopic(ctx context.Context, name string) (domain.Topic, error) {
	_, err := b.sns.GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{
		TopicArn: awsapi.String(b.topicArn(name)),
	})
	if err != nil {
		var nfe *snstypes.NotFoundException
		if errors.As(err, &nfe) {
			return domain.Topic{}, domain.NoSuchTopic(name)
		}
		return domain.Topic{}, fmt.Errorf("sns GetTopicAttributes: %w", err)
	}
	return domain.Topic{Name: name}, nil
}

func (b *Backend) ListTopics(ctx context.Context, opt domain.ListTopicsOptions) (domain.ListTopicsResult, error) {
	out, err := b.sns.ListTopics(ctx, &sns.ListTopicsInput{})
	if err != nil {
		return domain.ListTopicsResult{}, fmt.Errorf("sns ListTopics: %w", err)
	}
	res := domain.ListTopicsResult{}
	for _, t := range out.Topics {
		name := b.topicNameFromArn(awsapi.ToString(t.TopicArn))
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
	// 1. Auto-create the backing SQS queue (name == sub name).
	if _, err := b.sqs.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: awsapi.String(sub),
	}); err != nil {
		var existing *sqstypes.QueueNameExists
		if !errors.As(err, &existing) {
			return domain.Subscription{}, fmt.Errorf("sqs CreateQueue: %w", err)
		}
	}
	// 2. SNS Subscribe with Protocol=sqs + Endpoint = the queue arn.
	if _, err := b.sns.Subscribe(ctx, &sns.SubscribeInput{
		TopicArn: awsapi.String(b.topicArn(topic)),
		Protocol: awsapi.String("sqs"),
		Endpoint: awsapi.String(b.queueArn(sub)),
	}); err != nil {
		var nfe *snstypes.NotFoundException
		if errors.As(err, &nfe) {
			return domain.Subscription{}, domain.NoSuchTopic(topic)
		}
		return domain.Subscription{}, fmt.Errorf("sns Subscribe: %w", err)
	}
	return domain.Subscription{
		Name:               sub,
		Topic:              topic,
		AckDeadlineSeconds: opt.AckDeadlineSeconds,
		Durable:            true,
		CreatedAt:          time.Now().UTC(),
	}, nil
}

func (b *Backend) DeleteSubscription(ctx context.Context, sub string) error {
	// Find the subscription ARN via ListSubscriptions (no other op
	// looks it up by name). Then unsubscribe + delete the backing
	// queue.
	out, err := b.sns.ListSubscriptions(ctx, &sns.ListSubscriptionsInput{})
	if err != nil {
		return fmt.Errorf("sns ListSubscriptions: %w", err)
	}
	queueARN := b.queueArn(sub)
	var subArn string
	for _, s := range out.Subscriptions {
		if awsapi.ToString(s.Endpoint) == queueARN {
			subArn = awsapi.ToString(s.SubscriptionArn)
			break
		}
	}
	if subArn != "" {
		if _, err := b.sns.Unsubscribe(ctx, &sns.UnsubscribeInput{
			SubscriptionArn: awsapi.String(subArn),
		}); err != nil {
			return fmt.Errorf("sns Unsubscribe: %w", err)
		}
	}
	// Resolve the queue URL and delete.
	qu, err := b.sqs.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{
		QueueName: awsapi.String(sub),
	})
	if err != nil {
		var dne *sqstypes.QueueDoesNotExist
		if errors.As(err, &dne) {
			return domain.NoSuchSubscription(sub)
		}
		return fmt.Errorf("sqs GetQueueUrl: %w", err)
	}
	if _, err := b.sqs.DeleteQueue(ctx, &sqs.DeleteQueueInput{
		QueueUrl: qu.QueueUrl,
	}); err != nil {
		return fmt.Errorf("sqs DeleteQueue: %w", err)
	}
	return nil
}

func (b *Backend) HeadSubscription(ctx context.Context, sub string) (domain.Subscription, error) {
	// The lookup requires iterating SNS subscriptions until one
	// matches the backing-queue ARN.
	out, err := b.sns.ListSubscriptions(ctx, &sns.ListSubscriptionsInput{})
	if err != nil {
		return domain.Subscription{}, fmt.Errorf("sns ListSubscriptions: %w", err)
	}
	queueARN := b.queueArn(sub)
	for _, s := range out.Subscriptions {
		if awsapi.ToString(s.Endpoint) == queueARN {
			return domain.Subscription{
				Name:    sub,
				Topic:   b.topicNameFromArn(awsapi.ToString(s.TopicArn)),
				Durable: true,
			}, nil
		}
	}
	return domain.Subscription{}, domain.NoSuchSubscription(sub)
}

func (b *Backend) ListSubscriptions(ctx context.Context, opt domain.ListSubscriptionsOptions) (domain.ListSubscriptionsResult, error) {
	out, err := b.sns.ListSubscriptions(ctx, &sns.ListSubscriptionsInput{})
	if err != nil {
		return domain.ListSubscriptionsResult{}, fmt.Errorf("sns ListSubscriptions: %w", err)
	}
	res := domain.ListSubscriptionsResult{}
	for _, s := range out.Subscriptions {
		topic := b.topicNameFromArn(awsapi.ToString(s.TopicArn))
		if opt.Topic != "" && topic != opt.Topic {
			continue
		}
		sub := b.topicNameFromArn(awsapi.ToString(s.Endpoint))
		res.Subscriptions = append(res.Subscriptions, domain.Subscription{
			Name:    sub,
			Topic:   topic,
			Durable: true,
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
	in := &sns.PublishInput{
		TopicArn: awsapi.String(b.topicArn(topic)),
		Message:  awsapi.String(string(opt.Body)),
	}
	if len(opt.Attributes) > 0 {
		in.MessageAttributes = map[string]snstypes.MessageAttributeValue{}
		for k, v := range opt.Attributes {
			in.MessageAttributes[k] = snstypes.MessageAttributeValue{
				DataType:    awsapi.String("String"),
				StringValue: awsapi.String(v),
			}
		}
	}
	out, err := b.sns.Publish(ctx, in)
	if err != nil {
		var nfe *snstypes.NotFoundException
		if errors.As(err, &nfe) {
			return domain.PublishResult{}, domain.NoSuchTopic(topic)
		}
		return domain.PublishResult{}, fmt.Errorf("sns Publish: %w", err)
	}
	return domain.PublishResult{MessageID: awsapi.ToString(out.MessageId)}, nil
}

func (b *Backend) Receive(ctx context.Context, sub string, opt domain.ReceiveOptions) ([]domain.Message, error) {
	qu, err := b.sqs.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{
		QueueName: awsapi.String(sub),
	})
	if err != nil {
		var dne *sqstypes.QueueDoesNotExist
		if errors.As(err, &dne) {
			return nil, domain.NoSuchSubscription(sub)
		}
		return nil, fmt.Errorf("sqs GetQueueUrl: %w", err)
	}
	max := int32(opt.MaxMessages)
	if max <= 0 || max > 10 {
		max = 10
	}
	in := &sqs.ReceiveMessageInput{
		QueueUrl:            qu.QueueUrl,
		MaxNumberOfMessages: max,
		WaitTimeSeconds:     int32(opt.WaitTime),
		MessageAttributeNames: []string{"All"},
	}
	if opt.AckDeadline > 0 {
		in.VisibilityTimeout = int32(opt.AckDeadline)
	}
	out, err := b.sqs.ReceiveMessage(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("sqs ReceiveMessage: %w", err)
	}
	res := make([]domain.Message, 0, len(out.Messages))
	now := time.Now().UTC()
	for _, m := range out.Messages {
		attrs := map[string]string{}
		for k, v := range m.MessageAttributes {
			attrs[k] = awsapi.ToString(v.StringValue)
		}
		res = append(res, domain.Message{
			MessageID:     awsapi.ToString(m.MessageId),
			Body:          []byte(awsapi.ToString(m.Body)),
			Attributes:    attrs,
			ReceiptHandle: awsapi.ToString(m.ReceiptHandle),
			ReceivedAt:    now,
			DeliveryCount: 1,
		})
	}
	return res, nil
}

func (b *Backend) Ack(ctx context.Context, sub string, receiptHandle string) error {
	qu, err := b.sqs.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: awsapi.String(sub)})
	if err != nil {
		return fmt.Errorf("sqs GetQueueUrl: %w", err)
	}
	if _, err := b.sqs.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      qu.QueueUrl,
		ReceiptHandle: awsapi.String(receiptHandle),
	}); err != nil {
		return fmt.Errorf("sqs DeleteMessage: %w", err)
	}
	return nil
}

func (b *Backend) ChangeVisibility(ctx context.Context, sub string, receiptHandle string, visibilityTimeout int) error {
	qu, err := b.sqs.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: awsapi.String(sub)})
	if err != nil {
		return fmt.Errorf("sqs GetQueueUrl: %w", err)
	}
	if _, err := b.sqs.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          qu.QueueUrl,
		ReceiptHandle:     awsapi.String(receiptHandle),
		VisibilityTimeout: int32(visibilityTimeout),
	}); err != nil {
		return fmt.Errorf("sqs ChangeMessageVisibility: %w", err)
	}
	return nil
}
