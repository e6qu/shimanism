// Package aws_sns is the AWS SNS-shaped HTTP frontend for
// shimanism's pubsub service.
//
// SNS uses the awsQuery wire protocol (form-encoded request bodies,
// XML response envelopes wrapped in `<{Op}Response><{Op}Result>...</></><ResponseMetadata>...</></>`).
// This frontend dispatches on the `Action=` parameter, translates
// each request into a call on the neutral pubsub.Pubsub interface,
// and serialises the response as the appropriate XML shape.
//
// **Receive side is delegated.** SNS subscriptions in this phase
// always use Protocol=sqs, so the data plane is exposed via the
// sibling aws_sqs package (sqs-shaped receive against pubsub
// subscriptions). The Subscribe handler treats the Endpoint URL's
// last segment as the subscription name.
package aws_sns

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/shimanism/internal/pubsub/domain"
)

// Server is an AWS-SNS-shaped HTTP frontend.
type Server struct {
	s domain.Pubsub
}

func New(s domain.Pubsub) *Server { return &Server{s: s} }

const (
	snsNamespace = "http://sns.amazonaws.com/doc/2010-03-31/"
	snsAccount   = "000000000000"
	snsRegion    = "us-east-1"
)

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Sender", "MethodNotAllowed",
			"only POST is allowed on the SNS endpoint")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Sender", "MalformedInput",
			"read body: "+err.Error())
		return
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Sender", "MalformedInput",
			"parse query: "+err.Error())
		return
	}
	action := form.Get("Action")
	switch action {
	case "CreateTopic":
		srv.createTopic(w, r, form)
	case "DeleteTopic":
		srv.deleteTopic(w, r, form)
	case "ListTopics":
		srv.listTopics(w, r, form)
	case "GetTopicAttributes":
		srv.getTopicAttributes(w, r, form)
	case "Subscribe":
		srv.subscribe(w, r, form)
	case "Unsubscribe":
		srv.unsubscribe(w, r, form)
	case "ListSubscriptions":
		srv.listSubscriptions(w, r, form, "")
	case "ListSubscriptionsByTopic":
		srv.listSubscriptions(w, r, form, parseTopicArn(form.Get("TopicArn")))
	case "Publish":
		srv.publish(w, r, form)
	case "ListTagsForResource":
		srv.listTagsForResource(w, r, form)
	default:
		writeError(w, http.StatusBadRequest, "Sender", "InvalidAction",
			"unknown or out-of-intersection action: "+action)
	}
}

// ----------------------------------------------------------------------
// Topic handlers
// ----------------------------------------------------------------------

func (srv *Server) createTopic(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := form.Get("Name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameter",
			"Name is required")
		return
	}
	if _, err := srv.s.CreateTopic(r.Context(), name, domain.CreateTopicOptions{}); err != nil {
		var de *domain.Error
		if errorsAs(err, &de) && de.Kind == domain.KindTopicAlreadyExists {
			// AWS SNS CreateTopic is idempotent — returns the existing ARN.
		} else {
			mapDomainError(w, err)
			return
		}
	}
	writeXML(w, http.StatusOK, "CreateTopic", map[string]string{
		"TopicArn": topicArn(name),
	})
}

