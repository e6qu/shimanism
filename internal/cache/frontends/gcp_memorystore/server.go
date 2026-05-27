// Package gcp_memorystore is the GCP Memorystore-shaped REST/JSON
// frontend for shimanism's cache service. Reuses
// `google.golang.org/api/redis/v1` wire types.
package gcp_memorystore

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	redisapi "google.golang.org/api/redis/v1"

	"github.com/e6qu/shimanism/internal/cache/domain"

	_ "github.com/e6qu/shimanism/services/cache/gen/gcp" // Phase 14.C spec-drift contract; gen.gcp.Routes is the canonical route inventory.
)

type Server struct {
	s domain.Cache
}

func New(s domain.Cache) *Server { return &Server{s: s} }

// Memorystore URL families:
//
//	/v1/projects/{p}/... — google.golang.org/api/redis/v1 (SDK)
//	/v1beta1/projects/{p}/... — hashicorp/google google_redis_instance
//
// Both shapes route to the same handler.

// ServeHTTP dispatches by path-shape inspection. Both `/v1/` and
// `/v1beta1/` prefixes are accepted (SDK and Terraform provider
// respectively). Routes covered: instances collection, instance,
// instance:failover action, operations. Existing
// `TestGCPRoutes_Cache_FrontendDispatchCoverage` pins behavior.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method
	rest := stripVersionPrefix(path)
	if rest == path {
		writeError(w, http.StatusNotFound, "NOT_FOUND",
			"no Memorystore route matches "+method+" "+path)
		return
	}
	segs := strings.Split(strings.TrimPrefix(rest, "/"), "/")
	// segs = ["projects", p, "locations", l, "instances"|"operations", ...]
	if len(segs) < 5 || segs[0] != "projects" || segs[2] != "locations" {
		writeError(w, http.StatusNotFound, "NOT_FOUND",
			"no Memorystore route matches "+method+" "+path)
		return
	}
	switch segs[4] {
	case "instances":
		if len(segs) == 5 || (len(segs) == 6 && segs[5] == "") {
			switch method {
			case http.MethodGet:
				srv.listInstances(w, r)
			case http.MethodPost:
				srv.createInstance(w, r)
			default:
				writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION",
					method+" not allowed on instances collection")
			}
			return
		}
		if len(segs) == 6 {
			tail := segs[5]
			if i := strings.IndexByte(tail, ':'); i >= 0 {
				// /instances/{name}:action
				name, action := tail[:i], tail[i+1:]
				if action == "failover" && method == http.MethodPost {
					srv.failover(w, r, name)
					return
				}
			} else {
				// /instances/{name}
				switch method {
				case http.MethodGet:
					srv.getInstance(w, r, tail)
				case http.MethodDelete:
					srv.deleteInstance(w, r, tail)
				case http.MethodPatch, http.MethodPut:
					srv.patchInstance(w, r, tail)
				default:
					writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION",
						method+" not allowed on instance")
				}
				return
			}
		}
	case "operations":
		if len(segs) == 6 && method == http.MethodGet {
			srv.getOperation(w, r, segs[5])
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
		MemorySizeGB:  int(body.MemorySizeGb),
	}
	if _, err := srv.s.CreateInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, projectFromPath(r.URL.Path), locationFromPath(r.URL.Path), name, "CREATE")
}

func (srv *Server) getInstance(w http.ResponseWriter, r *http.Request, name string) {
	inst, err := srv.s.DescribeInstance(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, instanceToGCPWithPath(inst, projectFromPath(r.URL.Path), locationFromPath(r.URL.Path)))
}

func (srv *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListInstances(r.Context(), domain.ListInstancesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	project := projectFromPath(r.URL.Path)
	location := locationFromPath(r.URL.Path)
	out := redisapi.ListInstancesResponse{}
	for _, i := range res.Instances {
		out.Instances = append(out.Instances, instanceToGCPWithPath(i, project, location))
	}
	writeJSON(w, http.StatusOK, &out)
}

func (srv *Server) deleteInstance(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, projectFromPath(r.URL.Path), locationFromPath(r.URL.Path), name, "DELETE")
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
	writeOperation(w, projectFromPath(r.URL.Path), locationFromPath(r.URL.Path), name, "UPDATE")
}

