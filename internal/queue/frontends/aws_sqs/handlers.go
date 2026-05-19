package aws_sqs

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/shimanism/internal/queue/domain"
)

// ----------------------------------------------------------------------
// Wire types — JSON shapes the SDK puts on / reads from the wire.
// ----------------------------------------------------------------------

type createQueueRequest struct {
	QueueName  string            `json:"QueueName"`
	Attributes map[string]string `json:"Attributes,omitempty"`
	Tags       map[string]string `json:"Tags,omitempty"`
}

type createQueueResponse struct {
	QueueUrl string `json:"QueueUrl"`
}

type deleteQueueRequest struct {
	QueueUrl string `json:"QueueUrl"`
}

type listQueuesRequest struct {
	QueueNamePrefix string `json:"QueueNamePrefix,omitempty"`
	MaxResults      *int32 `json:"MaxResults,omitempty"`
	NextToken       string `json:"NextToken,omitempty"`
}

type listQueuesResponse struct {
	QueueUrls []string `json:"QueueUrls,omitempty"`
	NextToken string   `json:"NextToken,omitempty"`
}

type getQueueUrlRequest struct {
	QueueName              string `json:"QueueName"`
	QueueOwnerAWSAccountId string `json:"QueueOwnerAWSAccountId,omitempty"`
}

type getQueueUrlResponse struct {
	QueueUrl string `json:"QueueUrl"`
}

type getQueueAttributesRequest struct {
	QueueUrl       string   `json:"QueueUrl"`
	AttributeNames []string `json:"AttributeNames,omitempty"`
}

type getQueueAttributesResponse struct {
	Attributes map[string]string `json:"Attributes"`
}

type messageAttributeValue struct {
	StringValue string `json:"StringValue,omitempty"`
	BinaryValue []byte `json:"BinaryValue,omitempty"`
	DataType    string `json:"DataType"`
}

type sendMessageRequest struct {
	QueueUrl          string                           `json:"QueueUrl"`
	MessageBody       string                           `json:"MessageBody"`
	DelaySeconds      *int32                           `json:"DelaySeconds,omitempty"`
	MessageAttributes map[string]messageAttributeValue `json:"MessageAttributes,omitempty"`
}

type sendMessageResponse struct {
	MessageId        string `json:"MessageId"`
	MD5OfMessageBody string `json:"MD5OfMessageBody,omitempty"`
}

type receiveMessageRequest struct {
	QueueUrl              string   `json:"QueueUrl"`
	MaxNumberOfMessages   *int32   `json:"MaxNumberOfMessages,omitempty"`
	VisibilityTimeout     *int32   `json:"VisibilityTimeout,omitempty"`
	WaitTimeSeconds       *int32   `json:"WaitTimeSeconds,omitempty"`
	MessageAttributeNames []string `json:"MessageAttributeNames,omitempty"`
	AttributeNames        []string `json:"AttributeNames,omitempty"`
}

type receiveMessageOut struct {
	MessageId         string                              `json:"MessageId"`
	ReceiptHandle     string                              `json:"ReceiptHandle"`
	Body              string                              `json:"Body"`
	MD5OfBody         string                              `json:"MD5OfBody,omitempty"`
	Attributes        map[string]string                   `json:"Attributes,omitempty"`
	MessageAttributes map[string]messageAttributeValueOut `json:"MessageAttributes,omitempty"`
}

type messageAttributeValueOut struct {
	StringValue string `json:"StringValue,omitempty"`
	DataType    string `json:"DataType"`
}

type receiveMessageResponse struct {
	Messages []receiveMessageOut `json:"Messages"`
}

type deleteMessageRequest struct {
	QueueUrl      string `json:"QueueUrl"`
	ReceiptHandle string `json:"ReceiptHandle"`
}

type changeMessageVisibilityRequest struct {
	QueueUrl          string `json:"QueueUrl"`
	ReceiptHandle     string `json:"ReceiptHandle"`
	VisibilityTimeout int32  `json:"VisibilityTimeout"`
}

// ----------------------------------------------------------------------
// QueueUrl helpers. Shim issues URLs of the form
// https://sqs.shim.amazonaws.com/000000000000/<name>. Clients may
// pass real-AWS URLs (sqs.<region>.amazonaws.com/<account>/<name>) —
// the normaliser strips the host + account prefix and returns the
// bare queue name.
// ----------------------------------------------------------------------

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

// ----------------------------------------------------------------------
// Per-operation handlers
// ----------------------------------------------------------------------

func (srv *Server) createQueue(w http.ResponseWriter, r *http.Request) {
	var in createQueueRequest
	if !decode(w, r, &in) {
		return
	}
	if in.QueueName == "" {
		writeError(w, http.StatusBadRequest, "InvalidParameterValue", "QueueName is required")
		return
	}
	opt := domain.CreateQueueOptions{Attributes: attributesFromAWS(in.Attributes)}
	if _, err := srv.s.CreateQueue(r.Context(), in.QueueName, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &createQueueResponse{QueueUrl: fakeQueueURL(in.QueueName)})
}

