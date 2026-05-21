// Package aws_sqs is the AWS SQS REST/JSON frontend for shimanism's
// queue service. It speaks the `awsJson1_0` wire protocol that
// `aws-sdk-go-v2/service/sqs` (since 2023), `aws sqs` CLI, and the
// `hashicorp/aws` Terraform provider all drive, and translates
// each request into a call on the neutral `domain.Queues`
// interface.
//
// Wire details. SQS dispatches every operation through a single
// endpoint: POST /, with a JSON body and an
// `X-Amz-Target: AmazonSQS.<Operation>` header carrying the
// operation name. Errors come back as JSON with a `__type`
// discriminator + appropriate HTTP status. The Content-Type is
// `application/x-amz-json-1.0` (one minor version below Secrets
// Manager's awsJson1_1 — same shape on the wire). See the
// vendored Smithy spec at services/queue/spec/aws-sqs.smithy.json
// for the canonical request/response shapes.
//
// QueueUrl handling. AWS SQS responses carry queue URLs of the
// form `https://sqs.<region>.amazonaws.com/<account>/<name>`. The
// shim returns shim-issued URLs whose host segment is `shim` and
// account is `000000000000`; the normaliser accepts both shapes
// so clients passing real-AWS-shaped URLs through the shim work.
package aws_sqs

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/e6qu/shimanism/internal/queue/domain"
)

// Server is the AWS SQS frontend wired to a backend.
type Server struct {
	s domain.Queues
}

// New returns a frontend bound to the given backend.
func New(s domain.Queues) *Server { return &Server{s: s} }

// ServeHTTP dispatches the request to the per-operation handler
// chosen by the `X-Amz-Target` header.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequest",
			r.Method+" not allowed; SQS uses POST")
		return
	}
	target := r.Header.Get("X-Amz-Target")
	if target == "" {
		writeError(w, http.StatusBadRequest, "InvalidRequest",
			"missing X-Amz-Target header")
		return
	}
	op := target
	if i := strings.IndexByte(target, '.'); i >= 0 {
		op = target[i+1:]
	}
	switch op {
	case "CreateQueue":
		srv.createQueue(w, r)
	case "DeleteQueue":
		srv.deleteQueue(w, r)
	case "ListQueues":
		srv.listQueues(w, r)
	case "GetQueueUrl":
		srv.getQueueUrl(w, r)
	case "GetQueueAttributes":
		srv.getQueueAttributes(w, r)
	case "SetQueueAttributes":
		srv.setQueueAttributes(w, r)
	case "SendMessage":
		srv.sendMessage(w, r)
	case "ReceiveMessage":
		srv.receiveMessage(w, r)
	case "DeleteMessage":
		srv.deleteMessage(w, r)
	case "ChangeMessageVisibility":
		srv.changeMessageVisibility(w, r)
	case "ListQueueTags":
		srv.listQueueTags(w, r)
	default:
		writeError(w, http.StatusBadRequest, "UnknownOperationException",
			"operation "+op+" is not supported by this shim")
	}
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
