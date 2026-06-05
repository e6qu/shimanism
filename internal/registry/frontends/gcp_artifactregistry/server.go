// Package gcp_artifactregistry is the GCP Artifact Registry frontend for
// shimanism's container-registry service (Phase 18). Artifact Registry's
// image push/pull is the OCI Distribution /v2/ API authenticated with a
// Bearer OAuth2 token (N31); this frontend gates the shared
// ocidistribution router with a Bearer challenge + the gcpbearer verifier
// and translates the AR repository control plane onto domain.Registry.
//
// Phase 18.B ships the authenticated data plane (the novel push/pull
// path, exercised by a real OCI client). The repository control plane
// (artifactregistry/v1 REST + its LROs) is 18.B follow-on.
package gcp_artifactregistry

import (
	"net/http"
	"strings"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/registry/domain"
	"github.com/e6qu/shimanism/internal/registry/ocidistribution"
)

// audience is the token audience the data-plane Bearer must carry.
const audience = "https://artifactregistry.googleapis.com/"

// arAdapter maps the OCI path repository name onto the backend repository
// name. Artifact Registry addresses images as {project}/{repo}/{image};
// the shim uses that path verbatim as the backend repository key, so the
// mapping is identity.
type arAdapter struct{}

func (arAdapter) RepoName(parsed string) string { return parsed }

// Server is the Artifact Registry frontend.
type Server struct {
	reg      domain.Registry
	verifier *gcpbearer.Verifier
	oci      *ocidistribution.Router
}

// New returns a frontend bound to the given backend.
func New(reg domain.Registry) *Server {
	return &Server{
		reg: reg,
		verifier: gcpbearer.New(gcpbearer.Options{
			Audience: audience,
			TestKey:  []byte("test-key-do-not-use-in-prod"),
		}),
		oci: ocidistribution.NewWithAdapter(reg, arAdapter{}),
	}
}

// Handler returns the frontend's HTTP handler.
func Handler(reg domain.Registry) http.Handler { return New(reg) }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/v2/") || r.URL.Path == "/v2":
		// Data plane (OCI /v2/): OCI clients (go-containerregistry,
		// docker) expect a Bearer challenge on /v2/ and then send
		// Authorization: Bearer <token>. Verify the token; on failure
		// emit the challenge so the client retries with credentials.
		if err := s.verifier.Verify(r); err != nil {
			s.challenge(w, r)
			return
		}
		s.oci.ServeHTTP(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/"):
		// Control plane (artifactregistry/v1 REST): standard Bearer.
		if err := s.verifier.Verify(r); err != nil {
			writeARErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		s.serveControl(w, r)
	default:
		http.NotFound(w, r)
	}
}

// challenge emits the 401 Bearer auth challenge OCI clients use to
// discover the data-plane auth scheme (N31).
func (s *Server) challenge(w http.ResponseWriter, r *http.Request) {
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	realm := scheme + "://" + r.Host + "/v2/token"
	w.Header().Set("WWW-Authenticate",
		`Bearer realm="`+realm+`",service="artifactregistry.googleapis.com"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`))
}
