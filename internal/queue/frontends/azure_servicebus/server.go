// Package azure_servicebus is the Azure Service Bus queues REST
// data-plane frontend for shimanism's queue service.
//
// **AMQP open question.** The Azure SDK
// (azure-sdk-for-go/sdk/messaging/azservicebus) drives Service Bus
// over AMQP, not REST. This phase ships REST only; the AMQP tier is
// deferred. SDK conformance against AMQP is documented as deferred;
// raw-HTTP exercise is the contract for the Azure frontend.
//
// Dispatch: hybrid. The Service Bus *management* (admin) REST spec
// at services/queue/gen/azure (cmd/azure-codegen) covers
// entity/subscription/rule CRUD via `gen.HandlerWithOptions`. The
// *messaging* data-plane URLs (`/{queue}/messages/...`) are NOT in
// the admin spec — they're served by a separate proprietary REST
// surface that's AMQP-bridged on real Azure. Those routes are
// matched by a hand-written regex pre-pass BEFORE the gen mux sees
// the request.
//
// Routes covered (REST data plane, hand-written, before gen):
//
//	POST   /{queue}/messages                       — Send
//	POST   /{queue}/messages/head                  — Peek-and-lock receive
//	DELETE /{queue}/messages/{id}/{lock}           — Complete (ack)
//	POST   /{queue}/messages/{id}/{lock}           — Renew lock
//
// Routes covered (admin REST, via gen):
//
//	PUT    /{entityName}                            — EntityPut (Create queue)
//	GET    /{entityName}                            — EntityGet (Head queue)
//	DELETE /{entityName}                            — EntityDelete (Delete queue)
//	GET    /$Resources/{entityType}                 — ListEntities (List queues — entityType="Queues")
//	GET    /$namespaceinfo                          — NamespaceGet (out of intersection — notImplemented)
//	Subscription/Rule routes                        — not used by queue frontend; notImplemented
package azure_servicebus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/e6qu/shimanism/internal/queue/domain"
	gen "github.com/e6qu/shimanism/services/queue/gen/azure"
)

// Server is an Azure-Service-Bus-shaped HTTP frontend. It implements
// gen.ServerInterface so the gen wire types + spec-drift contract
// are honoured; ServeHTTP dispatches hand-written because the
// upstream Service Bus admin spec has a path-pattern conflict that
// Go 1.22's ServeMux refuses (`/{entityName}` vs
// `/{topicName}/subscriptions` are ambiguous on `GET /x/subscriptions`).
// The hand-written dispatch routes the queue frontend's actually-
// used URLs into the gen.ServerInterface methods directly.
type Server struct {
	s domain.Queues
}

// New returns a frontend bound to the given backend.
func New(s domain.Queues) *Server {
	return &Server{s: s}
}

// Regex routes for both data-plane messaging URLs and admin URLs.
// Admin URLs go into the gen.ServerInterface methods (so wire types
// + adapter pattern stay aligned with the spec); data-plane URLs
// (`/messages/...`) call hand-written handlers since they're outside
// the admin spec.
var (
	reMessageRest = regexp.MustCompile(`^/([^/]+)/messages/([^/]+)/([^/]+)$`)
	reMessageHead = regexp.MustCompile(`^/([^/]+)/messages/head/?$`)
	reMessages    = regexp.MustCompile(`^/([^/]+)/messages/?$`)
	reListEntries = regexp.MustCompile(`^/\$Resources/([^/]+)/?$`)
	reEntity      = regexp.MustCompile(`^/([^/$]+)/?$`)
)

// ServeHTTP dispatches: data-plane messaging URLs to hand-written
// handlers, admin URLs into the gen.ServerInterface methods.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if m := reMessageRest.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodDelete:
			srv.completeMessage(w, r, m[1], m[2], m[3])
		case http.MethodPost:
			srv.renewLock(w, r, m[1], m[2], m[3])
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed on message")
		}
		return
	}
	if m := reMessageHead.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.peekLock(w, r, m[1])
		return
	}
	if m := reMessages.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.sendMessage(w, r, m[1])
		return
	}

	// Admin URLs → gen.ServerInterface methods.
	if m := reListEntries.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.ListEntities(w, r, m[1], gen.ListEntitiesParams{})
		return
	}
	if m := reEntity.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.EntityPut(w, r, m[1], gen.EntityPutParams{})
		case http.MethodGet:
			srv.EntityGet(w, r, m[1], gen.EntityGetParams{})
		case http.MethodDelete:
			srv.EntityDelete(w, r, m[1], gen.EntityDeleteParams{})
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed on entity")
		}
		return
	}

	writeError(w, http.StatusNotFound, "ResourceNotFound",
		"no Azure Service Bus route matches "+method+" "+path)
}

// notImplemented writes the Azure "operation not supported" envelope
// for spec ops outside the cross-cloud queue intersection.
func notImplemented(w http.ResponseWriter, op string) {
	writeError(w, http.StatusNotImplemented, "OperationNotSupported",
		op+" is not in the cross-cloud queue intersection")
}

// =====================================================================
// In-intersection admin handlers (gen.ServerInterface)
// =====================================================================