func (srv *Server) deleteTopic(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := parseTopicArn(form.Get("TopicArn"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameter",
			"TopicArn is required")
		return
	}
	if err := srv.s.DeleteTopic(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeXML(w, http.StatusOK, "DeleteTopic", nil)
}

func (srv *Server) listTopics(w http.ResponseWriter, r *http.Request, form url.Values) {
	res, err := srv.s.ListTopics(r.Context(), domain.ListTopicsOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	type member struct {
		XMLName  xml.Name `xml:"member"`
		TopicArn string   `xml:"TopicArn"`
	}
	type topics struct {
		Members []member `xml:"member"`
	}
	out := topics{}
	for _, t := range res.Topics {
		out.Members = append(out.Members, member{TopicArn: topicArn(t.Name)})
	}
	writeXMLStruct(w, http.StatusOK, "ListTopics", struct {
		XMLName xml.Name `xml:"Topics"`
		Topics  topics
	}{Topics: out})
}

func (srv *Server) getTopicAttributes(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := parseTopicArn(form.Get("TopicArn"))
	if _, err := srv.s.HeadTopic(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	type entry struct {
		XMLName xml.Name `xml:"entry"`
		Key     string   `xml:"key"`
		Value   string   `xml:"value"`
	}
	type attrs struct {
		XMLName xml.Name `xml:"Attributes"`
		Entries []entry  `xml:"entry"`
	}
	// Provider Read paths parse Policy + EffectiveDeliveryPolicy as
	// JSON; an empty string triggers 'unexpected end of JSON input'.
	// Real SNS returns canonical-default JSON envelopes when no
	// custom policy is configured (category 2: feature unset →
	// source cloud's actual default).
	defaultPolicy := fmt.Sprintf(`{"Version":"2012-10-17","Id":"__default_policy_ID","Statement":[{"Sid":"__default_statement_ID","Effect":"Allow","Principal":{"AWS":"*"},"Action":"SNS:GetTopicAttributes","Resource":"%s"}]}`, topicArn(name))
	defaultDeliveryPolicy := `{"http":{"defaultHealthyRetryPolicy":{"minDelayTarget":20,"maxDelayTarget":20,"numRetries":3,"numMaxDelayRetries":0,"numNoDelayRetries":0,"numMinDelayRetries":0,"backoffFunction":"linear"},"disableSubscriptionOverrides":false}}`
	// DisplayName is a category-2 attribute — empty when not
	// explicitly set. Real SNS returns the empty string, not the
	// topic name; the Terraform provider treats a non-empty
	// DisplayName as user intent and proposes diffs.
	writeXMLStruct(w, http.StatusOK, "GetTopicAttributes", attrs{Entries: []entry{
		{Key: "TopicArn", Value: topicArn(name)},
		{Key: "DisplayName", Value: ""},
		{Key: "Owner", Value: snsAccount},
		{Key: "Policy", Value: defaultPolicy},
		{Key: "EffectiveDeliveryPolicy", Value: defaultDeliveryPolicy},
		{Key: "SubscriptionsConfirmed", Value: "0"},
		{Key: "SubscriptionsPending", Value: "0"},
		{Key: "SubscriptionsDeleted", Value: "0"},
	}})
}

// listTagsForResource returns the (currently empty) tag set for an
// SNS topic. The pubsub domain has no tag concept yet — tag storage
// is tracked alongside BUG-12 (queue tags). Category-2 honest empty
// response: the topic has no tags configured. Without this handler,
// hashicorp/aws aws_sns_topic import crashes on
// InvalidAction: ListTagsForResource.
func (srv *Server) listTagsForResource(w http.ResponseWriter, r *http.Request, form url.Values) {
	arn := form.Get("ResourceArn")
	name := parseTopicArn(arn)
	if name == "" {
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameter",
			"ResourceArn is required")
		return
	}
	if _, err := srv.s.HeadTopic(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	type tags struct {
		XMLName xml.Name `xml:"Tags"`
	}
	writeXMLStruct(w, http.StatusOK, "ListTagsForResource", tags{})
}

// ----------------------------------------------------------------------
// Subscription handlers
// ----------------------------------------------------------------------

func (srv *Server) subscribe(w http.ResponseWriter, r *http.Request, form url.Values) {
	topic := parseTopicArn(form.Get("TopicArn"))
	protocol := form.Get("Protocol")
	endpoint := form.Get("Endpoint")
	if topic == "" || protocol == "" || endpoint == "" {
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameter",
			"TopicArn, Protocol, and Endpoint are required")
		return
	}
	if protocol != "sqs" {
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameter",
			"only Protocol=sqs is in the pubsub intersection (got "+protocol+")")
		return
	}
	sub := lastArnSegment(endpoint)
	if sub == "" {
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameter",
			"Endpoint must be an SQS queue ARN with a name segment")
		return
	}
	if _, err := srv.s.CreateSubscription(r.Context(), topic, sub, domain.CreateSubscriptionOptions{
		Durable: true,
	}); err != nil {
		var de *domain.Error
		if errorsAs(err, &de) && de.Kind == domain.KindSubscriptionAlreadyExists {
			// idempotent
		} else {
			mapDomainError(w, err)
			return
		}
	}
	writeXML(w, http.StatusOK, "Subscribe", map[string]string{
		"SubscriptionArn": subscriptionArn(topic, sub),
	})
}

