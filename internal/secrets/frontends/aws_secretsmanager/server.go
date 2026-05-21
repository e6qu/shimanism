// Package aws_secretsmanager is the AWS Secrets Manager REST/JSON
// frontend for shimanism's secrets service. It speaks the
// `awsJson1_1` wire protocol that the `aws-sdk-go-v2/service/
// secretsmanager` SDK, `aws secretsmanager` CLI, and the
// `hashicorp/aws` Terraform provider all drive — and translates
// each request into a call on the neutral `domain.Secrets`
// interface, same shape the other frontends use.
//
// Wire details. AWS Secrets Manager dispatches every operation
// through a single endpoint: POST /, with a JSON body and an
// `X-Amz-Target: secretsmanager.<Operation>` header carrying the
// operation name. Errors come back as JSON with a `__type`
// discriminator + appropriate HTTP status. See the vendored
// Smithy spec at services/secrets/spec/aws-secrets-manager.smithy.json
// for the canonical request/response shapes.
//
// Per AGENTS.md's reuse-over-reinvention rule, the request/response
// types are hand-written to mirror the wire JSON shapes the SDK
// expects; we don't run the storage codegen over Secrets Manager
// because the codegen emits REST-XML and Secrets Manager is
// awsJson1_1. Extending the codegen to JSON-protocol is future work.
package aws_secretsmanager

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/e6qu/shimanism/internal/secrets/domain"
)

// Server is the AWS Secrets Manager frontend wired to a backend.
type Server struct {
	s domain.Secrets
}

// New returns a frontend bound to the given backend.
func New(s domain.Secrets) *Server { return &Server{s: s} }

// ServeHTTP dispatches the request to the per-operation handler
// chosen by the `X-Amz-Target` header. Anything that doesn't match
// the supported intersection ops gets the AWS-canonical
// `UnknownOperationException` response so SDK clients see the
// real error vocabulary, not a generic 500.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequest",
			r.Method+" not allowed; Secrets Manager uses POST")
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
	case "CreateSecret":
		srv.createSecret(w, r)
	case "GetSecretValue":
		srv.getSecretValue(w, r)
	case "PutSecretValue":
		srv.putSecretValue(w, r)
	case "DeleteSecret":
		srv.deleteSecret(w, r)
	case "DescribeSecret":
		srv.describeSecret(w, r)
	case "UpdateSecret":
		srv.updateSecret(w, r)
	case "TagResource":
		srv.tagResource(w, r)
	case "UntagResource":
		srv.untagResource(w, r)
	case "ListSecrets":
		srv.listSecrets(w, r)
	case "ListSecretVersionIds":
		srv.listSecretVersionIds(w, r)
	case "GetResourcePolicy":
		// Probe: the hashicorp/aws Terraform provider calls
		// GetResourcePolicy on every aws_secretsmanager_secret read
		// refresh. The shim doesn't shim resource policies (they're
		// IAM-side, out of the secrets data-plane intersection), but
		// returning UnknownOperationException breaks TF state-reads.
		// Honour the call with the canonical "no policy" response so
		// TF state converges; never persist a policy.
		srv.getResourcePolicy(w, r)
	default:
		// Out-of-intersection operation. Return AWS's canonical
		// "operation isn't supported by this service endpoint"
		// vocabulary — never fabricate success.
		writeError(w, http.StatusBadRequest, "UnknownOperationException",
			"operation "+op+" is not supported by this shim")
	}
}

// decode parses the JSON body into the given target struct. Returns
// (ok=false) and writes an InvalidRequest error on failure; in that
// case the caller must just return.
func decode(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	dec := json.NewDecoder(r.Body)
	// awsJson1_1 is permissive about unknown fields (forward
	// compatibility), so we don't DisallowUnknownFields.
	if err := dec.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequest",
			"malformed JSON body: "+err.Error())
		return false
	}
	return true
}

// writeJSON serialises a JSON response with the awsJson1_1 content
// type and the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
