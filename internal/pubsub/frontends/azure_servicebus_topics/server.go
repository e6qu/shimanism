// Package azure_servicebus_topics is the Azure Service Bus topics
// REST data-plane frontend for shimanism's pubsub service.
//
// **AMQP open question.** The Azure SDK
// (azure-sdk-for-go/sdk/messaging/azservicebus) drives Service Bus
// over AMQP, not REST. This phase ships REST only; AMQP tier is
// deferred. SDK conformance against AMQP is documented as deferred;
// raw-HTTP exercise is the contract for the Azure frontend.
//
// Dispatch: hand-written regex routes admin URLs into the
// gen.ServerInterface methods directly. The upstream Service Bus
// admin spec has path-pattern conflicts (`/{entityName}` vs
// `/{topicName}/subscriptions`) that Go 1.22's ServeMux refuses,
// so `gen.HandlerWithOptions` would panic at construction. Manual
// dispatch sidesteps the panic while preserving the spec wire
// types + the `Server implements gen.ServerInterface` adapter
// contract.
//
// Routes covered (REST data plane, hand-written, not in admin spec):
//
//	PUT    /{topic}                                          — CreateTopic (admin)
//	DELETE /{topic}                                          — DeleteTopic (admin)
//	GET    /{topic}                                          — HeadTopic (admin)
//	PUT    /{topic}/Subscriptions/{sub}                      — CreateSubscription (admin)
//	DELETE /{topic}/Subscriptions/{sub}                      — DeleteSubscription (admin)
//	GET    /{topic}/Subscriptions/{sub}                      — HeadSubscription (admin)
//	POST   /{topic}/messages                                 — Publish (data plane)
//	POST   /{topic}/Subscriptions/{sub}/messages/head        — Peek-and-lock receive (data plane)
//	DELETE /{topic}/Subscriptions/{sub}/messages/{id}/{lock} — Ack (data plane)
//	POST   /{topic}/Subscriptions/{sub}/messages/{id}/{lock} — Renew lock (data plane)
//	GET    /$Resources/Topics                                — ListTopics (admin; ListEntities entityType=Topics)
//	GET    /{topic}/Subscriptions                            — ListSubscriptions (admin)
package azure_servicebus_topics

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/e6qu/shimanism/internal/pubsub/domain"
	gen "github.com/e6qu/shimanism/services/pubsub/gen/azure"
)

// Server is the Azure-Service-Bus-topics-shaped HTTP frontend. It
// implements gen.ServerInterface (the wire types + spec-drift
// contract); ServeHTTP dispatches hand-written because the gen mux
// can't register the upstream Service Bus admin spec's overlapping
// patterns on Go 1.22+ ServeMux.
type Server struct {
	s domain.Pubsub
}

// New returns a frontend bound to the given backend.
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

// ServeHTTP dispatches: data-plane messaging URLs to hand-written
// handlers, admin URLs into the gen.ServerInterface methods.
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
			srv.SubscriptionPut(w, r, m[1], m[2], gen.SubscriptionPutParams{})
		case http.MethodDelete:
			srv.SubscriptionDelete(w, r, m[1], m[2], gen.SubscriptionDeleteParams{})
		case http.MethodGet:
			srv.SubscriptionGet(w, r, m[1], m[2], gen.SubscriptionGetParams{})
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed on subscription")
		}
		return
	}
	if m := reSubsList.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.ListSubscriptions(w, r, m[1], gen.ListSubscriptionsParams{})
		return
	}
	if m := reMessages.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.publish(w, r, m[1])
		return
	}
	if reListTopics.MatchString(path) && method == http.MethodGet {
		srv.ListEntities(w, r, "Topics", gen.ListEntitiesParams{})
		return
	}
	if m := reTopic.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.EntityPut(w, r, m[1], gen.EntityPutParams{})
		case http.MethodDelete:
			srv.EntityDelete(w, r, m[1], gen.EntityDeleteParams{})
		case http.MethodGet:
			srv.EntityGet(w, r, m[1], gen.EntityGetParams{})
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed on topic")
		}
		return
	}

	writeError(w, http.StatusNotFound, "ResourceNotFound",
		"no Azure Service Bus route matches "+method+" "+path)
}

