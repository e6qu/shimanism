// Package gcp_pubsub is the GCP Pub/Sub REST/JSON frontend for
// shimanism's queue service. It speaks the HTTP+JSON wire
// protocol that `google.golang.org/api/pubsub/v1` (the official
// Discovery-generated REST SDK) and `gcloud pubsub` drive, and
// translates each request into a call on the neutral
// `domain.Queues` interface.
//
// Per AGENTS.md's reuse-over-reinvention rule, the request/response
// wire types come from `google.golang.org/api/pubsub/v1` directly —
// the same raw types the SDK is generated from. The shim only
// emits the routing + dispatch + error-envelope layer.
//
// A domain queue maps to a topic + subscription pair sharing the
// queue's name. Topic ops and subscription ops are addressed under
// different resource paths but resolve to the same backend queue
// when their short names match.
package gcp_pubsub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/queue/domain"
)

// Server is a GCP-Pub/Sub-shaped HTTP frontend.
type Server struct {
	s domain.Queues
}

func New(s domain.Queues) *Server { return &Server{s: s} }

var (
	reTopicPublish     = regexp.MustCompile(`^/v1/projects/([^/]+)/topics/([^/:]+):publish$`)
	reTopic            = regexp.MustCompile(`^/v1/projects/([^/]+)/topics/([^/]+)$`)
	reTopics           = regexp.MustCompile(`^/v1/projects/([^/]+)/topics/?$`)
	reSubPull          = regexp.MustCompile(`^/v1/projects/([^/]+)/subscriptions/([^/:]+):pull$`)
	reSubAck           = regexp.MustCompile(`^/v1/projects/([^/]+)/subscriptions/([^/:]+):acknowledge$`)
	reSubModAck        = regexp.MustCompile(`^/v1/projects/([^/]+)/subscriptions/([^/:]+):modifyAckDeadline$`)
	reSubscription     = regexp.MustCompile(`^/v1/projects/([^/]+)/subscriptions/([^/]+)$`)
	reSubscriptionList = regexp.MustCompile(`^/v1/projects/([^/]+)/subscriptions/?$`)
)

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if m := reTopicPublish.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.publish(w, r, m[2])
		return
	}
	if m := reTopic.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.createTopic(w, r, m[2])
		case http.MethodGet:
			srv.getTopic(w, r, m[2])
		case http.MethodDelete:
			srv.deleteTopic(w, r, m[2])
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed on topic")
		}
		return
	}
	if reTopics.MatchString(path) {
		if method == http.MethodGet {
			srv.listTopics(w, r)
			return
		}
	}
	if m := reSubPull.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.pull(w, r, m[2])
		return
	}
	if m := reSubAck.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.acknowledge(w, r, m[2])
		return
	}
	if m := reSubModAck.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.modifyAckDeadline(w, r, m[2])
		return
	}
	if m := reSubscription.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.createSubscription(w, r, m[1], m[2])
		case http.MethodGet:
			srv.getSubscription(w, r, m[1], m[2])
		case http.MethodDelete:
			srv.deleteSubscription(w, r, m[2])
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed on subscription")
		}
		return
	}
	if reSubscriptionList.MatchString(path) {
		if method == http.MethodGet {
			srv.listSubscriptions(w, r)
			return
		}
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND",
		"no GCP Pub/Sub route matches "+method+" "+path)
}

// ----------------------------------------------------------------------
// Topic handlers
// ----------------------------------------------------------------------

func (srv *Server) createTopic(w http.ResponseWriter, r *http.Request, name string) {
	// Body carries an optional Topic message we mostly ignore — only
	// the queue name (parsed from URL) matters at this phase.
	var body pubsubraw.Topic
	_ = decodeJSON(w, r, &body)
	if _, err := srv.s.CreateQueue(r.Context(), name, domain.CreateQueueOptions{}); err != nil {
		mapDomainError(w, err)
		return
	}
	project := projectFromPath(r.URL.Path)
	writeJSON(w, http.StatusOK, &pubsubraw.Topic{
		Name: fmt.Sprintf("projects/%s/topics/%s", project, name),
	})
}

