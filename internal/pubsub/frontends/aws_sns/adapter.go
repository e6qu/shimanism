// Package aws_sns is the AWS SNS frontend for shimanism's pubsub
// service. Phase 11.8d migrated it from a hand-written awsQuery wire
// layer to spec-driven generated stubs.
package aws_sns

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/shimanism/internal/awsquery"
	"github.com/e6qu/shimanism/internal/pubsub/domain"
	gen "github.com/e6qu/shimanism/services/pubsub/gen"
)

// Adapter binds gen.SNSBackend to a domain.Pubsub backend.
type Adapter struct {
	s domain.Pubsub
}

// New returns the http.Handler dispatching through the generated
// awsQuery router into the adapter bound to the given backend.
func New(s domain.Pubsub) http.Handler {
	return gen.RegisterSNSRoutes(&Adapter{s: s})
}

const (
	snsRegion  = "us-east-1"
	snsAccount = "000000000000"
)

func strPtr(s string) *string { return &s }

func topicArn(name string) string {
	return fmt.Sprintf("arn:aws:sns:%s:%s:%s", snsRegion, snsAccount, name)
}

func subscriptionArn(topic, sub string) string {
	return fmt.Sprintf("arn:aws:sns:%s:%s:%s:%s", snsRegion, snsAccount, topic, sub)
}

func queueArn(queue string) string {
	return fmt.Sprintf("arn:aws:sqs:%s:%s:%s", snsRegion, snsAccount, queue)
}

// parseTopicArn extracts the topic name from an ARN-or-name. Real
// SNS ARNs are `arn:aws:sns:<region>:<acct>:<name>`.
func parseTopicArn(arn string) string {
	if arn == "" {
		return ""
	}
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return arn
	}
	return parts[5]
}

func lastArnSegment(arn string) string {
	if arn == "" {
		return ""
	}
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

func mapDomainErr(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		return &awsquery.BackendError{
			HTTPStatus: http.StatusInternalServerError,
			Type:       "Receiver",
			Code:       "InternalFailure",
			Message:    err.Error(),
		}
	}
	be := &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Message: de.Error()}
	switch de.Kind {
	case domain.KindNoSuchTopic, domain.KindNoSuchSubscription:
		be.HTTPStatus = http.StatusNotFound
		be.Code = "NotFound"
	case domain.KindTopicAlreadyExists, domain.KindSubscriptionAlreadyExists:
		be.Code = "InvalidParameter"
	case domain.KindInvalidArgument:
		be.Code = "InvalidParameter"
	default:
		be.HTTPStatus = http.StatusInternalServerError
		be.Type = "Receiver"
		be.Code = "InternalFailure"
	}
	return be
}

// ---------------------------------------------------------------------
// Topic ops.
// ---------------------------------------------------------------------

func (a *Adapter) CreateTopic(ctx context.Context, in *gen.CreateTopicInput) (*gen.CreateTopicResponse, error) {
	if in.Name == "" {
		return nil, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameter", Message: "Name is required"}
	}
	if _, err := a.s.CreateTopic(ctx, in.Name, domain.CreateTopicOptions{}); err != nil {
		var de *domain.Error
		if !errors.As(err, &de) || de.Kind != domain.KindTopicAlreadyExists {
			return nil, mapDomainErr(err)
		}
		// AWS SNS CreateTopic is idempotent — return the existing ARN.
	}
	return &gen.CreateTopicResponse{TopicArn: strPtr(topicArn(in.Name))}, nil
}

func (a *Adapter) DeleteTopic(ctx context.Context, in *gen.DeleteTopicInput) (struct{}, error) {
	name := parseTopicArn(in.TopicArn)
	if name == "" {
		return struct{}{}, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameter", Message: "TopicArn is required"}
	}
	if err := a.s.DeleteTopic(ctx, name); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) ListTopics(ctx context.Context, in *gen.ListTopicsInput) (*gen.ListTopicsResponse, error) {
	res, err := a.s.ListTopics(ctx, domain.ListTopicsOptions{})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.ListTopicsResponse{}
	for _, tp := range res.Topics {
		arn := topicArn(tp.Name)
		out.Topics.Member = append(out.Topics.Member, gen.Topic{TopicArn: &arn})
	}
	return out, nil
}