func (srv *Server) deleteQueue(w http.ResponseWriter, r *http.Request) {
	var in deleteQueueRequest
	if !decode(w, r, &in) {
		return
	}
	name := normaliseQueueURL(in.QueueUrl)
	if name == "" {
		writeError(w, http.StatusBadRequest, "InvalidParameterValue", "QueueUrl is required")
		return
	}
	if err := srv.s.DeleteQueue(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (srv *Server) listQueues(w http.ResponseWriter, r *http.Request) {
	var in listQueuesRequest
	if !decode(w, r, &in) {
		return
	}
	opt := domain.ListQueuesOptions{Prefix: in.QueueNamePrefix, NextToken: in.NextToken}
	if in.MaxResults != nil {
		opt.MaxResults = int(*in.MaxResults)
	}
	res, err := srv.s.ListQueues(r.Context(), opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := listQueuesResponse{NextToken: res.NextToken}
	for _, q := range res.Queues {
		resp.QueueUrls = append(resp.QueueUrls, fakeQueueURL(q.Name))
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) getQueueUrl(w http.ResponseWriter, r *http.Request) {
	var in getQueueUrlRequest
	if !decode(w, r, &in) {
		return
	}
	if in.QueueName == "" {
		writeError(w, http.StatusBadRequest, "InvalidParameterValue", "QueueName is required")
		return
	}
	if _, err := srv.s.HeadQueue(r.Context(), in.QueueName); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &getQueueUrlResponse{QueueUrl: fakeQueueURL(in.QueueName)})
}

func (srv *Server) getQueueAttributes(w http.ResponseWriter, r *http.Request) {
	var in getQueueAttributesRequest
	if !decode(w, r, &in) {
		return
	}
	name := normaliseQueueURL(in.QueueUrl)
	q, err := srv.s.HeadQueue(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &getQueueAttributesResponse{Attributes: attributesToAWS(q.Attributes)})
}

func (srv *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	var in sendMessageRequest
	if !decode(w, r, &in) {
		return
	}
	name := normaliseQueueURL(in.QueueUrl)
	opt := domain.SendMessageOptions{
		Body:       []byte(in.MessageBody),
		Attributes: messageAttrsFromAWS(in.MessageAttributes),
	}
	if in.DelaySeconds != nil {
		opt.DelaySeconds = int(*in.DelaySeconds)
	}
	res, err := srv.s.SendMessage(r.Context(), name, opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &sendMessageResponse{MessageId: res.MessageID})
}

func (srv *Server) receiveMessage(w http.ResponseWriter, r *http.Request) {
	var in receiveMessageRequest
	if !decode(w, r, &in) {
		return
	}
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
	msgs, err := srv.s.ReceiveMessages(r.Context(), name, opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := receiveMessageResponse{Messages: make([]receiveMessageOut, 0, len(msgs))}
	for _, m := range msgs {
		resp.Messages = append(resp.Messages, receiveMessageOut{
			MessageId:         m.MessageID,
			ReceiptHandle:     m.ReceiptHandle,
			Body:              string(m.Body),
			Attributes:        sqsSystemAttributes(m),
			MessageAttributes: messageAttrsToAWS(m.Attributes),
		})
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	var in deleteMessageRequest
	if !decode(w, r, &in) {
		return
	}
	name := normaliseQueueURL(in.QueueUrl)
	if err := srv.s.DeleteMessage(r.Context(), name, in.ReceiptHandle); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (srv *Server) changeMessageVisibility(w http.ResponseWriter, r *http.Request) {
	var in changeMessageVisibilityRequest
	if !decode(w, r, &in) {
		return
	}
	name := normaliseQueueURL(in.QueueUrl)
	if err := srv.s.ChangeVisibility(r.Context(), name, in.ReceiptHandle, int(in.VisibilityTimeout)); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

// ----------------------------------------------------------------------
// AWS ↔ domain attribute mapping
// ----------------------------------------------------------------------

func attributesFromAWS(attrs map[string]string) domain.QueueAttributes {
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

func attributesToAWS(a domain.QueueAttributes) map[string]string {
	return map[string]string{
		"VisibilityTimeout":           strconv.Itoa(a.VisibilityTimeoutSeconds),
		"MessageRetentionPeriod":      strconv.Itoa(a.MessageRetentionSeconds),
		"MaximumMessageSize":          strconv.Itoa(a.MaxMessageSizeBytes),
		"DelaySeconds":                strconv.Itoa(a.DelaySeconds),
		"ApproximateNumberOfMessages": strconv.Itoa(a.ApproximateMessageCount),
		"CreatedTimestamp":            strconv.FormatInt(a.CreatedAt.Unix(), 10),
	}
}

func messageAttrsFromAWS(in map[string]messageAttributeValue) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		// Only String and Number map cleanly across clouds. Binary is
		// out of intersection.
		out[k] = v.StringValue
	}
	return out
}

func messageAttrsToAWS(in map[string]string) map[string]messageAttributeValueOut {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]messageAttributeValueOut, len(in))
	for k, v := range in {
		out[k] = messageAttributeValueOut{StringValue: v, DataType: "String"}
	}
	return out
}

func sqsSystemAttributes(m domain.Message) map[string]string {
	out := map[string]string{}
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