func (srv *Server) getTopic(w http.ResponseWriter, r *http.Request, name string) {
	if _, err := srv.s.HeadQueue(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	project := projectFromPath(r.URL.Path)
	writeJSON(w, http.StatusOK, &pubsubraw.Topic{
		Name: fmt.Sprintf("projects/%s/topics/%s", project, name),
	})
}

func (srv *Server) deleteTopic(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteQueue(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (srv *Server) listTopics(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListQueues(r.Context(), domain.ListQueuesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	project := projectFromPath(r.URL.Path)
	resp := pubsubraw.ListTopicsResponse{}
	for _, q := range res.Queues {
		resp.Topics = append(resp.Topics, &pubsubraw.Topic{
			Name: fmt.Sprintf("projects/%s/topics/%s", project, q.Name),
		})
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) publish(w http.ResponseWriter, r *http.Request, name string) {
	var body pubsubraw.PublishRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	resp := pubsubraw.PublishResponse{}
	for _, msg := range body.Messages {
		data, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
				"message.data is not valid base64: "+err.Error())
			return
		}
		res, err := srv.s.SendMessage(r.Context(), name, domain.SendMessageOptions{
			Body:       data,
			Attributes: msg.Attributes,
		})
		if err != nil {
			mapDomainError(w, err)
			return
		}
		resp.MessageIds = append(resp.MessageIds, res.MessageID)
	}
	writeJSON(w, http.StatusOK, &resp)
}

// ----------------------------------------------------------------------
// Subscription handlers
// ----------------------------------------------------------------------

func (srv *Server) createSubscription(w http.ResponseWriter, r *http.Request, project, name string) {
	var body pubsubraw.Subscription
	if !decodeJSON(w, r, &body) {
		return
	}
	retentionSec := parseDurationSeconds(body.MessageRetentionDuration)
	opt := domain.CreateQueueOptions{Attributes: domain.QueueAttributes{
		VisibilityTimeoutSeconds: int(body.AckDeadlineSeconds),
		MessageRetentionSeconds:  retentionSec,
	}}
	if _, err := srv.s.CreateQueue(r.Context(), name, opt); err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Kind == domain.KindQueueAlreadyExists {
			// Subscriptions can refer to existing topics; treat
			// already-exists on the queue as OK.
		} else {
			mapDomainError(w, err)
			return
		}
	}
	resp := pubsubraw.Subscription{
		Name:                     fmt.Sprintf("projects/%s/subscriptions/%s", project, name),
		Topic:                    body.Topic,
		AckDeadlineSeconds:       body.AckDeadlineSeconds,
		MessageRetentionDuration: messageRetentionDurationOrDefault(retentionSec),
	}
	if resp.AckDeadlineSeconds == 0 {
		resp.AckDeadlineSeconds = 10
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) getSubscription(w http.ResponseWriter, r *http.Request, project, name string) {
	q, err := srv.s.HeadQueue(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &pubsubraw.Subscription{
		Name:                     fmt.Sprintf("projects/%s/subscriptions/%s", project, name),
		Topic:                    fmt.Sprintf("projects/%s/topics/%s", project, name),
		AckDeadlineSeconds:       int64(q.Attributes.VisibilityTimeoutSeconds),
		MessageRetentionDuration: messageRetentionDurationOrDefault(q.Attributes.MessageRetentionSeconds),
	})
}

func (srv *Server) deleteSubscription(w http.ResponseWriter, r *http.Request, name string) {
	// In real Pub/Sub, topics and subscriptions are independent resources;
	// deleting a subscription does not delete the topic. The shim collapses
	// the pair onto a single domain queue, so subscription-delete probes
	// existence (404 if the queue is gone) but otherwise leaves the queue
	// alive — topic-delete is what tears down the queue.
	if _, err := srv.s.HeadQueue(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (srv *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListQueues(r.Context(), domain.ListQueuesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	project := projectFromPath(r.URL.Path)
	resp := pubsubraw.ListSubscriptionsResponse{}
	for _, q := range res.Queues {
		resp.Subscriptions = append(resp.Subscriptions, &pubsubraw.Subscription{
			Name:                     fmt.Sprintf("projects/%s/subscriptions/%s", project, q.Name),
			Topic:                    fmt.Sprintf("projects/%s/topics/%s", project, q.Name),
			AckDeadlineSeconds:       int64(q.Attributes.VisibilityTimeoutSeconds),
			MessageRetentionDuration: messageRetentionDurationOrDefault(q.Attributes.MessageRetentionSeconds),
		})
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) pull(w http.ResponseWriter, r *http.Request, name string) {
	var body pubsubraw.PullRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.ReceiveMessagesOptions{
		MaxMessages: int(body.MaxMessages),
	}
	if !body.ReturnImmediately {
		// Pub/Sub long-pulls up to the SDK's connection timeout. The
		// shim uses a small fixed wait so it returns promptly when
		// no messages are available.
		opt.WaitTime = 10
	}
	msgs, err := srv.s.ReceiveMessages(r.Context(), name, opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := pubsubraw.PullResponse{}
	for _, m := range msgs {
		resp.ReceivedMessages = append(resp.ReceivedMessages, &pubsubraw.ReceivedMessage{
			AckId: m.ReceiptHandle,
			Message: &pubsubraw.PubsubMessage{
				MessageId:   m.MessageID,
				Data:        base64.StdEncoding.EncodeToString(m.Body),
				Attributes:  m.Attributes,
				PublishTime: m.ReceivedAt.Format(time.RFC3339Nano),
			},
			DeliveryAttempt: int64(m.DeliveryCount),
		})
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) acknowledge(w http.ResponseWriter, r *http.Request, name string) {
	var body pubsubraw.AcknowledgeRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	for _, id := range body.AckIds {
		if err := srv.s.DeleteMessage(r.Context(), name, id); err != nil {
			mapDomainError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (srv *Server) modifyAckDeadline(w http.ResponseWriter, r *http.Request, name string) {
	var body pubsubraw.ModifyAckDeadlineRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	for _, id := range body.AckIds {
		if err := srv.s.ChangeVisibility(r.Context(), name, id, int(body.AckDeadlineSeconds)); err != nil {
			mapDomainError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func projectFromPath(path string) string {
	const prefix = "/v1/projects/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "read body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

var _ = strconv.Itoa

// messageRetentionDurationOrDefault returns the GCP Pub/Sub subscription
// retention as a duration-formatted string ("604800s"). Real GCP defaults
// to 604800s (7 days) when no value was set at create time; the shim
// matches that read-side fidelity so terraform plan after apply doesn't
// report drift on the field. Domain seconds == 0 means "unset" → emit
// the GCP default.
func messageRetentionDurationOrDefault(seconds int) string {
	if seconds <= 0 {
		return "604800s"
	}
	return strconv.Itoa(seconds) + "s"
}

// parseDurationSeconds parses a GCP duration-formatted string ("604800s"
// or "604800.0s") into an integer second count. Returns 0 on any parse
// failure (interpreted by the domain as "unset, use backend default").
func parseDurationSeconds(s string) int {
	if s == "" {
		return 0
	}
	s = strings.TrimSuffix(s, "s")
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
