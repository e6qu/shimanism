// Package aws is the AWS SQS passthrough backend for shimanism's
// queue service. It uses aws-sdk-go-v2/service/sqs to drive real
// AWS SQS.
//
// Receipt handles are AWS's native ReceiptHandle strings — they
// pass through unchanged; the shim treats them as opaque.
// QueueUrls are resolved per-request via GetQueueUrl + cached
// inside a single domain call only (no persistent cache, per
// AGENTS.md § The shim is stateless).
package aws

import (
	"context"
	"errors"
	"strconv"
	"strings"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"

	"github.com/e6qu/shimanism/internal/queue/domain"
)

// Backend implements domain.Queues via real AWS SQS.
type Backend struct {
	c *sqs.Client
}

// New wraps a configured SQS client.
func New(c *sqs.Client) *Backend { return &Backend{c: c} }

var _ domain.Queues = (*Backend)(nil)

func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	var qne *sqstypes.QueueDoesNotExist
	if errors.As(err, &qne) {
		return domain.NoSuchQueue(name)
	}
	var qexists *sqstypes.QueueNameExists
	if errors.As(err, &qexists) {
		return domain.QueueAlreadyExists(name)
	}
	var rinv *sqstypes.ReceiptHandleIsInvalid
	if errors.As(err, &rinv) {
		return domain.InvalidReceiptHandle(awsapi.ToString(rinv.Message))
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "QueueDoesNotExist", "AWS.SimpleQueueService.NonExistentQueue":
			return domain.NoSuchQueue(name)
		case "QueueAlreadyExists", "AWS.SimpleQueueService.QueueNameExists":
			return domain.QueueAlreadyExists(name)
		case "InvalidParameterValue":
			return domain.InvalidArgument(ae.ErrorMessage())
		}
	}
	return err
}

func (b *Backend) queueURL(ctx context.Context, name string) (string, error) {
	out, err := b.c.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: awsapi.String(name)})
	if err != nil {
		return "", translateErr(err, name)
	}
	return awsapi.ToString(out.QueueUrl), nil
}

func (b *Backend) CreateQueue(ctx context.Context, name string, opt domain.CreateQueueOptions) (domain.Queue, error) {
	attrs := map[string]string{}
	if opt.Attributes.VisibilityTimeoutSeconds > 0 {
		attrs["VisibilityTimeout"] = strconv.Itoa(opt.Attributes.VisibilityTimeoutSeconds)
	}
	if opt.Attributes.MessageRetentionSeconds > 0 {
		attrs["MessageRetentionPeriod"] = strconv.Itoa(opt.Attributes.MessageRetentionSeconds)
	}
	if opt.Attributes.MaxMessageSizeBytes > 0 {
		attrs["MaximumMessageSize"] = strconv.Itoa(opt.Attributes.MaxMessageSizeBytes)
	}
	if opt.Attributes.DelaySeconds > 0 {
		attrs["DelaySeconds"] = strconv.Itoa(opt.Attributes.DelaySeconds)
	}
	_, err := b.c.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  awsapi.String(name),
		Attributes: attrs,
	})
	if err != nil {
		return domain.Queue{}, translateErr(err, name)
	}
	return domain.Queue{Name: name, Attributes: opt.Attributes}, nil
}

func (b *Backend) DeleteQueue(ctx context.Context, name string) error {
	url, err := b.queueURL(ctx, name)
	if err != nil {
		return err
	}
	_, err = b.c.DeleteQueue(ctx, &sqs.DeleteQueueInput{QueueUrl: awsapi.String(url)})
	return translateErr(err, name)
}

func (b *Backend) SetQueueAttributes(ctx context.Context, name string, attrs domain.QueueAttributes) error {
	url, err := b.queueURL(ctx, name)
	if err != nil {
		return err
	}
	a := map[string]string{}
	if attrs.VisibilityTimeoutSeconds > 0 {
		a["VisibilityTimeout"] = strconv.Itoa(attrs.VisibilityTimeoutSeconds)
	}
	if attrs.MessageRetentionSeconds > 0 {
		a["MessageRetentionPeriod"] = strconv.Itoa(attrs.MessageRetentionSeconds)
	}
	if attrs.MaxMessageSizeBytes > 0 {
		a["MaximumMessageSize"] = strconv.Itoa(attrs.MaxMessageSizeBytes)
	}
	if attrs.DelaySeconds > 0 {
		a["DelaySeconds"] = strconv.Itoa(attrs.DelaySeconds)
	}
	if len(a) == 0 {
		return nil
	}
	_, err = b.c.SetQueueAttributes(ctx, &sqs.SetQueueAttributesInput{
		QueueUrl:   awsapi.String(url),
		Attributes: a,
	})
	return translateErr(err, name)
}

