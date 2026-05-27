// Package gcp_pubsub is the GCP Pub/Sub REST/JSON frontend for
// shimanism's pubsub service.
//
// Same protocol as Phase 3's GCP Pub/Sub frontend (REST + JSON,
// reusing google.golang.org/api/pubsub/v1 wire types) but
// fanout-aware: topic ≠ subscription, multiple subs can attach to
// one topic, and topic-delete doesn't drop the subscription.
package gcp_pubsub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/pubsub/domain"

	_ "github.com/e6qu/shimanism/services/pubsub/gen/gcp" // Phase 14.C spec-drift contract; gen.gcp.Routes is the canonical route inventory.
)

type Server struct {
	s domain.Pubsub
}

func New(s domain.Pubsub) *Server { return &Server{s: s} }

// ServeHTTP dispatches by path-shape inspection. Same shape as the
// queue gcp_pubsub frontend; the only differences are the domain
// interface (Pubsub vs Queues) and that subscriptions are
// independent of topics (fanout-aware). Existing
// `TestGCPRoutes_Pubsub_FrontendDispatchCoverage` pins behavior.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method
	rest, ok := strings.CutPrefix(path, "/v1/")
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND",
			"no GCP Pub/Sub route matches "+method+" "+path)
		return
	}
	segs := strings.Split(rest, "/")
	if len(segs) < 3 || segs[0] != "projects" {
		writeError(w, http.StatusNotFound, "NOT_FOUND",
			"no GCP Pub/Sub route matches "+method+" "+path)
		return
	}
	switch segs[2] {
	case "topics":
		if len(segs) == 3 || (len(segs) == 4 && segs[3] == "") {
			if method == http.MethodGet {
				srv.listTopics(w, r)
				return
			}
		}
		if len(segs) == 4 {
			tail := segs[3]
			if i := strings.IndexByte(tail, ':'); i >= 0 {
				name, action := tail[:i], tail[i+1:]
				if action == "publish" && method == http.MethodPost {
					srv.publish(w, r, name)
					return
				}
			} else {
				switch method {
				case http.MethodPut:
					srv.createTopic(w, r, tail)
				case http.MethodGet:
					srv.getTopic(w, r, tail)
				case http.MethodDelete:
					srv.deleteTopic(w, r, tail)
				default:
					writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION",
						method+" not allowed on topic")
				}
				return
			}
		}
	case "subscriptions":
		if len(segs) == 3 || (len(segs) == 4 && segs[3] == "") {
			if method == http.MethodGet {
				srv.listSubscriptions(w, r)
				return
			}
		}
		if len(segs) == 4 {
			tail := segs[3]
			if i := strings.IndexByte(tail, ':'); i >= 0 {
				name, action := tail[:i], tail[i+1:]
				if method == http.MethodPost {
					switch action {
					case "pull":
						srv.pull(w, r, name)
						return
					case "acknowledge":
						srv.acknowledge(w, r, name)
						return
					case "modifyAckDeadline":
						srv.modifyAckDeadline(w, r, name)
						return
					}
				}
			} else {
				switch method {
				case http.MethodPut:
					srv.createSubscription(w, r, segs[1], tail)
				case http.MethodGet:
					srv.getSubscription(w, r, segs[1], tail)
				case http.MethodDelete:
					srv.deleteSubscription(w, r, tail)
				default:
					writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION",
						method+" not allowed on subscription")
				}
				return
			}
		}
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND",
		"no GCP Pub/Sub route matches "+method+" "+path)
}

// ----------------------------------------------------------------------
// Topic handlers
// ----------------------------------------------------------------------

func (srv *Server) createTopic(w http.ResponseWriter, r *http.Request, name string) {
	var body pubsubraw.Topic
	_ = decodeJSON(w, r, &body)
	if _, err := srv.s.CreateTopic(r.Context(), name, domain.CreateTopicOptions{}); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &pubsubraw.Topic{
		Name: fmt.Sprintf("projects/%s/topics/%s", projectFromPath(r.URL.Path), name),
	})
}

func (srv *Server) getTopic(w http.ResponseWriter, r *http.Request, name string) {
	if _, err := srv.s.HeadTopic(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &pubsubraw.Topic{
		Name: fmt.Sprintf("projects/%s/topics/%s", projectFromPath(r.URL.Path), name),
	})
}

func (srv *Server) deleteTopic(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteTopic(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (srv *Server) listTopics(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListTopics(r.Context(), domain.ListTopicsOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	project := projectFromPath(r.URL.Path)
	resp := pubsubraw.ListTopicsResponse{}
	for _, t := range res.Topics {
		resp.Topics = append(resp.Topics, &pubsubraw.Topic{
			Name: fmt.Sprintf("projects/%s/topics/%s", project, t.Name),
		})
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) publish(w http.ResponseWriter, r *http.Request, topic string) {
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
		res, err := srv.s.Publish(r.Context(), topic, domain.PublishOptions{
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
	topic := nameFromResource(body.Topic)
	if _, err := srv.s.CreateSubscription(r.Context(), topic, name, domain.CreateSubscriptionOptions{
		AckDeadlineSeconds: int(body.AckDeadlineSeconds),
		Durable:            true,
	}); err != nil {
		mapDomainError(w, err)
		return
	}
	resp := pubsubraw.Subscription{
		Name:               fmt.Sprintf("projects/%s/subscriptions/%s", project, name),
		Topic:              fmt.Sprintf("projects/%s/topics/%s", project, topic),
		AckDeadlineSeconds: body.AckDeadlineSeconds,
	}
	if resp.AckDeadlineSeconds == 0 {
		resp.AckDeadlineSeconds = 10
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) getSubscription(w http.ResponseWriter, r *http.Request, project, name string) {
	s, err := srv.s.HeadSubscription(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &pubsubraw.Subscription{
		Name:               fmt.Sprintf("projects/%s/subscriptions/%s", project, name),
		Topic:              fmt.Sprintf("projects/%s/topics/%s", project, s.Topic),
		AckDeadlineSeconds: int64(s.AckDeadlineSeconds),
	})
}

func (srv *Server) deleteSubscription(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteSubscription(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (srv *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListSubscriptions(r.Context(), domain.ListSubscriptionsOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	project := projectFromPath(r.URL.Path)
	resp := pubsubraw.ListSubscriptionsResponse{}
	for _, s := range res.Subscriptions {
		resp.Subscriptions = append(resp.Subscriptions, &pubsubraw.Subscription{
			Name:               fmt.Sprintf("projects/%s/subscriptions/%s", project, s.Name),
			Topic:              fmt.Sprintf("projects/%s/topics/%s", project, s.Topic),
			AckDeadlineSeconds: int64(s.AckDeadlineSeconds),
		})
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) pull(w http.ResponseWriter, r *http.Request, name string) {
	var body pubsubraw.PullRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.ReceiveOptions{
		MaxMessages: int(body.MaxMessages),
	}
	if !body.ReturnImmediately {
		opt.WaitTime = 10
	}
	msgs, err := srv.s.Receive(r.Context(), name, opt)
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
		if err := srv.s.Ack(r.Context(), name, id); err != nil {
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

func nameFromResource(r string) string {
	if i := strings.LastIndex(r, "/"); i >= 0 {
		return r[i+1:]
	}
	return r
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

var _ = errors.As