func (a *Adapter) GetTopicAttributes(ctx context.Context, in *gen.GetTopicAttributesInput) (*gen.GetTopicAttributesResponse, error) {
	name := parseTopicArn(in.TopicArn)
	if name == "" {
		return nil, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameter", Message: "TopicArn is required"}
	}
	t, err := a.s.HeadTopic(ctx, name)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	// SNS canonical "freshly created" attributes — what real SNS
	// returns for a topic that hasn't been configured beyond Create.
	attrs := gen.TopicAttributesMap{
		"TopicArn":              topicArn(t.Name),
		"DisplayName":           "",
		"SubscriptionsConfirmed": "0",
		"SubscriptionsPending":   "0",
		"SubscriptionsDeleted":   "0",
		"DeliveryPolicy":         "",
		"Policy":                 "",
	}
	return &gen.GetTopicAttributesResponse{Attributes: attrs}, nil
}

func (a *Adapter) SetTopicAttributes(ctx context.Context, in *gen.SetTopicAttributesInput) (struct{}, error) {
	name := parseTopicArn(in.TopicArn)
	if name == "" {
		return struct{}{}, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameter", Message: "TopicArn is required"}
	}
	if in.AttributeName == "" {
		return struct{}{}, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameter", Message: "AttributeName is required"}
	}
	// Validate the topic exists; SetTopicAttributes itself is a no-op
	// at the domain layer (the cross-cloud intersection doesn't carry
	// SNS-specific knobs like DeliveryPolicy / Policy).
	if _, err := a.s.HeadTopic(ctx, name); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	// Reject non-default values per the no-silent-fallback rule.
	if in.AttributeValue != nil && *in.AttributeValue != "" {
		return struct{}{}, &awsquery.BackendError{
			HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameter",
			Message: "AttributeValue is not supported in the cross-cloud intersection for " + in.AttributeName,
		}
	}
	return struct{}{}, nil
}

// ---------------------------------------------------------------------
// Subscription ops.
// ---------------------------------------------------------------------

func (a *Adapter) Subscribe(ctx context.Context, in *gen.SubscribeInput) (*gen.SubscribeResponse, error) {
	topic := parseTopicArn(in.TopicArn)
	if topic == "" || in.Protocol == "" || in.Endpoint == nil || *in.Endpoint == "" {
		return nil, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameter", Message: "TopicArn, Protocol, and Endpoint are required"}
	}
	if in.Protocol != "sqs" {
		return nil, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameter", Message: "only Protocol=sqs is in the pubsub intersection (got " + in.Protocol + ")"}
	}
	sub := lastArnSegment(*in.Endpoint)
	if sub == "" {
		return nil, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameter", Message: "Endpoint must be an SQS queue ARN with a name segment"}
	}
	if _, err := a.s.CreateSubscription(ctx, topic, sub, domain.CreateSubscriptionOptions{Durable: true}); err != nil {
		var de *domain.Error
		if !errors.As(err, &de) || de.Kind != domain.KindSubscriptionAlreadyExists {
			return nil, mapDomainErr(err)
		}
	}
	return &gen.SubscribeResponse{SubscriptionArn: strPtr(subscriptionArn(topic, sub))}, nil
}

func (a *Adapter) Unsubscribe(ctx context.Context, in *gen.UnsubscribeInput) (struct{}, error) {
	sub := lastArnSegment(in.SubscriptionArn)
	if sub == "" {
		return struct{}{}, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameter", Message: "SubscriptionArn is required"}
	}
	if err := a.s.DeleteSubscription(ctx, sub); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) ListSubscriptions(ctx context.Context, in *gen.ListSubscriptionsInput) (*gen.ListSubscriptionsResponse, error) {
	return a.listSubs(ctx, "")
}

