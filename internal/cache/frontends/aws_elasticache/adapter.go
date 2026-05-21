// Package aws_elasticache is the AWS ElastiCache frontend for
// shimanism's cache service. Phase 11.12 migrated it from a
// hand-written awsQuery wire layer to spec-driven generated stubs.
package aws_elasticache

import (
	"context"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/awsquery"
	"github.com/e6qu/shimanism/internal/cache/domain"
	gen "github.com/e6qu/shimanism/services/cache/gen"
)

// Adapter binds gen.ElastiCacheBackend to a domain.Cache backend.
type Adapter struct {
	s domain.Cache
}

// New returns the http.Handler dispatching through the generated
// awsQuery router into the adapter bound to the given backend.
func New(s domain.Cache) http.Handler {
	return gen.RegisterElastiCacheRoutes(&Adapter{s: s})
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
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

func mapDomainErr(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		return &awsquery.BackendError{HTTPStatus: http.StatusInternalServerError, Type: "Receiver", Code: "InternalFailure", Message: err.Error()}
	}
	be := &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Message: de.Error()}
	switch de.Kind {
	case domain.KindNoSuchInstance:
		be.HTTPStatus = http.StatusNotFound
		be.Code = "CacheClusterNotFound"
	case domain.KindInstanceAlreadyExists:
		be.Code = "CacheClusterAlreadyExists"
	case domain.KindInvalidArgument:
		be.Code = "InvalidParameterValue"
	default:
		be.HTTPStatus = http.StatusInternalServerError
		be.Type = "Receiver"
		be.Code = "InternalFailure"
	}
	return be
}

func instanceToAWS(inst domain.Instance) *gen.CacheCluster {
	status := awsStatusFromDomain(inst.Status)
	engine := "redis"
	out := &gen.CacheCluster{
		CacheClusterId:     &inst.Name,
		CacheClusterStatus: &status,
		Engine:             &engine,
		EngineVersion:      &inst.EngineVersion,
		CacheNodeType:      &inst.NodeType,
	}
	if inst.Status == domain.StatusAvailable && inst.Connection.Host != "" {
		port := int32(inst.Connection.Port)
		host := inst.Connection.Host
		out.ConfigurationEndpoint = &gen.Endpoint{Address: &host, Port: &port}
	}
	return out
}

// ---------------------------------------------------------------------
// Per-op methods.
// ---------------------------------------------------------------------

func (a *Adapter) CreateCacheCluster(ctx context.Context, in *gen.CreateCacheClusterMessage) (*gen.CreateCacheClusterResult, error) {
	if in.CacheClusterId == "" {
		return nil, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameterValue", Message: "CacheClusterId is required"}
	}
	opt := domain.CreateInstanceOptions{
		EngineVersion: strDeref(in.EngineVersion),
		NodeType:      strDeref(in.CacheNodeType),
		AuthToken:     strDeref(in.AuthToken),
	}
	res, err := a.s.CreateInstance(ctx, in.CacheClusterId, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.CreateCacheClusterResult{CacheCluster: instanceToAWS(res.Instance)}, nil
}

func (a *Adapter) DeleteCacheCluster(ctx context.Context, in *gen.DeleteCacheClusterMessage) (*gen.DeleteCacheClusterResult, error) {
	if err := a.s.DeleteInstance(ctx, in.CacheClusterId); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.DeleteCacheClusterResult{CacheCluster: instanceToAWS(domain.Instance{
		Name:   in.CacheClusterId,
		Status: domain.StatusDeleting,
	})}, nil
}

func (a *Adapter) DescribeCacheClusters(ctx context.Context, in *gen.DescribeCacheClustersMessage) (*gen.CacheClusterMessage, error) {
	name := strDeref(in.CacheClusterId)
	out := &gen.CacheClusterMessage{}
	if name != "" {
		inst, err := a.s.DescribeInstance(ctx, name)
		if err != nil {
			return nil, mapDomainErr(err)
		}
		out.CacheClusters.Member = append(out.CacheClusters.Member, *instanceToAWS(inst))
		return out, nil
	}
	res, err := a.s.ListInstances(ctx, domain.ListInstancesOptions{})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	for _, i := range res.Instances {
		out.CacheClusters.Member = append(out.CacheClusters.Member, *instanceToAWS(i))
	}
	return out, nil
}

func (a *Adapter) ModifyCacheCluster(ctx context.Context, in *gen.ModifyCacheClusterMessage) (*gen.ModifyCacheClusterResult, error) {
	opt := domain.ModifyInstanceOptions{
		NodeType:  strDeref(in.CacheNodeType),
		AuthToken: strDeref(in.AuthToken),
	}
	if err := a.s.ModifyInstance(ctx, in.CacheClusterId, opt); err != nil {
		return nil, mapDomainErr(err)
	}
	inst, _ := a.s.DescribeInstance(ctx, in.CacheClusterId)
	return &gen.ModifyCacheClusterResult{CacheCluster: instanceToAWS(inst)}, nil
}

func (a *Adapter) RebootCacheCluster(ctx context.Context, in *gen.RebootCacheClusterMessage) (*gen.RebootCacheClusterResult, error) {
	if err := a.s.RebootInstance(ctx, in.CacheClusterId); err != nil {
		return nil, mapDomainErr(err)
	}
	inst, _ := a.s.DescribeInstance(ctx, in.CacheClusterId)
	return &gen.RebootCacheClusterResult{CacheCluster: instanceToAWS(inst)}, nil
}
