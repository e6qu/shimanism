// Package aws_elasticache is the AWS ElastiCache-shaped HTTP
// frontend for shimanism's cache service. awsQuery wire protocol
// — same family as Phase 4 SNS and Phase 5 RDS.
package aws_elasticache

import (
	"io"
	"net/http"
	"net/url"

	"github.com/e6qu/shimanism/internal/cache/domain"
)

type Server struct {
	s domain.Cache
}

func New(s domain.Cache) *Server { return &Server{s: s} }

const ecNamespace = "http://elasticache.amazonaws.com/doc/2015-02-02/"

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Sender", "MethodNotAllowed",
			"only POST is allowed on the ElastiCache endpoint")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Sender", "MalformedInput",
			"read body: "+err.Error())
		return
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Sender", "MalformedInput",
			"parse query: "+err.Error())
		return
	}
	action := form.Get("Action")
	switch action {
	case "CreateCacheCluster":
		srv.createCacheCluster(w, r, form)
	case "DeleteCacheCluster":
		srv.deleteCacheCluster(w, r, form)
	case "DescribeCacheClusters":
		srv.describeCacheClusters(w, r, form)
	case "ModifyCacheCluster":
		srv.modifyCacheCluster(w, r, form)
	case "RebootCacheCluster":
		srv.rebootCacheCluster(w, r, form)
	default:
		writeError(w, http.StatusBadRequest, "Sender", "InvalidAction",
			"unknown or out-of-intersection action: "+action)
	}
}

func (srv *Server) createCacheCluster(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := form.Get("CacheClusterId")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameter",
			"CacheClusterId is required")
		return
	}
	opt := domain.CreateInstanceOptions{
		EngineVersion: form.Get("EngineVersion"),
		NodeType:      form.Get("CacheNodeType"),
		AuthToken:     form.Get("AuthToken"),
	}
	res, err := srv.s.CreateInstance(r.Context(), name, opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeCacheClusterResponse(w, "CreateCacheCluster", res.Instance)
}

func (srv *Server) deleteCacheCluster(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := form.Get("CacheClusterId")
	if err := srv.s.DeleteInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeCacheClusterResponse(w, "DeleteCacheCluster", domain.Instance{
		Name:   name,
		Status: domain.StatusDeleting,
	})
}

func (srv *Server) describeCacheClusters(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := form.Get("CacheClusterId")
	if name != "" {
		inst, err := srv.s.DescribeInstance(r.Context(), name)
		if err != nil {
			mapDomainError(w, err)
			return
		}
		writeDescribeCacheClusters(w, []domain.Instance{inst})
		return
	}
	res, err := srv.s.ListInstances(r.Context(), domain.ListInstancesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeDescribeCacheClusters(w, res.Instances)
}

func (srv *Server) modifyCacheCluster(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := form.Get("CacheClusterId")
	opt := domain.ModifyInstanceOptions{
		NodeType:  form.Get("CacheNodeType"),
		AuthToken: form.Get("AuthToken"),
	}
	if err := srv.s.ModifyInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	inst, _ := srv.s.DescribeInstance(r.Context(), name)
	writeCacheClusterResponse(w, "ModifyCacheCluster", inst)
}

func (srv *Server) rebootCacheCluster(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := form.Get("CacheClusterId")
	if err := srv.s.RebootInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	inst, _ := srv.s.DescribeInstance(r.Context(), name)
	writeCacheClusterResponse(w, "RebootCacheCluster", inst)
}

func awsStatusFromDomain(s domain.Status) string {
	switch s {
	case domain.StatusCreating:
		return "creating"
	case domain.StatusAvailable:
		return "available"
	case domain.StatusModifying:
		return "modifying"
	case domain.StatusRebooting:
		return "rebooting cache cluster nodes"
	case domain.StatusDeleting:
		return "deleting"
	default:
		return "unknown"
	}
}