func (a *Adapter) ListSubscriptionsByTopic(ctx context.Context, in *gen.ListSubscriptionsByTopicInput) (*gen.ListSubscriptionsByTopicResponse, error) {
	topic := parseTopicArn(in.TopicArn)
	res, err := a.listSubs(ctx, topic)
	if err != nil {
		return nil, err
	}
	// gen.ListSubscriptionsByTopicResponse is structurally identical
	// to gen.ListSubscriptionsResponse but a distinct type — copy
	// fields across.
	return &gen.ListSubscriptionsByTopicResponse{
		NextToken:     res.NextToken,
		Subscriptions: res.Subscriptions,
	}, nil
}

func (a *Adapter) listSubs(ctx context.Context, topic string) (*gen.ListSubscriptionsResponse, error) {
	res, err := a.s.ListSubscriptions(ctx, domain.ListSubscriptionsOptions{Topic: topic})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.ListSubscriptionsResponse{}
	for _, s := range res.Subscriptions {
		subARN := subscriptionArn(s.Topic, s.Name)
		tArn := topicArn(s.Topic)
		proto := "sqs"
		ep := queueArn(s.Name)
		out.Subscriptions.Member = append(out.Subscriptions.Member, gen.Subscription{
			SubscriptionArn: &subARN,
			TopicArn:        &tArn,
			Protocol:        &proto,
			Endpoint:        &ep,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------
// Publish.
// ---------------------------------------------------------------------

func (a *Adapter) Publish(ctx context.Context, in *gen.PublishInput) (*gen.PublishResponse, error) {
	if in.TopicArn == nil || *in.TopicArn == "" {
		return nil, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameter", Message: "TopicArn is required"}
	}
	topic := parseTopicArn(*in.TopicArn)

	// MessageAttributes is map<string, MessageAttributeValue>. The
	// awsQuery emitter doesn't yet decode map<string,struct>; pull
	// the raw form via awsquery.FormFromContext and parse here.
	attrs := messageAttributesFromContext(ctx)

	res, err := a.s.Publish(ctx, topic, domain.PublishOptions{
		Body:       []byte(in.Message),
		Attributes: attrs,
	})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.PublishResponse{MessageId: strPtr(res.MessageID)}, nil
}

// messageAttributesFromContext extracts SNS Publish's
// MessageAttributes map from the form on the request context.
// AWS serialises this as
//   MessageAttributes.entry.N.Name=key
//   MessageAttributes.entry.N.Value.DataType=String
//   MessageAttributes.entry.N.Value.StringValue=val
//
// The domain stores only StringValue.
func messageAttributesFromContext(ctx context.Context) map[string]string {
	form := awsquery.FormFromContext(ctx)
	if form == nil {
		return nil
	}
	out := map[string]string{}
	for i := 1; ; i++ {
		nameKey := "MessageAttributes.entry." + strconv.Itoa(i) + ".Name"
		name := form.Get(nameKey)
		if name == "" {
			break
		}
		valueKey := "MessageAttributes.entry." + strconv.Itoa(i) + ".Value.StringValue"
		out[name] = form.Get(valueKey)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------
// ListTagsForResource — Phase 9 probe shape (the hashicorp/aws SNS
// importer calls this on every Read). Pubsub domain has no tag
// concept yet (tracked alongside BUG-12 family); return empty.
// ---------------------------------------------------------------------

func (a *Adapter) ListTagsForResource(ctx context.Context, in *gen.ListTagsForResourceRequest) (*gen.ListTagsForResourceResponse, error) {
	name := parseTopicArn(in.ResourceArn)
	if name == "" {
		return nil, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameter", Message: "ResourceArn is required"}
	}
	if _, err := a.s.HeadTopic(ctx, name); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.ListTagsForResourceResponse{}, nil
}