func (b *Backend) HeadQueue(ctx context.Context, name string) (domain.Queue, error) {
	url, err := b.queueURL(ctx, name)
	if err != nil {
		return domain.Queue{}, err
	}
	out, err := b.c.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       awsapi.String(url),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	if err != nil {
		return domain.Queue{}, translateErr(err, name)
	}
	a := domain.QueueAttributes{}
	if v, ok := out.Attributes["VisibilityTimeout"]; ok {
		a.VisibilityTimeoutSeconds, _ = strconv.Atoi(v)
	}
	if v, ok := out.Attributes["MessageRetentionPeriod"]; ok {
		a.MessageRetentionSeconds, _ = strconv.Atoi(v)
	}
	if v, ok := out.Attributes["MaximumMessageSize"]; ok {
		a.MaxMessageSizeBytes, _ = strconv.Atoi(v)
	}
	if v, ok := out.Attributes["DelaySeconds"]; ok {
		a.DelaySeconds, _ = strconv.Atoi(v)
	}
	if v, ok := out.Attributes["ApproximateNumberOfMessages"]; ok {
		a.ApproximateMessageCount, _ = strconv.Atoi(v)
	}
	return domain.Queue{Name: name, Attributes: a}, nil
}

func (b *Backend) ListQueues(ctx context.Context, opt domain.ListQueuesOptions) (domain.ListQueuesResult, error) {
	in := &sqs.ListQueuesInput{}
	if opt.Prefix != "" {
		in.QueueNamePrefix = awsapi.String(opt.Prefix)
	}
	if opt.MaxResults > 0 {
		mr := int32(opt.MaxResults)
		in.MaxResults = &mr
	}
	if opt.NextToken != "" {
		in.NextToken = awsapi.String(opt.NextToken)
	}
	out, err := b.c.ListQueues(ctx, in)
	if err != nil {
		return domain.ListQueuesResult{}, translateErr(err, "")
	}
	res := domain.ListQueuesResult{NextToken: awsapi.ToString(out.NextToken)}
	for _, u := range out.QueueUrls {
		// Extract the last segment of the URL as the queue name.
		name := u
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		res.Queues = append(res.Queues, domain.Queue{Name: name})
	}
	return res, nil
}

func (b *Backend) SendMessage(ctx context.Context, queueName string, opt domain.SendMessageOptions) (domain.SendMessageResult, error) {
	url, err := b.queueURL(ctx, queueName)
	if err != nil {
		return domain.SendMessageResult{}, err
	}
	in := &sqs.SendMessageInput{
		QueueUrl:    awsapi.String(url),
		MessageBody: awsapi.String(string(opt.Body)),
	}
	if opt.DelaySeconds > 0 {
		ds := int32(opt.DelaySeconds)
		in.DelaySeconds = ds
	}
	if len(opt.Attributes) > 0 {
		in.MessageAttributes = map[string]sqstypes.MessageAttributeValue{}
		for k, v := range opt.Attributes {
			v := v
			in.MessageAttributes[k] = sqstypes.MessageAttributeValue{
				StringValue: awsapi.String(v),
				DataType:    awsapi.String("String"),
			}
		}
	}
	out, err := b.c.SendMessage(ctx, in)
	if err != nil {
		return domain.SendMessageResult{}, translateErr(err, queueName)
	}
	return domain.SendMessageResult{MessageID: awsapi.ToString(out.MessageId)}, nil
}

func (b *Backend) ReceiveMessages(ctx context.Context, queueName string, opt domain.ReceiveMessagesOptions) ([]domain.Message, error) {
	url, err := b.queueURL(ctx, queueName)
	if err != nil {
		return nil, err
	}
	in := &sqs.ReceiveMessageInput{
		QueueUrl:              awsapi.String(url),
		MaxNumberOfMessages:   int32(opt.MaxMessages),
		MessageAttributeNames: []string{"All"},
		AttributeNames:        []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	}
	if opt.VisibilityTimeout > 0 {
		in.VisibilityTimeout = int32(opt.VisibilityTimeout)
	}
	if opt.WaitTime > 0 {
		in.WaitTimeSeconds = int32(opt.WaitTime)
	}
	out, err := b.c.ReceiveMessage(ctx, in)
	if err != nil {
		return nil, translateErr(err, queueName)
	}
	res := make([]domain.Message, 0, len(out.Messages))
	for _, m := range out.Messages {
		attrs := map[string]string{}
		for k, v := range m.MessageAttributes {
			if v.StringValue != nil {
				attrs[k] = awsapi.ToString(v.StringValue)
			}
		}
		dc := 1
		if v, ok := m.Attributes["ApproximateReceiveCount"]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				dc = n
			}
		}
		res = append(res, domain.Message{
			MessageID:     awsapi.ToString(m.MessageId),
			Body:          []byte(awsapi.ToString(m.Body)),
			Attributes:    attrs,
			ReceiptHandle: awsapi.ToString(m.ReceiptHandle),
			DeliveryCount: dc,
		})
	}
	return res, nil
}

func (b *Backend) DeleteMessage(ctx context.Context, queueName string, receiptHandle string) error {
	url, err := b.queueURL(ctx, queueName)
	if err != nil {
		return err
	}
	_, err = b.c.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      awsapi.String(url),
		ReceiptHandle: awsapi.String(receiptHandle),
	})
	return translateErr(err, queueName)
}

func (b *Backend) ChangeVisibility(ctx context.Context, queueName string, receiptHandle string, visibilityTimeout int) error {
	url, err := b.queueURL(ctx, queueName)
	if err != nil {
		return err
	}
	_, err = b.c.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          awsapi.String(url),
		ReceiptHandle:     awsapi.String(receiptHandle),
		VisibilityTimeout: int32(visibilityTimeout),
	})
	return translateErr(err, queueName)
}
