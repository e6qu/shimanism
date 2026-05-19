// Package aws is the AWS API Gateway v2 passthrough backend.
package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
)

type Backend struct {
	c *apigatewayv2.Client
	// names→apiId mapping helper. We don't persist this; AWS
	// assigns ApiIds and the shim treats the Name as the
	// caller-supplied key. Lookups iterate GetApis.
}

func New(c *apigatewayv2.Client) *Backend {
	return &Backend{c: c}
}

var _ domain.APIGateway = (*Backend)(nil)

// apiIDByName scans existing APIs to find one with the given Name.
// AWS API Gateway uses ApiId (not Name) for all path-bound ops.
func (b *Backend) apiIDByName(ctx context.Context, name string) (string, error) {
	out, err := b.c.GetApis(ctx, &apigatewayv2.GetApisInput{})
	if err != nil {
		return "", err
	}
	for _, a := range out.Items {
		if awsapi.ToString(a.Name) == name {
			return awsapi.ToString(a.ApiId), nil
		}
	}
	return "", domain.NoSuchGateway(name)
}

func (b *Backend) toDomain(in apigwtypes.Api) domain.Gateway {
	g := domain.Gateway{
		Name:   awsapi.ToString(in.Name),
		Status: domain.StatusAvailable, // AWS APIs are "available" once created
	}
	if in.CreatedDate != nil {
		g.CreatedAt = *in.CreatedDate
	}
	if in.ApiEndpoint != nil {
		g.Endpoint = domain.Endpoint{URL: awsapi.ToString(in.ApiEndpoint)}
	}
	return g
}

func (b *Backend) CreateGateway(ctx context.Context, name string, opt domain.CreateGatewayOptions) (domain.Gateway, error) {
	out, err := b.c.CreateApi(ctx, &apigatewayv2.CreateApiInput{
		Name:         awsapi.String(name),
		ProtocolType: apigwtypes.ProtocolTypeHttp,
	})
	if err != nil {
		return domain.Gateway{}, translateErr(err, name)
	}
	if len(opt.Routes) > 0 {
		apiID := awsapi.ToString(out.ApiId)
		if err := b.publishRoutes(ctx, apiID, opt.Routes); err != nil {
			return domain.Gateway{}, err
		}
	}
	return domain.Gateway{
		Name:      name,
		Status:    domain.StatusAvailable,
		Endpoint:  domain.Endpoint{URL: awsapi.ToString(out.ApiEndpoint)},
		Routes:    opt.Routes,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func (b *Backend) DeleteGateway(ctx context.Context, name string) error {
	apiID, err := b.apiIDByName(ctx, name)
	if err != nil {
		return err
	}
	if _, err := b.c.DeleteApi(ctx, &apigatewayv2.DeleteApiInput{
		ApiId: awsapi.String(apiID),
	}); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) DescribeGateway(ctx context.Context, name string) (domain.Gateway, error) {
	apiID, err := b.apiIDByName(ctx, name)
	if err != nil {
		return domain.Gateway{}, err
	}
	out, err := b.c.GetApi(ctx, &apigatewayv2.GetApiInput{ApiId: awsapi.String(apiID)})
	if err != nil {
		return domain.Gateway{}, translateErr(err, name)
	}
	g := b.toDomain(apigwtypes.Api{
		ApiId:        out.ApiId,
		Name:         out.Name,
		ApiEndpoint:  out.ApiEndpoint,
		CreatedDate:  out.CreatedDate,
		ProtocolType: out.ProtocolType,
	})
	// List routes.
	if rOut, err := b.c.GetRoutes(ctx, &apigatewayv2.GetRoutesInput{ApiId: awsapi.String(apiID)}); err == nil {
		for _, rt := range rOut.Items {
			method, path, _ := splitRouteKey(awsapi.ToString(rt.RouteKey))
			g.Routes = append(g.Routes, domain.Route{Method: method, Path: path})
		}
	}
	return g, nil
}

func (b *Backend) ListGateways(ctx context.Context, opt domain.ListGatewaysOptions) (domain.ListGatewaysResult, error) {
	out, err := b.c.GetApis(ctx, &apigatewayv2.GetApisInput{})
	if err != nil {
		return domain.ListGatewaysResult{}, translateErr(err, "")
	}
	res := domain.ListGatewaysResult{}
	for _, a := range out.Items {
		name := awsapi.ToString(a.Name)
		if opt.Prefix != "" && !strings.HasPrefix(name, opt.Prefix) {
			continue
		}
		res.Gateways = append(res.Gateways, b.toDomain(a))
		if opt.MaxResults > 0 && len(res.Gateways) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) DeployGateway(ctx context.Context, name string, opt domain.DeployGatewayOptions) error {
	apiID, err := b.apiIDByName(ctx, name)
	if err != nil {
		return err
	}
	// Delete existing routes + integrations.
	if rOut, err := b.c.GetRoutes(ctx, &apigatewayv2.GetRoutesInput{ApiId: awsapi.String(apiID)}); err == nil {
		for _, rt := range rOut.Items {
			_, _ = b.c.DeleteRoute(ctx, &apigatewayv2.DeleteRouteInput{
				ApiId:   awsapi.String(apiID),
				RouteId: rt.RouteId,
			})
		}
	}
	if iOut, err := b.c.GetIntegrations(ctx, &apigatewayv2.GetIntegrationsInput{ApiId: awsapi.String(apiID)}); err == nil {
		for _, it := range iOut.Items {
			_, _ = b.c.DeleteIntegration(ctx, &apigatewayv2.DeleteIntegrationInput{
				ApiId:         awsapi.String(apiID),
				IntegrationId: it.IntegrationId,
			})
		}
	}
	if err := b.publishRoutes(ctx, apiID, opt.Routes); err != nil {
		return err
	}
	_, err = b.c.CreateDeployment(ctx, &apigatewayv2.CreateDeploymentInput{
		ApiId: awsapi.String(apiID),
	})
	if err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) publishRoutes(ctx context.Context, apiID string, routes []domain.Route) error {
	for _, rt := range routes {
		// Integration first, then route bound to the integration.
		iOut, err := b.c.CreateIntegration(ctx, &apigatewayv2.CreateIntegrationInput{
			ApiId:             awsapi.String(apiID),
			IntegrationType:   apigwtypes.IntegrationTypeHttpProxy,
			IntegrationUri:    awsapi.String(rt.Backend),
			IntegrationMethod: awsapi.String(rt.Method),
		})
		if err != nil {
			return fmt.Errorf("CreateIntegration: %w", err)
		}
		if _, err := b.c.CreateRoute(ctx, &apigatewayv2.CreateRouteInput{
			ApiId:    awsapi.String(apiID),
			RouteKey: awsapi.String(rt.Method + " " + rt.Path),
			Target:   awsapi.String("integrations/" + awsapi.ToString(iOut.IntegrationId)),
		}); err != nil {
			return fmt.Errorf("CreateRoute: %w", err)
		}
	}
	return nil
}

func translateErr(err error, name string) error {
	var nfe *apigwtypes.NotFoundException
	if errors.As(err, &nfe) {
		return domain.NoSuchGateway(name)
	}
	var ce *apigwtypes.ConflictException
	if errors.As(err, &ce) {
		return domain.GatewayAlreadyExists(name)
	}
	return fmt.Errorf("apigwv2 %q: %w", name, err)
}

func splitRouteKey(rk string) (method, path string, ok bool) {
	i := strings.IndexByte(rk, ' ')
	if i < 0 {
		return "", "", false
	}
	return rk[:i], rk[i+1:], true
}