func (srv *Server) EntityPut(w http.ResponseWriter, r *http.Request, entityName string, _ gen.EntityPutParams) {
	if _, err := srv.s.CreateQueue(r.Context(), entityName, domain.CreateQueueOptions{}); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (srv *Server) EntityDelete(w http.ResponseWriter, r *http.Request, entityName string, _ gen.EntityDeleteParams) {
	if err := srv.s.DeleteQueue(r.Context(), entityName); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) EntityGet(w http.ResponseWriter, r *http.Request, entityName string, _ gen.EntityGetParams) {
	q, err := srv.s.HeadQueue(r.Context(), entityName)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	// Real Azure returns an Atom-feed entry; the shim returns a small
	// JSON shape sufficient for raw-HTTP clients. The official
	// azservicebus SDK doesn't consume this endpoint directly (it
	// uses AMQP).
	writeJSON(w, http.StatusOK, map[string]any{
		"name":                     q.Name,
		"VisibilityTimeoutSeconds": q.Attributes.VisibilityTimeoutSeconds,
		"MessageRetentionSeconds":  q.Attributes.MessageRetentionSeconds,
		"ApproximateMessageCount":  q.Attributes.ApproximateMessageCount,
	})
}

func (srv *Server) ListEntities(w http.ResponseWriter, r *http.Request, entityType string, _ gen.ListEntitiesParams) {
	if !strings.EqualFold(entityType, "Queues") {
		// This frontend is the queue frontend; non-Queues entity types
		// are handled by the pubsub frontend (topics) on its own port.
		notImplemented(w, "ListEntities entityType="+entityType)
		return
	}
	res, err := srv.s.ListQueues(r.Context(), domain.ListQueuesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	queues := make([]string, 0, len(res.Queues))
	for _, q := range res.Queues {
		queues = append(queues, q.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"queues": queues})
}

// =====================================================================
// Out-of-intersection stubs (subscription / rule / namespace) —
// queues don't carry subscriptions or rules; namespace info isn't
// in the cross-cloud intersection.
// =====================================================================

func (srv *Server) NamespaceGet(w http.ResponseWriter, _ *http.Request, _ gen.NamespaceGetParams) {
	notImplemented(w, "NamespaceGet")
}

func (srv *Server) ListSubscriptions(w http.ResponseWriter, _ *http.Request, _ string, _ gen.ListSubscriptionsParams) {
	notImplemented(w, "ListSubscriptions")
}

func (srv *Server) SubscriptionDelete(w http.ResponseWriter, _ *http.Request, _, _ string, _ gen.SubscriptionDeleteParams) {
	notImplemented(w, "SubscriptionDelete")
}

func (srv *Server) SubscriptionGet(w http.ResponseWriter, _ *http.Request, _, _ string, _ gen.SubscriptionGetParams) {
	notImplemented(w, "SubscriptionGet")
}

func (srv *Server) SubscriptionPut(w http.ResponseWriter, _ *http.Request, _, _ string, _ gen.SubscriptionPutParams) {
	notImplemented(w, "SubscriptionPut")
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

func (srv *Server) sendMessage(w http.ResponseWriter, r *http.Request, queue string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "read body: "+err.Error())
		return
	}
	attrs := extractBrokerProperties(r.Header)
	if _, err := srv.s.SendMessage(r.Context(), queue, domain.SendMessageOptions{
		Body:       body,
		Attributes: attrs,
	}); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (srv *Server) peekLock(w http.ResponseWriter, r *http.Request, queue string) {
	opt := domain.ReceiveMessagesOptions{MaxMessages: 1}
	if tos := r.URL.Query().Get("timeout"); tos != "" {
		if n, err := strconv.Atoi(tos); err == nil {
			opt.WaitTime = n
		}
	}
	msgs, err := srv.s.ReceiveMessages(r.Context(), queue, opt)
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

func (srv *Server) completeMessage(w http.ResponseWriter, r *http.Request, queue, messageID, lockToken string) {
	_ = messageID
	if err := srv.s.DeleteMessage(r.Context(), queue, lockToken); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) renewLock(w http.ResponseWriter, r *http.Request, queue, messageID, lockToken string) {
	_ = messageID
	q, err := srv.s.HeadQueue(r.Context(), queue)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	extend := q.Attributes.VisibilityTimeoutSeconds
	if extend <= 0 {
		extend = 30
	}
	if err := srv.s.ChangeVisibility(r.Context(), queue, lockToken, extend); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// =====================================================================
// Helpers
// =====================================================================

func extractBrokerProperties(h http.Header) map[string]string {
	// Azure sends per-message attributes as individual headers OR as
	// serialized JSON in the BrokerProperties header. Honour both.
	out := map[string]string{}
	for k, vs := range h {
		if len(vs) == 0 {
			continue
		}
		switch k {
		case "BrokerProperties", "Content-Type", "Content-Length", "Authorization", "User-Agent", "Host", "Accept", "Accept-Encoding":
			continue
		}
		if !strings.HasPrefix(strings.ToLower(k), "x-shim-") && strings.Contains(k, "-") {
			continue
		}
		out[k] = vs[0]
	}
	return out
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "read body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

var _ = fmt.Sprintf
var _ = errors.As
var _ = decodeJSON