func (srv *Server) failover(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.RebootInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, projectFromPath(r.URL.Path), locationFromPath(r.URL.Path), name, "FAILOVER")
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

// stripVersionPrefix removes the "/v1/" or "/v1beta1/" segment so
// downstream parsers can work with the version-neutral remainder.
func stripVersionPrefix(path string) string {
	for _, p := range []string{"/v1/", "/v1beta1/"} {
		if strings.HasPrefix(path, p) {
			return path[len(p)-1:]
		}
	}
	return path
}

func projectFromPath(path string) string {
	parts := strings.Split(strings.TrimPrefix(stripVersionPrefix(path), "/projects/"), "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

func locationFromPath(path string) string {
	parts := strings.Split(strings.TrimPrefix(stripVersionPrefix(path), "/projects/"), "/")
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

// instanceToGCPWithPath surfaces the canonical Memorystore Instance
// shape including the full resource Name + the read-side defaults
// hashicorp/google's `google_redis_instance` schema expects. Honest
// defaults — they describe "an instance with the default network /
// encryption posture", not fabricated state.
func instanceToGCPWithPath(in domain.Instance, project, location string) *redisapi.Instance {
	out := &redisapi.Instance{
		Tier:                  in.NodeType,
		RedisVersion:          in.EngineVersion,
		MemorySizeGb:          int64(in.MemorySizeGB),
		State:                 gcpStatusFromDomain(in.Status),
		Host:                  in.Connection.Host,
		Port:                  int64(in.Connection.Port),
		ConnectMode:           "DIRECT_PEERING",
		TransitEncryptionMode: "DISABLED",
		AuthEnabled:           false,
		ReadReplicasMode:      "READ_REPLICAS_DISABLED",
		ReplicaCount:          0,
	}
	if project != "" && location != "" && in.Name != "" {
		out.Name = fmt.Sprintf("projects/%s/locations/%s/instances/%s", project, location, in.Name)
	}
	if location != "" {
		out.LocationId = location
	}
	if !in.CreatedAt.IsZero() {
		out.CreateTime = in.CreatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// writeOperation returns the canonical Operation envelope. The
// Operation Name encodes (opType, target) so Operations.Get can
// resolve the current state by reading the target — stateless, no
// shim-side operation table (Phase 10.1 / BUG-5 closure).
//
// Memorystore Operation names follow the full GCP resource-name
// convention: `projects/{p}/locations/{l}/operations/{op-id}`.
// hashicorp/google google_redis_instance polls the operation by
// using the response's Name field directly to construct the URL,
// so the prefix must be present.
func writeOperation(w http.ResponseWriter, project, location, target, opType string) {
	writeJSON(w, http.StatusOK, &redisapi.Operation{
		Name: fmt.Sprintf("projects/%s/locations/%s/operations/%s",
			project, location, encodeOperationName(opType, target)),
		Done: false,
	})
}

func encodeOperationName(opType, target string) string {
	return "operation-" + strings.ToLower(opType) + "-" + target
}

func decodeOperationName(name string) (opType, target string, ok bool) {
	rest, found := strings.CutPrefix(name, "operation-")
	if !found {
		return "", "", false
	}
	for _, t := range []string{"create", "delete", "update", "failover"} {
		if suf, ok := strings.CutPrefix(rest, t+"-"); ok {
			return t, suf, true
		}
	}
	return "", "", false
}

func (srv *Server) getOperation(w http.ResponseWriter, r *http.Request, name string) {
	opType, target, ok := decodeOperationName(name)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND",
			"operation "+name+" not found (shim only resolves operations it issued)")
		return
	}
	inst, err := srv.s.DescribeInstance(r.Context(), target)
	fullName := fmt.Sprintf("projects/%s/locations/%s/operations/%s",
		projectFromPath(r.URL.Path), locationFromPath(r.URL.Path), name)
	op := &redisapi.Operation{Name: fullName}
	if err != nil {
		// Delete success signals via NoSuchInstance.
		if opType == "delete" {
			op.Done = true
			writeJSON(w, http.StatusOK, op)
			return
		}
		mapDomainError(w, err)
		return
	}
	switch inst.Status {
	case domain.StatusAvailable:
		op.Done = true
	default:
		op.Done = false
	}
	writeJSON(w, http.StatusOK, op)
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
