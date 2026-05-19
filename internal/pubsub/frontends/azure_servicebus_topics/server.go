// Package azure_servicebus_topics is the Azure Service Bus topics
// REST data-plane frontend for shimanism's pubsub service.
//
// **AMQP open question.** Same posture as Phase 3 — the Azure SDK
// (azure-sdk-for-go/sdk/messaging/azservicebus) drives Service Bus
// over AMQP, not REST. This phase ships REST only; AMQP tier is
// deferred. SDK conformance against AMQP is documented as
// deferred; raw-HTTP exercise is the contract for the Azure
// frontend.
//
// Routes covered (REST data plane):
//
//	PUT    /{topic}                                          — CreateTopic
//	DELETE /{topic}                                          — DeleteTopic
//	GET    /{topic}                                          — HeadTopic
//	PUT    /{topic}/Subscriptions/{sub}                      — CreateSubscription
//	DELETE /{topic}/Subscriptions/{sub}                      — DeleteSubscription
//	GET    /{topic}/Subscriptions/{sub}                      — HeadSubscription
//	POST   /{topic}/messages                                 — Publish
//	POST   /{topic}/Subscriptions/{sub}/messages/head        — Peek-and-lock receive
//	DELETE /{topic}/Subscriptions/{sub}/messages/{id}/{lock} — Ack
//	POST   /{topic}/Subscriptions/{sub}/messages/{id}/{lock} — Renew lock
//	GET    /$Resources/Topics                                — ListTopics
//	GET    /{topic}/Subscriptions                            — ListSubscriptions
package azure_servicebus_topics

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/e6qu/shimanism/internal/pubsub/domain"
)

type Server struct {
	s domain.Pubsub
}

func New(s domain.Pubsub) *Server { return &Server{s: s} }

var (
	reSubMessageRest = regexp.MustCompile(`^/([^/]+)/Subscriptions/([^/]+)/messages/([^/]+)/([^/]+)$`)
	reSubMessageHead = regexp.MustCompile(`^/([^/]+)/Subscriptions/([^/]+)/messages/head/?$`)
	reSubscription   = regexp.MustCompile(`^/([^/]+)/Subscriptions/([^/]+)/?$`)
	reSubsList       = regexp.MustCompile(`^/([^/]+)/Subscriptions/?$`)
	reMessages       = regexp.MustCompile(`^/([^/$]+)/messages/?$`)
	reTopic          = regexp.MustCompile(`^/([^/$]+)/?$`)
	reListTopics     = regexp.MustCompile(`^/\$Resources/Topics/?$`)
)

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if m := reSubMessageRest.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodDelete:
			srv.ack(w, r, m[1], m[2], m[3], m[4])
		case http.MethodPost:
			srv.renewLock(w, r, m[1], m[2], m[3], m[4])
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed on message")
		}
		return
	}
	if m := reSubMessageHead.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.peekLock(w, r, m[1], m[2])
		return
	}
	if m := reSubscription.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.createSubscription(w, r, m[1], m[2])
		case http.MethodDelete:
			srv.deleteSubscription(w, r, m[2])
		case http.MethodGet:
			srv.getSubscription(w, r, m[1], m[2])
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed on subscription")
		}
		return
	}
	if m := reSubsList.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.listSubscriptions(w, r, m[1])
		return
	}
	if m := reMessages.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.publish(w, r, m[1])
		return
	}
	if reListTopics.MatchString(path) && method == http.MethodGet {
		srv.listTopics(w, r)
		return
	}
	if m := reTopic.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.createTopic(w, r, m[1])
		case http.MethodDelete:
			srv.deleteTopic(w, r, m[1])
		case http.MethodGet:
			srv.getTopic(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed on topic")
		}
		return
	}

	writeError(w, http.StatusNotFound, "ResourceNotFound",
		"no Azure Service Bus route matches "+method+" "+path)
}

// ----------------------------------------------------------------------
// Topic handlers
// ----------------------------------------------------------------------

