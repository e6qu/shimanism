// Package aws_sqs is the AWS SQS frontend for shimanism's queue
// service. Phase 11.7a migrated it from a hand-written awsJson1_0
// wire layer to a spec-driven path: wire decode + dispatch + encode
// is generated into services/queue/gen/aws_sqs.gen.go. This package
// is the adapter — it implements gen.SQSBackend by translating
// per-op requests into the neutral domain.Queues layer.
package aws_sqs

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/shimanism/internal/awsjson"
	"github.com/e6qu/shimanism/internal/queue/domain"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
	gen "github.com/e6qu/shimanism/services/queue/gen"
)

// Adapter binds gen.SQSBackend to a domain.Queues backend.
type Adapter struct {
	s domain.Queues
}

// New returns the http.Handler dispatching through the generated
// awsJson1_0 router into the adapter bound to the given backend.
// SigV4 verification is wired in; SHIMANISM_TEST_UNAUTHENTICATED=1
// short-circuits during the conformance-lane rewrite (set by the
// harness's init()).
func New(s domain.Queues) http.Handler {
	verifier := sigv4verifier.New(sigv4verifier.StaticStore{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, sigv4verifier.Options{Service: "sqs", Region: "us-east-1"})
	mw := sigv4verifier.Middleware(verifier, awsjson.WriteError)
	return mw(gen.RegisterSQSRoutes(&Adapter{s: s}))
}

// ---------------------------------------------------------------------
// Helpers — QueueUrl forge / normalise, attribute round-trip, errors.
// ---------------------------------------------------------------------

func fakeQueueURL(name string) string {
	return "https://sqs.shim.amazonaws.com/000000000000/" + name
}

func normaliseQueueURL(url string) string {
	if !strings.Contains(url, "/") {
		return url
	}
	if i := strings.LastIndexByte(url, '/'); i >= 0 {
		return url[i+1:]
	}
	return url
}

func strPtr(s string) *string { return &s }

// legacyQueryErrorCode maps Smithy error short names to the legacy
// awsQuery codes that SQS clients (including hashicorp/aws's
// per-error waiters) match against the `x-amzn-query-error` header.
var legacyQueryErrorCode = map[string]string{
	"QueueDoesNotExist":      "AWS.SimpleQueueService.NonExistentQueue",
	"QueueNameExists":        "QueueAlreadyExists",
	"ReceiptHandleIsInvalid": "ReceiptHandleIsInvalid",
	"InvalidMessageContents": "InvalidMessageContents",
	"OverLimit":              "OverLimit",
	"UnsupportedOperation":   "AWS.SimpleQueueService.UnsupportedOperation",
}

func mapDomainErr(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		return &awsjson.BackendError{
			HTTPStatus: http.StatusInternalServerError,
			Type:       "InternalFailure",
			Message:    err.Error(),
		}
	}
	be := &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Message: de.Error()}
	switch de.Kind {
	case domain.KindNoSuchQueue:
		be.Type = "QueueDoesNotExist"
	case domain.KindQueueAlreadyExists:
		be.Type = "QueueNameExists"
	case domain.KindInvalidReceiptHandle:
		be.Type = "ReceiptHandleIsInvalid"
	case domain.KindMessageTooLarge:
		be.Type = "InvalidMessageContents"
	case domain.KindInvalidArgument:
		be.Type = "InvalidParameterValue"
	default:
		be.HTTPStatus = http.StatusInternalServerError
		be.Type = "InternalFailure"
	}
	if legacy, ok := legacyQueryErrorCode[be.Type]; ok {
		be.QueryCompatibleCode = legacy
	}
	return be
}

func attributesFromAWS(attrs gen.QueueAttributeMap) domain.QueueAttributes {
	out := domain.QueueAttributes{}
	if v, ok := attrs["VisibilityTimeout"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			out.VisibilityTimeoutSeconds = n
		}
	}
	if v, ok := attrs["MessageRetentionPeriod"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			out.MessageRetentionSeconds = n
		}
	}
	if v, ok := attrs["MaximumMessageSize"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			out.MaxMessageSizeBytes = n
		}
	}
	if v, ok := attrs["DelaySeconds"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			out.DelaySeconds = n
		}
	}
	return out
}

