// Package azure_redis is the Azure Cache for Redis REST frontend.
// ARM URL shape: /subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Cache/redis/{name}
package azure_redis

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/e6qu/shimanism/internal/cache/domain"
)

type Server struct {
	s domain.Cache
}

func New(s domain.Cache) *Server { return &Server{s: s} }

var (
	reRedis       = regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Cache/[Rr]edis/([^/]+)/?$`)
	reRedisList   = regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Cache/[Rr]edis/?$`)
	reRedisReboot = regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Cache/[Rr]edis/([^/]+)/forceReboot/?$`)
)

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if m := reRedisReboot.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.reboot(w, r, m[1])
		return
	}
	if m := reRedis.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.createInstance(w, r, m[1])
		case http.MethodGet:
			srv.getInstance(w, r, m[1])
		case http.MethodDelete:
			srv.deleteInstance(w, r, m[1])
		case http.MethodPatch:
			srv.patchInstance(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed on redis")
		}
		return
	}
	if reRedisList.MatchString(path) && method == http.MethodGet {
		srv.listInstances(w, r)
		return
	}
	writeError(w, http.StatusNotFound, "ResourceNotFound",
		"no Azure Cache for Redis route matches "+method+" "+path)
}

// ----------------------------------------------------------------------
// Wire shapes (subset of Microsoft.Cache/redis schema)
// ----------------------------------------------------------------------

type redisBody struct {
	Location   string           `json:"location,omitempty"`
	Properties *redisProperties `json:"properties,omitempty"`
}

type redisProperties struct {
	SKU               *skuBody `json:"sku,omitempty"`
	RedisVersion      string   `json:"redisVersion,omitempty"`
	ProvisioningState string   `json:"provisioningState,omitempty"`
	HostName          string   `json:"hostName,omitempty"`
	Port              int      `json:"port,omitempty"`
	EnableNonSSLPort  bool     `json:"enableNonSslPort,omitempty"`
}

type skuBody struct {
	Name     string `json:"name,omitempty"`
	Family   string `json:"family,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
}

type redisResource struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Type       string           `json:"type"`
	Location   string           `json:"location"`
	Properties *redisProperties `json:"properties"`
}

type listResponse struct {
	Value []*redisResource `json:"value"`
}

func (srv *Server) createInstance(w http.ResponseWriter, r *http.Request, name string) {
	var body redisBody
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.CreateInstanceOptions{}
	if body.Properties != nil {
		opt.EngineVersion = body.Properties.RedisVersion
		if body.Properties.SKU != nil {
			opt.NodeType = body.Properties.SKU.Name
		}
	}
	if _, err := srv.s.CreateInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	inst, _ := srv.s.DescribeInstance(r.Context(), name)
	writeJSON(w, http.StatusCreated, instanceToARM(inst))
}

func (srv *Server) getInstance(w http.ResponseWriter, r *http.Request, name string) {
	inst, err := srv.s.DescribeInstance(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, instanceToARM(inst))
}

func (srv *Server) deleteInstance(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) patchInstance(w http.ResponseWriter, r *http.Request, name string) {
	var body redisBody
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.ModifyInstanceOptions{}
	if body.Properties != nil && body.Properties.SKU != nil {
		opt.NodeType = body.Properties.SKU.Name
	}
	if err := srv.s.ModifyInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	inst, _ := srv.s.DescribeInstance(r.Context(), name)
	writeJSON(w, http.StatusOK, instanceToARM(inst))
}

func (srv *Server) reboot(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.RebootInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListInstances(r.Context(), domain.ListInstancesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := listResponse{}
	for _, i := range res.Instances {
		out.Value = append(out.Value, instanceToARM(i))
	}
	writeJSON(w, http.StatusOK, &out)
}

func instanceToARM(in domain.Instance) *redisResource {
	port := in.Connection.Port
	if port == 0 {
		port = 6379
	}
	res := &redisResource{
		Name:     in.Name,
		Type:     "Microsoft.Cache/Redis",
		Location: "eastus",
		Properties: &redisProperties{
			RedisVersion:      in.EngineVersion,
			ProvisioningState: statusToARM(in.Status),
			SKU:               &skuBody{Name: in.NodeType, Family: "C", Capacity: 0},
		},
	}
	if in.Status == domain.StatusAvailable && in.Connection.Host != "" {
		res.Properties.HostName = in.Connection.Host
		res.Properties.Port = port
	}
	return res
}

func statusToARM(s domain.Status) string {
	switch s {
	case domain.StatusAvailable:
		return "Succeeded"
	case domain.StatusCreating:
		return "Creating"
	case domain.StatusModifying:
		return "Updating"
	case domain.StatusRebooting:
		return "Restarting"
	case domain.StatusDeleting:
		return "Deleting"
	default:
		return "Unknown"
	}
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

var _ = time.Time{}
