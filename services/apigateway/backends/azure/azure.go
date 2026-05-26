// Package azure is the Azure API Management backend.
//
// APIM's data model: `Service` → `Api` → `Operations`. The
// shim's Gateway maps to an APIM Api inside a pre-existing
// Service; Routes become Operations. DeployGateway replaces
// operations atomically (well, sequentially across REST calls).
package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement/v3"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
)

type Config struct {
	SubscriptionID string
	ResourceGroup  string
	ServiceName    string // APIM Service (pre-existing) the shim's Gateways live under
	Credential     azcore.TokenCredential
	// ClientOptions, if non-nil, is forwarded to the armapimanagement
	// factory. Used by the sockerless test lane to point the ARM
	// endpoint at a local simulator.
	ClientOptions *arm.ClientOptions
}

type Backend struct {
	api           *armapimanagement.APIClient
	op            *armapimanagement.APIOperationClient
	svc           *armapimanagement.ServiceClient
	subscription  string
	resourceGroup string
	serviceName   string
}

func New(cfg Config) (*Backend, error) {
	if cfg.SubscriptionID == "" || cfg.ResourceGroup == "" || cfg.ServiceName == "" {
		return nil, fmt.Errorf("azure apigateway: SubscriptionID + ResourceGroup + ServiceName required")
	}
	if cfg.Credential == nil {
		return nil, fmt.Errorf("azure apigateway: TokenCredential required")
	}
	factory, err := armapimanagement.NewClientFactory(cfg.SubscriptionID, cfg.Credential, cfg.ClientOptions)
	if err != nil {
		return nil, fmt.Errorf("azure apim factory: %w", err)
	}
	return &Backend{
		api:           factory.NewAPIClient(),
		op:            factory.NewAPIOperationClient(),
		svc:           factory.NewServiceClient(),
		subscription:  cfg.SubscriptionID,
		resourceGroup: cfg.ResourceGroup,
		serviceName:   cfg.ServiceName,
	}, nil
}

var _ domain.APIGateway = (*Backend)(nil)

func (b *Backend) CreateGateway(ctx context.Context, name string, opt domain.CreateGatewayOptions) (domain.Gateway, error) {
	displayName := name
	path := "/" + name
	if _, err := b.api.BeginCreateOrUpdate(ctx, b.resourceGroup, b.serviceName, name, armapimanagement.APICreateOrUpdateParameter{
		Properties: &armapimanagement.APICreateOrUpdateProperties{
			DisplayName: &displayName,
			Path:        &path,
			Protocols:   []*armapimanagement.Protocol{ptr(armapimanagement.ProtocolHTTPS)},
		},
	}, nil); err != nil {
		return domain.Gateway{}, translateErr(err, name)
	}
	if len(opt.Routes) > 0 {
		if err := b.deployOps(ctx, name, opt.Routes); err != nil {
			return domain.Gateway{}, err
		}
	}
	return domain.Gateway{
		Name:      name,
		Status:    domain.StatusCreating,
		Routes:    opt.Routes,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func (b *Backend) DeleteGateway(ctx context.Context, name string) error {
	// armapimanagement v3's APIClient.BeginDelete takes an ifMatch
	// etag (matching the entity's current state — "*" means
	// unconditional, the canonical migration-tool choice) and an
	// optional deleteRevisions flag (we pass nil = default to keep
	// revisions intact, which matches the v1 SDK behaviour). The
	// returned poller is awaited until completion so the delete is
	// observable through subsequent Describe / List calls.
	poller, err := b.api.BeginDelete(ctx, b.resourceGroup, b.serviceName, name, "*", nil)
	if err != nil {
		return translateErr(err, name)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) DescribeGateway(ctx context.Context, name string) (domain.Gateway, error) {
	out, err := b.api.Get(ctx, b.resourceGroup, b.serviceName, name, nil)
	if err != nil {
		return domain.Gateway{}, translateErr(err, name)
	}
	g := domain.Gateway{
		Name:   name,
		Status: domain.StatusAvailable, // APIM Apis don't have a granular status; assume available
	}
	if out.Properties != nil && out.Properties.ServiceURL != nil {
		g.Endpoint = domain.Endpoint{URL: *out.Properties.ServiceURL}
	}
	// Build routes from the Operations list.
	pager := b.op.NewListByAPIPager(b.resourceGroup, b.serviceName, name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			break
		}
		for _, op := range page.Value {
			if op.Properties == nil {
				continue
			}
			method := ""
			if op.Properties.Method != nil {
				method = *op.Properties.Method
			}
			path := ""
			if op.Properties.URLTemplate != nil {
				path = *op.Properties.URLTemplate
			}
			g.Routes = append(g.Routes, domain.Route{Method: method, Path: path})
		}
	}
	return g, nil
}

func (b *Backend) ListGateways(ctx context.Context, opt domain.ListGatewaysOptions) (domain.ListGatewaysResult, error) {
	pager := b.api.NewListByServicePager(b.resourceGroup, b.serviceName, nil)
	res := domain.ListGatewaysResult{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return res, translateErr(err, "")
		}
		for _, a := range page.Value {
			if a.Name == nil {
				continue
			}
			if opt.Prefix != "" && !strings.HasPrefix(*a.Name, opt.Prefix) {
				continue
			}
			g, _ := b.DescribeGateway(ctx, *a.Name)
			res.Gateways = append(res.Gateways, g)
			if opt.MaxResults > 0 && len(res.Gateways) >= opt.MaxResults {
				return res, nil
			}
		}
	}
	return res, nil
}

func (b *Backend) DeployGateway(ctx context.Context, name string, opt domain.DeployGatewayOptions) error {
	// Wipe existing operations.
	pager := b.op.NewListByAPIPager(b.resourceGroup, b.serviceName, name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			break
		}
		for _, o := range page.Value {
			if o.Name != nil {
				_, _ = b.op.Delete(ctx, b.resourceGroup, b.serviceName, name, *o.Name, "*", nil)
			}
		}
	}
	return b.deployOps(ctx, name, opt.Routes)
}

func (b *Backend) deployOps(ctx context.Context, api string, routes []domain.Route) error {
	for i, rt := range routes {
		opID := fmt.Sprintf("route-%d", i)
		display := fmt.Sprintf("%s %s", rt.Method, rt.Path)
		method := rt.Method
		path := rt.Path
		if _, err := b.op.CreateOrUpdate(ctx, b.resourceGroup, b.serviceName, api, opID,
			armapimanagement.OperationContract{
				Properties: &armapimanagement.OperationContractProperties{
					DisplayName: &display,
					Method:      &method,
					URLTemplate: &path,
				},
			}, nil); err != nil {
			return fmt.Errorf("CreateOrUpdate operation %s: %w", opID, err)
		}
	}
	return nil
}

func translateErr(err error, name string) error {
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		switch re.StatusCode {
		case 404:
			return domain.NoSuchGateway(name)
		case 409:
			return domain.GatewayAlreadyExists(name)
		}
	}
	return fmt.Errorf("apim %q: %w", name, err)
}

func ptr[T any](v T) *T { return &v }