func attributesToAWS(a domain.QueueAttributes) gen.QueueAttributeMap {
	return gen.QueueAttributeMap{
		"VisibilityTimeout":                     strconv.Itoa(a.VisibilityTimeoutSeconds),
		"MessageRetentionPeriod":                strconv.Itoa(a.MessageRetentionSeconds),
		"MaximumMessageSize":                    strconv.Itoa(a.MaxMessageSizeBytes),
		"DelaySeconds":                          strconv.Itoa(a.DelaySeconds),
		"ApproximateNumberOfMessages":           strconv.Itoa(a.ApproximateMessageCount),
		"ApproximateNumberOfMessagesNotVisible": "0",
		"ApproximateNumberOfMessagesDelayed":    "0",
		"CreatedTimestamp":                      strconv.FormatInt(a.CreatedAt.Unix(), 10),
		"LastModifiedTimestamp":                 strconv.FormatInt(a.CreatedAt.Unix(), 10),
		"ReceiveMessageWaitTimeSeconds":         "0",
		"Policy":                                "",
		"RedrivePolicy":                         "",
		"RedriveAllowPolicy":                    "",
		"KmsMasterKeyId":                        "",
		"KmsDataKeyReusePeriodSeconds":          "300",
		"SqsManagedSseEnabled":                  "false",
		"FifoQueue":                             "false",
		"ContentBasedDeduplication":             "false",
		"DeduplicationScope":                    "",
		"FifoThroughputLimit":                   "",
	}
}

func messageAttrsFromAWS(in gen.MessageBodyAttributeMap) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if v != nil && v.StringValue != nil {
			out[k] = *v.StringValue
		}
	}
	return out
}

func messageAttrsToAWS(in map[string]string) gen.MessageBodyAttributeMap {
	if len(in) == 0 {
		return nil
	}
	out := make(gen.MessageBodyAttributeMap, len(in))
	for k, v := range in {
		vCopy := v
		dt := "String"
		out[k] = &gen.MessageAttributeValue{StringValue: &vCopy, DataType: dt}
	}
	return out
}

func sqsSystemAttributes(m domain.Message) gen.MessageSystemAttributeMap {
	out := gen.MessageSystemAttributeMap{}
	if m.DeliveryCount > 0 {
		out["ApproximateReceiveCount"] = strconv.Itoa(m.DeliveryCount)
	}
	if !m.ReceivedAt.IsZero() {
		out["ApproximateFirstReceiveTimestamp"] = strconv.FormatInt(m.ReceivedAt.UnixMilli(), 10)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------
// Per-operation methods.
// ---------------------------------------------------------------------

func (a *Adapter) CreateQueue(ctx context.Context, in *gen.CreateQueueRequest) (*gen.CreateQueueResult, error) {
	if in.QueueName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "InvalidParameterValue", Message: "QueueName is required"}
	}
	opt := domain.CreateQueueOptions{Attributes: attributesFromAWS(in.Attributes)}
	if _, err := a.s.CreateQueue(ctx, in.QueueName, opt); err != nil {
		return nil, mapDomainErr(err)
	}
	if len(in.Tags) > 0 {
		if err := a.s.TagQueue(ctx, in.QueueName, in.Tags); err != nil {
			_ = a.s.DeleteQueue(ctx, in.QueueName)
			return nil, mapDomainErr(err)
		}
	}
	return &gen.CreateQueueResult{QueueUrl: strPtr(fakeQueueURL(in.QueueName))}, nil
}

func (a *Adapter) DeleteQueue(ctx context.Context, in *gen.DeleteQueueRequest) (struct{}, error) {
	name := normaliseQueueURL(in.QueueUrl)
	if name == "" {
		return struct{}{}, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "InvalidParameterValue", Message: "QueueUrl is required"}
	}
	if err := a.s.DeleteQueue(ctx, name); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) ListQueues(ctx context.Context, in *gen.ListQueuesRequest) (*gen.ListQueuesResult, error) {
	opt := domain.ListQueuesOptions{}
	if in.QueueNamePrefix != nil {
		opt.Prefix = *in.QueueNamePrefix
	}
	if in.NextToken != nil {
		opt.NextToken = *in.NextToken
	}
	if in.MaxResults != nil {
		opt.MaxResults = int(*in.MaxResults)
	}
	res, err := a.s.ListQueues(ctx, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.ListQueuesResult{}
	if res.NextToken != "" {
		out.NextToken = strPtr(res.NextToken)
	}
	for _, q := range res.Queues {
		out.QueueUrls = append(out.QueueUrls, fakeQueueURL(q.Name))
	}
	return out, nil
}

func (a *Adapter) GetQueueUrl(ctx context.Context, in *gen.GetQueueUrlRequest) (*gen.GetQueueUrlResult, error) {
	if in.QueueName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "InvalidParameterValue", Message: "QueueName is required"}
	}
	if _, err := a.s.HeadQueue(ctx, in.QueueName); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.GetQueueUrlResult{QueueUrl: strPtr(fakeQueueURL(in.QueueName))}, nil
}

