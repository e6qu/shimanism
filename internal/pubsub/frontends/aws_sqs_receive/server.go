// Package aws_sqs_receive is the SQS-shaped receive surface for
// shimanism's pubsub service. It exposes the subset of the AWS SQS
// wire protocol needed to drive the receive side of an SNS+SQS
// fanout flow against pubsub subscriptions: ReceiveMessage,
// DeleteMessage, ChangeMessageVisibility, GetQueueAttributes,
// DeleteQueue.
//
// Each SQS "queue" in this surface maps 1:1 to a pubsub
// Subscription. The QueueUrl's last path segment is the
// subscription name; the AWS SNS frontend's Subscribe handler
// derives the same name from its Endpoint ARN, so client + shim
// agree on the identifier without shim-side state.
//
// Ops outside this slim subset (CreateQueue, SendMessage,
// ListQueues, etc.) return UnknownOperationException — they don't
// belong on a fanout-only data plane.
package aws_sqs_receive

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/e6qu/shimanism/internal/pubsub/domain"
)

type Server struct {
	s domain.Pubsub
}

func New(s domain.Pubsub) *Server { return &Server{s: s} }

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequest",
			r.Method+" not allowed; SQS uses POST")
		return
	}
	target := r.Header.Get("X-Amz-Target")
	op := target
	if i := strings.IndexByte(target, '.'); i >= 0 {
		op = target[i+1:]
	}
	switch op {
	case "ReceiveMessage":
		srv.receiveMessage(w, r)
	case "DeleteMessage":
		srv.deleteMessage(w, r)
	case "ChangeMessageVisibility":
		srv.changeMessageVisibility(w, r)
	case "GetQueueAttributes":
		srv.getQueueAttributes(w, r)
	case "DeleteQueue":
		srv.deleteQueue(w, r)
	case "GetQueueUrl":
		srv.getQueueUrl(w, r)
	default:
		writeError(w, http.StatusBadRequest, "UnknownOperationException",
			"operation "+op+" is not supported on the pubsub fanout data plane (use SNS Publish for sends)")
	}
}

type receiveMessageRequest struct {
	QueueUrl            string `json:"QueueUrl"`
	MaxNumberOfMessages int32  `json:"MaxNumberOfMessages,omitempty"`
	WaitTimeSeconds     int32  `json:"WaitTimeSeconds,omitempty"`
	VisibilityTimeout   *int32 `json:"VisibilityTimeout,omitempty"`
}

type receiveMessageResponse struct {
	Messages []messageOut `json:"Messages,omitempty"`
}

type messageOut struct {
	MessageId         string                           `json:"MessageId"`
	Body              string                           `json:"Body"`
	ReceiptHandle     string                           `json:"ReceiptHandle"`
	MD5OfBody         string                           `json:"MD5OfBody,omitempty"`
	MessageAttributes map[string]messageAttributeValue `json:"MessageAttributes,omitempty"`
}

type messageAttributeValue struct {
	StringValue string `json:"StringValue,omitempty"`
	DataType    string `json:"DataType,omitempty"`
}

func (srv *Server) receiveMessage(w http.ResponseWriter, r *http.Request) {
	var in receiveMessageRequest
	if !decode(w, r, &in) {
		return
	}
	sub := normaliseQueueURL(in.QueueUrl)
	opt := domain.ReceiveOptions{
		MaxMessages: int(in.MaxNumberOfMessages),
		WaitTime:    int(in.WaitTimeSeconds),
	}
	if in.VisibilityTimeout != nil {
		opt.AckDeadline = int(*in.VisibilityTimeout)
	}
	msgs, err := srv.s.Receive(r.Context(), sub, opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := receiveMessageResponse{}
	for _, m := range msgs {
		out := messageOut{
			MessageId:     m.MessageID,
			Body:          string(m.Body),
			ReceiptHandle: m.ReceiptHandle,
		}
		if len(m.Attributes) > 0 {
			out.MessageAttributes = map[string]messageAttributeValue{}
			for k, v := range m.Attributes {
				out.MessageAttributes[k] = messageAttributeValue{
					StringValue: v,
					DataType:    "String",
				}
			}
		}
		resp.Messages = append(resp.Messages, out)
	}
	writeJSON(w, http.StatusOK, &resp)
}

type deleteMessageRequest struct {
	QueueUrl      string `json:"QueueUrl"`
	ReceiptHandle string `json:"ReceiptHandle"`
}

func (srv *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	var in deleteMessageRequest
	if !decode(w, r, &in) {
		return
	}
	sub := normaliseQueueURL(in.QueueUrl)
	if err := srv.s.Ack(r.Context(), sub, in.ReceiptHandle); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

type changeVisibilityRequest struct {
	QueueUrl          string `json:"QueueUrl"`
	ReceiptHandle     string `json:"ReceiptHandle"`
	VisibilityTimeout int32  `json:"VisibilityTimeout"`
}

func (srv *Server) changeMessageVisibility(w http.ResponseWriter, r *http.Request) {
	var in changeVisibilityRequest
	if !decode(w, r, &in) {
		return
	}
	sub := normaliseQueueURL(in.QueueUrl)
	if err := srv.s.ChangeVisibility(r.Context(), sub, in.ReceiptHandle, int(in.VisibilityTimeout)); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

type getQueueAttributesRequest struct {
	QueueUrl       string   `json:"QueueUrl"`
	AttributeNames []string `json:"AttributeNames,omitempty"`
}

type getQueueAttributesResponse struct {
	Attributes map[string]string `json:"Attributes"`
}

func (srv *Server) getQueueAttributes(w http.ResponseWriter, r *http.Request) {
	var in getQueueAttributesRequest
	if !decode(w, r, &in) {
		return
	}
	sub := normaliseQueueURL(in.QueueUrl)
	s, err := srv.s.HeadSubscription(r.Context(), sub)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &getQueueAttributesResponse{
		Attributes: map[string]string{
			"VisibilityTimeout":           itoa(s.AckDeadlineSeconds),
			"QueueArn":                    fakeQueueArn(sub),
			"ApproximateNumberOfMessages": "0",
		},
	})
}

type deleteQueueRequest struct {
	QueueUrl string `json:"QueueUrl"`
}

func (srv *Server) deleteQueue(w http.ResponseWriter, r *http.Request) {
	var in deleteQueueRequest
	if !decode(w, r, &in) {
		return
	}
	sub := normaliseQueueURL(in.QueueUrl)
	if err := srv.s.DeleteSubscription(r.Context(), sub); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

type getQueueUrlRequest struct {
	QueueName string `json:"QueueName"`
}

type getQueueUrlResponse struct {
	QueueUrl string `json:"QueueUrl"`
}

func (srv *Server) getQueueUrl(w http.ResponseWriter, r *http.Request) {
	var in getQueueUrlRequest
	if !decode(w, r, &in) {
		return
	}
	if _, err := srv.s.HeadSubscription(r.Context(), in.QueueName); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &getQueueUrlResponse{
		QueueUrl: fakeQueueURL(in.QueueName),
	})
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func normaliseQueueURL(url string) string {
	if url == "" {
		return ""
	}
	if i := strings.LastIndexByte(url, '/'); i >= 0 {
		return url[i+1:]
	}
	return url
}

func fakeQueueURL(name string) string {
	return "https://sqs.shim.amazonaws.com/000000000000/" + name
}

func fakeQueueArn(name string) string {
	return "arn:aws:sqs:us-east-1:000000000000:" + name
}

func decode(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequest",
			"malformed JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var neg bool
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