func (srv *Server) unsubscribe(w http.ResponseWriter, r *http.Request, form url.Values) {
	subArn := form.Get("SubscriptionArn")
	sub := lastArnSegment(subArn)
	if sub == "" {
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameter",
			"SubscriptionArn is required")
		return
	}
	if err := srv.s.DeleteSubscription(r.Context(), sub); err != nil {
		mapDomainError(w, err)
		return
	}
	writeXML(w, http.StatusOK, "Unsubscribe", nil)
}

func (srv *Server) listSubscriptions(w http.ResponseWriter, r *http.Request, form url.Values, topic string) {
	opt := domain.ListSubscriptionsOptions{Topic: topic}
	res, err := srv.s.ListSubscriptions(r.Context(), opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	type member struct {
		XMLName         xml.Name `xml:"member"`
		SubscriptionArn string   `xml:"SubscriptionArn"`
		TopicArn        string   `xml:"TopicArn"`
		Protocol        string   `xml:"Protocol"`
		Endpoint        string   `xml:"Endpoint"`
	}
	type subs struct {
		Members []member `xml:"member"`
	}
	out := subs{}
	for _, s := range res.Subscriptions {
		out.Members = append(out.Members, member{
			SubscriptionArn: subscriptionArn(s.Topic, s.Name),
			TopicArn:        topicArn(s.Topic),
			Protocol:        "sqs",
			Endpoint:        queueArn(s.Name),
		})
	}
	root := "ListSubscriptions"
	if topic != "" {
		root = "ListSubscriptionsByTopic"
	}
	writeXMLStruct(w, http.StatusOK, root, struct {
		XMLName       xml.Name `xml:"Subscriptions"`
		Subscriptions subs
	}{Subscriptions: out})
}

// ----------------------------------------------------------------------
// Publish handler
// ----------------------------------------------------------------------

func (srv *Server) publish(w http.ResponseWriter, r *http.Request, form url.Values) {
	topic := parseTopicArn(form.Get("TopicArn"))
	if topic == "" {
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameter",
			"TopicArn is required")
		return
	}
	message := form.Get("Message")
	attrs := extractMessageAttributes(form)
	res, err := srv.s.Publish(r.Context(), topic, domain.PublishOptions{
		Body:       []byte(message),
		Attributes: attrs,
	})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeXML(w, http.StatusOK, "Publish", map[string]string{
		"MessageId": res.MessageID,
	})
}

func extractMessageAttributes(form url.Values) map[string]string {
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

// ----------------------------------------------------------------------
// ARN helpers
// ----------------------------------------------------------------------

func topicArn(name string) string {
	return fmt.Sprintf("arn:aws:sns:%s:%s:%s", snsRegion, snsAccount, name)
}

func subscriptionArn(topic, sub string) string {
	return fmt.Sprintf("arn:aws:sns:%s:%s:%s:%s", snsRegion, snsAccount, topic, sub)
}

func queueArn(queue string) string {
	return fmt.Sprintf("arn:aws:sqs:%s:%s:%s", snsRegion, snsAccount, queue)
}

func parseTopicArn(arn string) string {
	// arn:aws:sns:<region>:<acct>:<name>
	if arn == "" {
		return ""
	}
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		// Maybe it's just a name.
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
