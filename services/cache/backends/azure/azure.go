// Package azure is the Azure Cache for Redis backend.
package azure

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"

	"github.com/e6qu/shimanism/internal/cache/domain"
)

type Config struct {
	SubscriptionID string
	ResourceGroup  string
	Location       string
	Credential     azcore.TokenCredential
	// ClientOptions, if non-nil, is forwarded to the armredis factory.
	// Used by the sockerless test lane to point the ARM endpoint at a
	// local simulator and inject a self-signed-cert-tolerant transport.
	ClientOptions *arm.ClientOptions
}

type Backend struct {
	c             *armredis.Client
	subscription  string
	resourceGroup string
	location      string
}

func New(cfg Config) (*Backend, error) {
	if cfg.SubscriptionID == "" || cfg.ResourceGroup == "" {
		return nil, fmt.Errorf("azure cache: SubscriptionID + ResourceGroup required")
	}
	if cfg.Credential == nil {
		return nil, fmt.Errorf("azure cache: TokenCredential required")
	}
	loc := cfg.Location
	if loc == "" {
		loc = "eastus"
	}
	factory, err := armredis.NewClientFactory(cfg.SubscriptionID, cfg.Credential, cfg.ClientOptions)
	if err != nil {
		return nil, fmt.Errorf("azure cache client factory: %w", err)
	}
	return &Backend{
		c:             factory.NewClient(),
		subscription:  cfg.SubscriptionID,
		resourceGroup: cfg.ResourceGroup,
		location:      loc,
	}, nil
}

var _ domain.Cache = (*Backend)(nil)

func domainStatus(s string) domain.Status {
	switch strings.ToLower(s) {
	case "succeeded":
		return domain.StatusAvailable
	case "creating", "provisioning":
		return domain.StatusCreating
	case "updating", "scaling":
		return domain.StatusModifying
	case "restarting":
		return domain.StatusRebooting
	case "deleting", "disabled":
		return domain.StatusDeleting
	default:
		return domain.StatusCreating
	}
}

func (b *Backend) toDomain(in *armredis.ResourceInfo) domain.Instance {
	if in == nil || in.Name == nil {
		return domain.Instance{}
	}
	out := domain.Instance{
		Name: *in.Name,
	}
	if in.Properties != nil {
		if in.Properties.RedisVersion != nil {
			out.EngineVersion = *in.Properties.RedisVersion
		}
		if in.Properties.ProvisioningState != nil {
			out.Status = domainStatus(string(*in.Properties.ProvisioningState))
		}
		if in.Properties.HostName != nil && out.Status == domain.StatusAvailable {
			port := 6379
			if in.Properties.Port != nil {
				port = int(*in.Properties.Port)
			}
			out.Connection = domain.Connection{
				Host:          *in.Properties.HostName,
				Port:          port,
				EngineVersion: out.EngineVersion,
			}
		}
	}
	if in.Properties != nil && in.Properties.SKU != nil && in.Properties.SKU.Name != nil {
		out.NodeType = string(*in.Properties.SKU.Name)
	}
	return out
}

func (b *Backend) CreateInstance(ctx context.Context, name string, opt domain.CreateInstanceOptions) (domain.CreateInstanceResult, error) {
	sku := armredis.SKUNameBasic
	if opt.NodeType != "" {
		sku = armredis.SKUName(opt.NodeType)
	}
	family := armredis.SKUFamilyC
	capacity := int32(0)
	in := armredis.CreateParameters{
		Location: &b.location,
		Properties: &armredis.CreateProperties{
			SKU: &armredis.SKU{
				Name:     &sku,
				Family:   &family,
				Capacity: &capacity,
			},
		},
	}
	if _, err := b.c.BeginCreate(ctx, b.resourceGroup, name, in, nil); err != nil {
		return domain.CreateInstanceResult{}, translateErr(err, name)
	}
	revealed := ""
	if opt.AuthToken == "" {
		revealed = newToken()
	}
	return domain.CreateInstanceResult{
		Instance: domain.Instance{
			Name:          name,
			EngineVersion: opt.EngineVersion,
			NodeType:      string(sku),
			Status:        domain.StatusCreating,
			CreatedAt:     time.Now().UTC(),
		},
		AuthToken: revealed,
	}, nil
}

func (b *Backend) DeleteInstance(ctx context.Context, name string) error {
	if _, err := b.c.BeginDelete(ctx, b.resourceGroup, name, nil); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) DescribeInstance(ctx context.Context, name string) (domain.Instance, error) {
	out, err := b.c.Get(ctx, b.resourceGroup, name, nil)
	if err != nil {
		return domain.Instance{}, translateErr(err, name)
	}
	return b.toDomain(&out.ResourceInfo), nil
}

func (b *Backend) ListInstances(ctx context.Context, opt domain.ListInstancesOptions) (domain.ListInstancesResult, error) {
	pager := b.c.NewListByResourceGroupPager(b.resourceGroup, nil)
	res := domain.ListInstancesResult{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return res, translateErr(err, "")
		}
		for _, r := range page.Value {
			if r.Name == nil {
				continue
			}
			if opt.Prefix != "" && !strings.HasPrefix(*r.Name, opt.Prefix) {
				continue
			}
			res.Instances = append(res.Instances, b.toDomain(r))
			if opt.MaxResults > 0 && len(res.Instances) >= opt.MaxResults {
				return res, nil
			}
		}
	}
	return res, nil
}

func (b *Backend) ModifyInstance(ctx context.Context, name string, opt domain.ModifyInstanceOptions) error {
	patch := armredis.UpdateParameters{
		Properties: &armredis.UpdateProperties{},
	}
	if opt.NodeType != "" {
		sku := armredis.SKUName(opt.NodeType)
		family := armredis.SKUFamilyC
		capacity := int32(0)
		patch.Properties.SKU = &armredis.SKU{
			Name:     &sku,
			Family:   &family,
			Capacity: &capacity,
		}
	}
	if _, err := b.c.BeginUpdate(ctx, b.resourceGroup, name, patch, nil); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) RebootInstance(ctx context.Context, name string) error {
	in := armredis.RebootParameters{
		RebootType: ptr(armredis.RebootTypePrimaryNode),
	}
	if _, err := b.c.ForceReboot(ctx, b.resourceGroup, name, in, nil); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func translateErr(err error, name string) error {
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		switch re.StatusCode {
		case 404:
			return domain.NoSuchInstance(name)
		case 409:
			return domain.InstanceAlreadyExists(name)
		}
	}
	return fmt.Errorf("azureredis %q: %w", name, err)
}

func ptr[T any](v T) *T { return &v }

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
