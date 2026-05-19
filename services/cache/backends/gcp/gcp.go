// Package gcp is the GCP Memorystore for Redis backend.
package gcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	redisapi "google.golang.org/api/redis/v1"

	"github.com/e6qu/shimanism/internal/cache/domain"
)

type Config struct {
	ProjectID string
	Region    string // default us-central1
}

type Backend struct {
	svc     *redisapi.Service
	project string
	region  string
}

func New(svc *redisapi.Service, cfg Config) *Backend {
	r := cfg.Region
	if r == "" {
		r = "us-central1"
	}
	return &Backend{svc: svc, project: cfg.ProjectID, region: r}
}

var _ domain.Cache = (*Backend)(nil)

func (b *Backend) parent() string {
	return fmt.Sprintf("projects/%s/locations/%s", b.project, b.region)
}

func (b *Backend) instancePath(name string) string {
	return fmt.Sprintf("%s/instances/%s", b.parent(), name)
}

func nameFromPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func domainStatus(s string) domain.Status {
	switch s {
	case "READY":
		return domain.StatusAvailable
	case "CREATING":
		return domain.StatusCreating
	case "UPDATING", "MAINTENANCE":
		return domain.StatusModifying
	case "DELETING":
		return domain.StatusDeleting
	default:
		return domain.StatusCreating
	}
}

func (b *Backend) toDomain(in *redisapi.Instance) domain.Instance {
	out := domain.Instance{
		Name:          nameFromPath(in.Name),
		EngineVersion: in.RedisVersion,
		NodeType:      in.Tier,
		Status:        domainStatus(in.State),
	}
	if t, err := time.Parse(time.RFC3339, in.CreateTime); err == nil {
		out.CreatedAt = t
	}
	if out.Status == domain.StatusAvailable {
		out.Connection = domain.Connection{
			Host:          in.Host,
			Port:          int(in.Port),
			EngineVersion: in.RedisVersion,
		}
		// Memorystore stores AUTH on the instance; the API exposes it
		// via a separate GetAuthString endpoint. The shim doesn't
		// fetch it on every Describe to keep the op cheap — auth is
		// returned exclusively at CreateInstance time (mirrors AWS).
	}
	return out
}

func (b *Backend) CreateInstance(ctx context.Context, name string, opt domain.CreateInstanceOptions) (domain.CreateInstanceResult, error) {
	tier := opt.NodeType
	if tier == "" {
		tier = "BASIC"
	}
	revealed := ""
	if opt.AuthToken == "" {
		revealed = newToken()
	}
	in := &redisapi.Instance{
		Tier:         tier,
		RedisVersion: redisVersionToGCP(opt.EngineVersion),
		MemorySizeGb: 1,
		AuthEnabled:  true,
	}
	if _, err := b.svc.Projects.Locations.Instances.Create(b.parent(), in).InstanceId(name).Context(ctx).Do(); err != nil {
		return domain.CreateInstanceResult{}, translateErr(err, name)
	}
	return domain.CreateInstanceResult{
		Instance: domain.Instance{
			Name:          name,
			EngineVersion: opt.EngineVersion,
			NodeType:      tier,
			Status:        domain.StatusCreating,
			CreatedAt:     time.Now().UTC(),
		},
		AuthToken: revealed,
	}, nil
}

func (b *Backend) DeleteInstance(ctx context.Context, name string) error {
	if _, err := b.svc.Projects.Locations.Instances.Delete(b.instancePath(name)).Context(ctx).Do(); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) DescribeInstance(ctx context.Context, name string) (domain.Instance, error) {
	in, err := b.svc.Projects.Locations.Instances.Get(b.instancePath(name)).Context(ctx).Do()
	if err != nil {
		return domain.Instance{}, translateErr(err, name)
	}
	return b.toDomain(in), nil
}

func (b *Backend) ListInstances(ctx context.Context, opt domain.ListInstancesOptions) (domain.ListInstancesResult, error) {
	out, err := b.svc.Projects.Locations.Instances.List(b.parent()).Context(ctx).Do()
	if err != nil {
		return domain.ListInstancesResult{}, translateErr(err, "")
	}
	res := domain.ListInstancesResult{}
	for _, in := range out.Instances {
		name := nameFromPath(in.Name)
		if opt.Prefix != "" && !strings.HasPrefix(name, opt.Prefix) {
			continue
		}
		res.Instances = append(res.Instances, b.toDomain(in))
		if opt.MaxResults > 0 && len(res.Instances) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) ModifyInstance(ctx context.Context, name string, opt domain.ModifyInstanceOptions) error {
	patch := &redisapi.Instance{}
	mask := []string{}
	if opt.NodeType != "" {
		patch.Tier = opt.NodeType
		mask = append(mask, "tier")
	}
	if _, err := b.svc.Projects.Locations.Instances.Patch(b.instancePath(name), patch).
		UpdateMask(strings.Join(mask, ",")).Context(ctx).Do(); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) RebootInstance(ctx context.Context, name string) error {
	// GCP Memorystore's failover op is a control-plane restart proxy.
	if _, err := b.svc.Projects.Locations.Instances.Failover(b.instancePath(name),
		&redisapi.FailoverInstanceRequest{DataProtectionMode: "FORCE_DATA_LOSS"}).
		Context(ctx).Do(); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func redisVersionToGCP(v string) string {
	if v == "" {
		return "REDIS_7_0"
	}
	if strings.HasPrefix(v, "REDIS_") {
		return v
	}
	return "REDIS_" + strings.ReplaceAll(v, ".", "_")
}

func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "404"), strings.Contains(msg, "notFound"):
		return domain.NoSuchInstance(name)
	case strings.Contains(msg, "409"), strings.Contains(msg, "alreadyExists"):
		return domain.InstanceAlreadyExists(name)
	}
	return fmt.Errorf("memorystore %q: %w", name, err)
}

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
