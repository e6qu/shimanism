// Package aws is the AWS ElastiCache passthrough backend.
package aws

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"

	"github.com/e6qu/shimanism/internal/cache/domain"
)

type Backend struct {
	c *elasticache.Client
}

func New(c *elasticache.Client) *Backend { return &Backend{c: c} }

var _ domain.Cache = (*Backend)(nil)

func domainStatus(s string) domain.Status {
	switch s {
	case "creating":
		return domain.StatusCreating
	case "available":
		return domain.StatusAvailable
	case "modifying", "snapshotting":
		return domain.StatusModifying
	case "rebooting cluster nodes":
		return domain.StatusRebooting
	case "deleting":
		return domain.StatusDeleting
	default:
		return domain.StatusUnknown
	}
}

func (b *Backend) toDomain(in *ectypes.CacheCluster) domain.Instance {
	out := domain.Instance{
		Name:          awsapi.ToString(in.CacheClusterId),
		EngineVersion: awsapi.ToString(in.EngineVersion),
		NodeType:      awsapi.ToString(in.CacheNodeType),
		Status:        domainStatus(awsapi.ToString(in.CacheClusterStatus)),
	}
	if in.CacheClusterCreateTime != nil {
		out.CreatedAt = *in.CacheClusterCreateTime
	}
	if in.ConfigurationEndpoint != nil && out.Status == domain.StatusAvailable {
		out.Connection = domain.Connection{
			Host:          awsapi.ToString(in.ConfigurationEndpoint.Address),
			Port:          int(awsapi.ToInt32(in.ConfigurationEndpoint.Port)),
			EngineVersion: out.EngineVersion,
		}
	}
	return out
}

func (b *Backend) CreateInstance(ctx context.Context, name string, opt domain.CreateInstanceOptions) (domain.CreateInstanceResult, error) {
	in := &elasticache.CreateCacheClusterInput{
		CacheClusterId: awsapi.String(name),
		Engine:         awsapi.String("redis"),
		EngineVersion:  awsapi.String(opt.EngineVersion),
		CacheNodeType:  awsapi.String(opt.NodeType),
		NumCacheNodes:  awsapi.Int32(1),
	}
	revealed := ""
	if opt.AuthToken != "" {
		in.AuthToken = awsapi.String(opt.AuthToken)
	} else {
		tok := newToken()
		in.AuthToken = awsapi.String(tok)
		revealed = tok
	}
	out, err := b.c.CreateCacheCluster(ctx, in)
	if err != nil {
		return domain.CreateInstanceResult{}, translateErr(err, name)
	}
	return domain.CreateInstanceResult{
		Instance:  b.toDomain(out.CacheCluster),
		AuthToken: revealed,
	}, nil
}

func (b *Backend) DeleteInstance(ctx context.Context, name string) error {
	if _, err := b.c.DeleteCacheCluster(ctx, &elasticache.DeleteCacheClusterInput{
		CacheClusterId: awsapi.String(name),
	}); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) DescribeInstance(ctx context.Context, name string) (domain.Instance, error) {
	out, err := b.c.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
		CacheClusterId: awsapi.String(name),
	})
	if err != nil {
		return domain.Instance{}, translateErr(err, name)
	}
	if len(out.CacheClusters) == 0 {
		return domain.Instance{}, domain.NoSuchInstance(name)
	}
	return b.toDomain(&out.CacheClusters[0]), nil
}

func (b *Backend) ListInstances(ctx context.Context, opt domain.ListInstancesOptions) (domain.ListInstancesResult, error) {
	out, err := b.c.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{})
	if err != nil {
		return domain.ListInstancesResult{}, translateErr(err, "")
	}
	res := domain.ListInstancesResult{}
	for i := range out.CacheClusters {
		name := awsapi.ToString(out.CacheClusters[i].CacheClusterId)
		if opt.Prefix != "" && !strings.HasPrefix(name, opt.Prefix) {
			continue
		}
		res.Instances = append(res.Instances, b.toDomain(&out.CacheClusters[i]))
		if opt.MaxResults > 0 && len(res.Instances) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) ModifyInstance(ctx context.Context, name string, opt domain.ModifyInstanceOptions) error {
	in := &elasticache.ModifyCacheClusterInput{
		CacheClusterId:   awsapi.String(name),
		ApplyImmediately: awsapi.Bool(true),
	}
	if opt.NodeType != "" {
		in.CacheNodeType = awsapi.String(opt.NodeType)
	}
	if opt.AuthToken != "" {
		in.AuthToken = awsapi.String(opt.AuthToken)
		in.AuthTokenUpdateStrategy = ectypes.AuthTokenUpdateStrategyTypeRotate
	}
	if _, err := b.c.ModifyCacheCluster(ctx, in); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) RebootInstance(ctx context.Context, name string) error {
	if _, err := b.c.RebootCacheCluster(ctx, &elasticache.RebootCacheClusterInput{
		CacheClusterId:       awsapi.String(name),
		CacheNodeIdsToReboot: []string{"0001"},
	}); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func translateErr(err error, name string) error {
	var nfe *ectypes.CacheClusterNotFoundFault
	if errors.As(err, &nfe) {
		return domain.NoSuchInstance(name)
	}
	var ae *ectypes.CacheClusterAlreadyExistsFault
	if errors.As(err, &ae) {
		return domain.InstanceAlreadyExists(name)
	}
	return fmt.Errorf("elasticache %q: %w", name, err)
}

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
