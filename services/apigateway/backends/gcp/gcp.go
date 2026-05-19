// Package gcp is the GCP API Gateway backend.
//
// GCP API Gateway uses a 3-layered model: `Api` →
// `ApiConfig` (the OpenAPI/gRPC spec) → `Gateway` (deploys a
// config to a region). The shim's DeployGateway translates the
// domain Route slice into an OpenAPI 2.0 spec document, creates
// a new ApiConfig with that document, and updates the Gateway to
// point at it.
//
// At this phase the implementation handles the lifecycle ops but
// emits a minimal OpenAPI document covering only the Route's
// method + path + backend. Full GCP API Gateway fidelity (auth,
// quotas, x-google-backend variations) is out of intersection.
package gcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	apigwapi "google.golang.org/api/apigateway/v1"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
)

type Config struct {
	ProjectID string
	Region    string // default us-central1
}

type Backend struct {
	svc     *apigwapi.Service
	project string
	region  string
}

func New(svc *apigwapi.Service, cfg Config) *Backend {
	r := cfg.Region
	if r == "" {
		r = "us-central1"
	}
	return &Backend{svc: svc, project: cfg.ProjectID, region: r}
}

var _ domain.APIGateway = (*Backend)(nil)

func (b *Backend) parent() string {
	return fmt.Sprintf("projects/%s/locations/%s", b.project, b.region)
}

func (b *Backend) apiName(name string) string {
	return fmt.Sprintf("projects/%s/locations/global/apis/%s", b.project, name)
}

func (b *Backend) gatewayName(name string) string {
	return fmt.Sprintf("%s/gateways/%s", b.parent(), name)
}

func nameFromPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (b *Backend) CreateGateway(ctx context.Context, name string, opt domain.CreateGatewayOptions) (domain.Gateway, error) {
	// 1. Create the Api.
	if _, err := b.svc.Projects.Locations.Apis.Create(
		fmt.Sprintf("projects/%s/locations/global", b.project),
		&apigwapi.ApigatewayApi{},
	).ApiId(name).Context(ctx).Do(); err != nil {
		return domain.Gateway{}, translateErr(err, name)
	}
	// 2. ApiConfig will be created via DeployGateway later. The
	// Gateway itself can't exist without an ApiConfig; at this
	// phase return Status=Creating and defer the Gateway resource
	// creation to DeployGateway.
	if len(opt.Routes) > 0 {
		if err := b.DeployGateway(ctx, name, domain.DeployGatewayOptions(opt)); err != nil {
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
	_, _ = b.svc.Projects.Locations.Gateways.Delete(b.gatewayName(name)).Context(ctx).Do()
	_, _ = b.svc.Projects.Locations.Apis.Configs.Delete(
		fmt.Sprintf("%s/configs/cfg-%s", b.apiName(name), name),
	).Context(ctx).Do()
	if _, err := b.svc.Projects.Locations.Apis.Delete(b.apiName(name)).Context(ctx).Do(); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) DescribeGateway(ctx context.Context, name string) (domain.Gateway, error) {
	gw, err := b.svc.Projects.Locations.Gateways.Get(b.gatewayName(name)).Context(ctx).Do()
	if err != nil {
		return domain.Gateway{}, translateErr(err, name)
	}
	out := domain.Gateway{
		Name:   nameFromPath(gw.Name),
		Status: statusFromGCP(gw.State),
	}
	if t, err := time.Parse(time.RFC3339, gw.CreateTime); err == nil {
		out.CreatedAt = t
	}
	if out.Status == domain.StatusAvailable && gw.DefaultHostname != "" {
		out.Endpoint = domain.Endpoint{URL: "https://" + gw.DefaultHostname}
	}
	return out, nil
}

func (b *Backend) ListGateways(ctx context.Context, opt domain.ListGatewaysOptions) (domain.ListGatewaysResult, error) {
	out, err := b.svc.Projects.Locations.Gateways.List(b.parent()).Context(ctx).Do()
	if err != nil {
		return domain.ListGatewaysResult{}, translateErr(err, "")
	}
	res := domain.ListGatewaysResult{}
	for _, gw := range out.Gateways {
		name := nameFromPath(gw.Name)
		if opt.Prefix != "" && !strings.HasPrefix(name, opt.Prefix) {
			continue
		}
		g, _ := b.DescribeGateway(ctx, name)
		res.Gateways = append(res.Gateways, g)
		if opt.MaxResults > 0 && len(res.Gateways) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) DeployGateway(ctx context.Context, name string, opt domain.DeployGatewayOptions) error {
	// Build the OpenAPI 2.0 document for these routes.
	doc := buildOpenAPI(name, opt.Routes)
	cfgID := fmt.Sprintf("cfg-%d", time.Now().UnixNano())
	cfg := &apigwapi.ApigatewayApiConfig{
		OpenapiDocuments: []*apigwapi.ApigatewayApiConfigOpenApiDocument{{
			Document: &apigwapi.ApigatewayApiConfigFile{
				Contents: doc,
				Path:     "openapi.yaml",
			},
		}},
	}
	if _, err := b.svc.Projects.Locations.Apis.Configs.Create(b.apiName(name), cfg).
		ApiConfigId(cfgID).Context(ctx).Do(); err != nil {
		return translateErr(err, name)
	}
	// Create/Update the Gateway pointing at the new ApiConfig.
	gw := &apigwapi.ApigatewayGateway{
		ApiConfig: fmt.Sprintf("%s/configs/%s", b.apiName(name), cfgID),
	}
	if _, err := b.svc.Projects.Locations.Gateways.Create(b.parent(), gw).GatewayId(name).Context(ctx).Do(); err != nil {
		// Already exists? Patch instead.
		_, patchErr := b.svc.Projects.Locations.Gateways.Patch(b.gatewayName(name), gw).UpdateMask("apiConfig").Context(ctx).Do()
		if patchErr != nil {
			return translateErr(err, name)
		}
	}
	return nil
}

func buildOpenAPI(name string, routes []domain.Route) string {
	var sb strings.Builder
	sb.WriteString("swagger: \"2.0\"\n")
	fmt.Fprintf(&sb, "info:\n  title: %s\n  version: 1.0.0\nschemes:\n  - https\npaths:\n", name)
	for _, r := range routes {
		method := strings.ToLower(r.Method)
		if method == "" || method == "any" {
			method = "get"
		}
		fmt.Fprintf(&sb, "  %s:\n    %s:\n      operationId: %s_%s\n",
			r.Path, method, method, strings.ReplaceAll(r.Path, "/", "_"))
		sb.WriteString("      responses:\n        \"200\":\n          description: OK\n")
		fmt.Fprintf(&sb, "      x-google-backend:\n        address: %s\n", r.Backend)
	}
	return sb.String()
}

func statusFromGCP(s string) domain.Status {
	switch s {
	case "ACTIVE":
		return domain.StatusAvailable
	case "CREATING":
		return domain.StatusCreating
	case "UPDATING":
		return domain.StatusUpdating
	case "DELETING":
		return domain.StatusDeleting
	default:
		return domain.StatusCreating
	}
}

func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "404"), strings.Contains(msg, "notFound"):
		return domain.NoSuchGateway(name)
	case strings.Contains(msg, "409"), strings.Contains(msg, "alreadyExists"):
		return domain.GatewayAlreadyExists(name)
	}
	return fmt.Errorf("apigateway %q: %w", name, err)
}