func (srv *Server) createTopic(w http.ResponseWriter, r *http.Request, name string) {
	if _, err := srv.s.CreateTopic(r.Context(), name, domain.CreateTopicOptions{}); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (srv *Server) deleteTopic(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteTopic(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) getTopic(w http.ResponseWriter, r *http.Request, name string) {
	t, err := srv.s.HeadTopic(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"name": t.Name})
}

func (srv *Server) listTopics(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListTopics(r.Context(), domain.ListTopicsOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := struct {
		Topics []string `json:"topics"`
	}{}
	for _, t := range res.Topics {
		out.Topics = append(out.Topics, t.Name)
	}
	writeJSON(w, http.StatusOK, out)
}

// ----------------------------------------------------------------------
// Subscription handlers
// ----------------------------------------------------------------------

func (srv *Server) createSubscription(w http.ResponseWriter, r *http.Request, topic, sub string) {
	if _, err := srv.s.CreateSubscription(r.Context(), topic, sub, domain.CreateSubscriptionOptions{
		Durable: true,
	}); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (srv *Server) deleteSubscription(w http.ResponseWriter, r *http.Request, sub string) {
	if err := srv.s.DeleteSubscription(r.Context(), sub); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) getSubscription(w http.ResponseWriter, r *http.Request, topic, sub string) {
	s, err := srv.s.HeadSubscription(r.Context(), sub)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":               s.Name,
		"topic":              s.Topic,
		"ackDeadlineSeconds": s.AckDeadlineSeconds,
	})
}

func (srv *Server) listSubscriptions(w http.ResponseWriter, r *http.Request, topic string) {
	res, err := srv.s.ListSubscriptions(r.Context(), domain.ListSubscriptionsOptions{Topic: topic})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := struct {
		Subscriptions []string `json:"subscriptions"`
	}{}
	for _, s := range res.Subscriptions {
		out.Subscriptions = append(out.Subscriptions, s.Name)
	}
	writeJSON(w, http.StatusOK, out)
}

// ----------------------------------------------------------------------
// Publish / Receive / Ack / Renew
// ----------------------------------------------------------------------

func (srv *Server) publish(w http.ResponseWriter, r *http.Request, topic string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "read body: "+err.Error())
		return
	}
	if _, err := srv.s.Publish(r.Context(), topic, domain.PublishOptions{Body: body}); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (srv *Server) peekLock(w http.ResponseWriter, r *http.Request, topic, sub string) {
	opt := domain.ReceiveOptions{MaxMessages: 1}
	if tos := r.URL.Query().Get("timeout"); tos != "" {
		if n, err := strconv.Atoi(tos); err == nil {
			opt.WaitTime = n
		}
	}
	msgs, err := srv.s.Receive(r.Context(), sub, opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	if len(msgs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	m := msgs[0]
	bp := map[string]interface{}{
		"MessageId": m.MessageID,
		"LockToken": m.ReceiptHandle,
	}
	if b, err := json.Marshal(bp); err == nil {
		w.Header().Set("BrokerProperties", string(b))
	}
	for k, v := range m.Attributes {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(m.Body)
}

func (srv *Server) ack(w http.ResponseWriter, r *http.Request, topic, sub, messageID, lockToken string) {
	_ = messageID
	_ = topic
	if err := srv.s.Ack(r.Context(), sub, lockToken); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) renewLock(w http.ResponseWriter, r *http.Request, topic, sub, messageID, lockToken string) {
	_ = messageID
	_ = topic
	// Look up the subscription's ack deadline so we extend by the
	// natural lock duration rather than 0 (which inmem treats as
	// "release now").
	s, err := srv.s.HeadSubscription(r.Context(), sub)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	extend := s.AckDeadlineSeconds
	if extend <= 0 {
		extend = 30
	}
	if err := srv.s.ChangeVisibility(r.Context(), sub, lockToken, extend); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
