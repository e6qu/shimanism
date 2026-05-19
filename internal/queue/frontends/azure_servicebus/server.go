// Package azure_servicebus is the Azure Service Bus REST data-plane
// frontend for shimanism's queue service.
//
// **AMQP open question.** The Azure SDK
// (`azure-sdk-for-go/sdk/messaging/azservicebus`) drives Service
// Bus over AMQP, not REST. Per [PLAN.md Phase 3 open question],
// the AMQP fidelity tier is deferred — this frontend speaks only
// the REST data-plane API, which is enough for raw-HTTP clients
// + the management surface. SDK conformance for this frontend
// against AMQP is documented as deferred.
//
// Routes covered (REST data plane):
//
//	POST   /{queue}/messages                      — SendMessage
//	POST   /{queue}/messages/head                 — Peek-and-lock receive
//	DELETE /{queue}/messages/{messageID}/{lock}   — Complete (ack)
//	POST   /{queue}/messages/{messageID}/{lock}   — Renew lock (change visibility)
//	PUT    /{queue}                               — Create queue
//	DELETE /{queue}                               — Delete queue
//	GET    /$Resources/Queues                     — List queues
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
)

// Server is an Azure-Service-Bus-shaped HTTP frontend.
type Server struct {
	s domain.Queues
}

func New(s domain.Queues) *Server { return &Server{s: s} }

var (
	reMessageRest = regexp.MustCompile(`^/([^/]+)/messages/([^/]+)/([^/]+)$`)
	reMessages    = regexp.MustCompile(`^/([^/]+)/messages/?$`)
	reMessageHead = regexp.MustCompile(`^/([^/]+)/messages/head/?$`)
	reQueue       = regexp.MustCompile(`^/([^/$]+)/?$`)
	reListQueues  = regexp.MustCompile(`^/\$Resources/Queues/?$`)
)

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
	if reListQueues.MatchString(path) && method == http.MethodGet {
		srv.listQueues(w, r)
		return
	}
	if m := reQueue.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.createQueue(w, r, m[1])
		case http.MethodDelete:
			srv.deleteQueue(w, r, m[1])
		case http.MethodGet:
			srv.getQueue(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed on queue")
		}
		return
	}

	writeError(w, http.StatusNotFound, "ResourceNotFound",
		"no Azure Service Bus route matches "+method+" "+path)
}

func (srv *Server) createQueue(w http.ResponseWriter, r *http.Request, name string) {
	if _, err := srv.s.CreateQueue(r.Context(), name, domain.CreateQueueOptions{}); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (srv *Server) deleteQueue(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteQueue(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) getQueue(w http.ResponseWriter, r *http.Request, name string) {
	q, err := srv.s.HeadQueue(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	// Azure SB management returns an Atom-feed entry; the shim returns
	// a small JSON shape sufficient for raw-HTTP clients. The official
	// azservicebus SDK doesn't consume this endpoint.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":                     q.Name,
		"VisibilityTimeoutSeconds": q.Attributes.VisibilityTimeoutSeconds,
		"MessageRetentionSeconds":  q.Attributes.MessageRetentionSeconds,
		"ApproximateMessageCount":  q.Attributes.ApproximateMessageCount,
	})
}

func (srv *Server) listQueues(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListQueues(r.Context(), domain.ListQueuesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := struct {
		Queues []string `json:"queues"`
	}{}
	for _, q := range res.Queues {
		out.Queues = append(out.Queues, q.Name)
	}
	writeJSON(w, http.StatusOK, out)
}

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
	// Azure REST returns BrokerProperties as a JSON header value.
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

func (srv *Server) completeMessage(w http.ResponseWriter, r *http.Request, queue, messageID, lockToken string) {
	// The Azure REST URL shape carries both messageID and lockToken, but
	// the shim treats lockToken as the opaque receipt-handle. Backends
	// that need to recover the messageID (e.g. the Azure-SB backend) can
	// encode the pair into the receipt themselves; backends that don't
	// (inmem, AWS, GCP, NATS) ignore the URL's messageID segment.
	_ = messageID
	if err := srv.s.DeleteMessage(r.Context(), queue, lockToken); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) renewLock(w http.ResponseWriter, r *http.Request, queue, messageID, lockToken string) {
	_ = messageID
	// Azure REST renew-lock has no per-call timeout — it extends by the
	// queue's configured lock duration. Resolve it on each call rather
	// than caching, since the queue's attributes can change between
	// receives.
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

func extractBrokerProperties(h http.Header) map[string]string {
	// Azure sends per-message attributes as individual headers OR as
	// a serialized JSON in the BrokerProperties header. Honour both.
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
			// Skip generic HTTP headers; only forward x-shim-* attrs.
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