// notImplemented writes the Azure "operation not supported" envelope
// for spec ops outside the cross-cloud pubsub intersection.
func notImplemented(w http.ResponseWriter, op string) {
	writeError(w, http.StatusNotImplemented, "OperationNotSupported",
		op+" is not in the cross-cloud pubsub intersection")
}

// =====================================================================
// Topic admin (gen.ServerInterface — EntityPut/Get/Delete + ListEntities)
// =====================================================================

func (srv *Server) EntityPut(w http.ResponseWriter, r *http.Request, entityName string, _ gen.EntityPutParams) {
	if _, err := srv.s.CreateTopic(r.Context(), entityName, domain.CreateTopicOptions{}); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (srv *Server) EntityDelete(w http.ResponseWriter, r *http.Request, entityName string, _ gen.EntityDeleteParams) {
	if err := srv.s.DeleteTopic(r.Context(), entityName); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) EntityGet(w http.ResponseWriter, r *http.Request, entityName string, _ gen.EntityGetParams) {
	t, err := srv.s.HeadTopic(r.Context(), entityName)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": t.Name})
}

func (srv *Server) ListEntities(w http.ResponseWriter, r *http.Request, entityType string, _ gen.ListEntitiesParams) {
	if !strings.EqualFold(entityType, "Topics") {
		notImplemented(w, "ListEntities entityType="+entityType)
		return
	}
	res, err := srv.s.ListTopics(r.Context(), domain.ListTopicsOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	topics := make([]string, 0, len(res.Topics))
	for _, t := range res.Topics {
		topics = append(topics, t.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"topics": topics})
}

// =====================================================================
// Subscription admin (gen.ServerInterface)
// =====================================================================

func (srv *Server) SubscriptionPut(w http.ResponseWriter, r *http.Request, topicName, subscriptionName string, _ gen.SubscriptionPutParams) {
	if _, err := srv.s.CreateSubscription(r.Context(), topicName, subscriptionName, domain.CreateSubscriptionOptions{
		Durable: true,
	}); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (srv *Server) SubscriptionDelete(w http.ResponseWriter, r *http.Request, _ string, subscriptionName string, _ gen.SubscriptionDeleteParams) {
	if err := srv.s.DeleteSubscription(r.Context(), subscriptionName); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) SubscriptionGet(w http.ResponseWriter, r *http.Request, _ string, subscriptionName string, _ gen.SubscriptionGetParams) {
	s, err := srv.s.HeadSubscription(r.Context(), subscriptionName)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":               s.Name,
		"topic":              s.Topic,
		"ackDeadlineSeconds": s.AckDeadlineSeconds,
	})
}

func (srv *Server) ListSubscriptions(w http.ResponseWriter, r *http.Request, topicName string, _ gen.ListSubscriptionsParams) {
	res, err := srv.s.ListSubscriptions(r.Context(), domain.ListSubscriptionsOptions{Topic: topicName})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	subs := make([]string, 0, len(res.Subscriptions))
	for _, s := range res.Subscriptions {
		subs = append(subs, s.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": subs})
}

// =====================================================================
// Out-of-intersection stubs (namespace, rules) — same gen
// interface as queue, but rules aren't wired here.
// =====================================================================

func (srv *Server) NamespaceGet(w http.ResponseWriter, _ *http.Request, _ gen.NamespaceGetParams) {
	notImplemented(w, "NamespaceGet")
}

func (srv *Server) ListRules(w http.ResponseWriter, _ *http.Request, _, _ string, _ gen.ListRulesParams) {
	notImplemented(w, "ListRules")
}

func (srv *Server) RuleDelete(w http.ResponseWriter, _ *http.Request, _, _, _ string, _ gen.RuleDeleteParams) {
	notImplemented(w, "RuleDelete")
}

func (srv *Server) RuleGet(w http.ResponseWriter, _ *http.Request, _, _, _ string, _ gen.RuleGetParams) {
	notImplemented(w, "RuleGet")
}

func (srv *Server) RulePut(w http.ResponseWriter, _ *http.Request, _, _, _ string, _ gen.RulePutParams) {
	notImplemented(w, "RulePut")
}

// =====================================================================
// Hand-written messaging-data-plane handlers (not in admin spec)
// =====================================================================

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
	bp := map[string]any{
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