func (a *Adapter) GetQueueAttributes(ctx context.Context, in *gen.GetQueueAttributesRequest) (*gen.GetQueueAttributesResult, error) {
	name := normaliseQueueURL(in.QueueUrl)
	q, err := a.s.HeadQueue(ctx, name)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.GetQueueAttributesResult{Attributes: attributesToAWS(q.Attributes)}, nil
}

func (a *Adapter) SetQueueAttributes(ctx context.Context, in *gen.SetQueueAttributesRequest) (struct{}, error) {
	if in.QueueUrl == "" {
		return struct{}{}, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "InvalidParameterValue", Message: "QueueUrl is required"}
	}
	name := normaliseQueueURL(in.QueueUrl)
	if err := a.s.SetQueueAttributes(ctx, name, attributesFromAWS(in.Attributes)); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) SendMessage(ctx context.Context, in *gen.SendMessageRequest) (*gen.SendMessageResult, error) {
	name := normaliseQueueURL(in.QueueUrl)
	opt := domain.SendMessageOptions{
		Body:       []byte(in.MessageBody),
		Attributes: messageAttrsFromAWS(in.MessageAttributes),
	}
	if in.DelaySeconds != nil {
		opt.DelaySeconds = int(*in.DelaySeconds)
	}
	res, err := a.s.SendMessage(ctx, name, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.SendMessageResult{MessageId: strPtr(res.MessageID)}, nil
}

func (a *Adapter) ReceiveMessage(ctx context.Context, in *gen.ReceiveMessageRequest) (*gen.ReceiveMessageResult, error) {
	name := normaliseQueueURL(in.QueueUrl)
	opt := domain.ReceiveMessagesOptions{}
	if in.MaxNumberOfMessages != nil {
		opt.MaxMessages = int(*in.MaxNumberOfMessages)
	}
	if in.VisibilityTimeout != nil {
		opt.VisibilityTimeout = int(*in.VisibilityTimeout)
	}
	if in.WaitTimeSeconds != nil {
		opt.WaitTime = int(*in.WaitTimeSeconds)
	}
	msgs, err := a.s.ReceiveMessages(ctx, name, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.ReceiveMessageResult{Messages: make(gen.MessageList, 0, len(msgs))}
	for _, m := range msgs {
		body := string(m.Body)
		out.Messages = append(out.Messages, gen.Message{
			MessageId:         strPtr(m.MessageID),
			ReceiptHandle:     strPtr(m.ReceiptHandle),
			Body:              &body,
			Attributes:        sqsSystemAttributes(m),
			MessageAttributes: messageAttrsToAWS(m.Attributes),
		})
	}
	return out, nil
}

func (a *Adapter) DeleteMessage(ctx context.Context, in *gen.DeleteMessageRequest) (struct{}, error) {
	name := normaliseQueueURL(in.QueueUrl)
	if err := a.s.DeleteMessage(ctx, name, in.ReceiptHandle); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) ChangeMessageVisibility(ctx context.Context, in *gen.ChangeMessageVisibilityRequest) (struct{}, error) {
	name := normaliseQueueURL(in.QueueUrl)
	if err := a.s.ChangeVisibility(ctx, name, in.ReceiptHandle, int(in.VisibilityTimeout)); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) ListQueueTags(ctx context.Context, in *gen.ListQueueTagsRequest) (*gen.ListQueueTagsResult, error) {
	tags, err := a.s.ListQueueTags(ctx, normaliseQueueURL(in.QueueUrl))
	if err != nil {
		return nil, mapDomainErr(err)
	}
	if tags == nil {
		tags = map[string]string{}
	}
	return &gen.ListQueueTagsResult{Tags: tags}, nil
}

func (a *Adapter) TagQueue(ctx context.Context, in *gen.TagQueueRequest) (struct{}, error) {
	if err := a.s.TagQueue(ctx, normaliseQueueURL(in.QueueUrl), in.Tags); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) UntagQueue(ctx context.Context, in *gen.UntagQueueRequest) (struct{}, error) {
	if err := a.s.UntagQueue(ctx, normaliseQueueURL(in.QueueUrl), in.TagKeys); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) PurgeQueue(ctx context.Context, in *gen.PurgeQueueRequest) (struct{}, error) {
	// PurgeQueue is out of the cross-cloud intersection (no GCP / Azure /
	// NATS analog with the same semantics). Reject honestly with the
	// source cloud's error vocabulary.
	return struct{}{}, &awsjson.BackendError{
		HTTPStatus: http.StatusBadRequest,
		Type:       "UnsupportedOperation",
		Message:    "PurgeQueue is out of the cross-cloud intersection",
	}
}
