// Package gcp_memorystore is the GCP Memorystore-shaped REST/JSON
// frontend for shimanism's cache service. Reuses
// `google.golang.org/api/redis/v1` wire types.
package gcp_memorystore

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	redisapi "google.golang.org/api/redis/v1"

	"github.com/e6qu/shimanism/internal/cache/domain"
)

type Server struct {
	s domain.Cache
}

func New(s domain.Cache) *Server { return &Server{s: s} }

var (
	reInstances        = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/instances/?$`)
	reInstance         = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/instances/([^/:]+)$`)
	reInstanceFailover = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/instances/([^/:]+):failover$`)
)

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if m := reInstanceFailover.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.failover(w, r, m[3])
		return
	}
	if m := reInstance.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.getInstance(w, r, m[3])
		case http.MethodDelete:
			srv.deleteInstance(w, r, m[3])
		case http.MethodPatch, http.MethodPut:
			srv.patchInstance(w, r, m[3])
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION",
				method+" not allowed on instance")
		}
		return
	}
	if m := reInstances.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.listInstances(w, r)
			return
		case http.MethodPost:
			srv.createInstance(w, r)
			return
		}
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND",
		"no Memorystore route matches "+method+" "+path)
}

func (srv *Server) createInstance(w http.ResponseWriter, r *http.Request) {
	var body redisapi.Instance
	if !decodeJSON(w, r, &body) {
		return
	}
	name := r.URL.Query().Get("instanceId")
	opt := domain.CreateInstanceOptions{
		EngineVersion: body.RedisVersion,
		NodeType:      body.Tier,
	}
	if _, err := srv.s.CreateInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, name, "CREATE")
}

func (srv *Server) getInstance(w http.ResponseWriter, r *http.Request, name string) {
	inst, err := srv.s.DescribeInstance(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, instanceToGCP(inst))
}

func (srv *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListInstances(r.Context(), domain.ListInstancesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := redisapi.ListInstancesResponse{}
	for _, i := range res.Instances {
		out.Instances = append(out.Instances, instanceToGCP(i))
	}
	writeJSON(w, http.StatusOK, &out)
}

func (srv *Server) deleteInstance(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, name, "DELETE")
}

func (srv *Server) patchInstance(w http.ResponseWriter, r *http.Request, name string) {
	var body redisapi.Instance
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.ModifyInstanceOptions{NodeType: body.Tier}
	if err := srv.s.ModifyInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, name, "UPDATE")
}

func (srv *Server) failover(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.RebootInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, name, "FAILOVER")
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func projectFromPath(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/v1/projects/"), "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

func locationFromPath(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/v1/projects/"), "/")
	if len(parts) >= 3 && parts[1] == "locations" {
		return parts[2]
	}
	return "us-central1"
}

func gcpStatusFromDomain(s domain.Status) string {
	switch s {
	case domain.StatusAvailable:
		return "READY"
	case domain.StatusCreating:
		return "CREATING"
	case domain.StatusModifying:
		return "UPDATING"
	case domain.StatusDeleting:
		return "DELETING"
	default:
		return "STATE_UNSPECIFIED"
	}
}

func instanceToGCP(in domain.Instance) *redisapi.Instance {
	out := &redisapi.Instance{
		Tier:         in.NodeType,
		RedisVersion: in.EngineVersion,
		State:        gcpStatusFromDomain(in.Status),
		Host:         in.Connection.Host,
		Port:         int64(in.Connection.Port),
	}
	if !in.CreatedAt.IsZero() {
		out.CreateTime = in.CreatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func writeOperation(w http.ResponseWriter, target, opType string) {
	writeJSON(w, http.StatusOK, &redisapi.Operation{
		Name: fmt.Sprintf("operation-%d", time.Now().UnixNano()),
		Done: false,
	})
	_ = target
	_ = opType
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

var _ = projectFromPath
var _ = locationFromPath
