// Package azure is the Azure Container Apps passthrough backend.
package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

	"github.com/e6qu/shimanism/internal/functions/domain"
)

type Config struct {
	SubscriptionID       string
	ResourceGroup        string
	Location             string
	ManagedEnvironmentID string // required by Container Apps
	Credential           azcore.TokenCredential
}

type Backend struct {
	c             *armappcontainers.ContainerAppsClient
	subscription  string
	resourceGroup string
	location      string
	envID         string
}

func New(cfg Config) (*Backend, error) {
	if cfg.SubscriptionID == "" || cfg.ResourceGroup == "" || cfg.ManagedEnvironmentID == "" {
		return nil, fmt.Errorf("azure functions: SubscriptionID + ResourceGroup + ManagedEnvironmentID required")
	}
	if cfg.Credential == nil {
		return nil, fmt.Errorf("azure functions: TokenCredential required")
	}
	loc := cfg.Location
	if loc == "" {
		loc = "eastus"
	}
	factory, err := armappcontainers.NewClientFactory(cfg.SubscriptionID, cfg.Credential, nil)
	if err != nil {
		return nil, fmt.Errorf("azure containerapps factory: %w", err)
	}
	return &Backend{
		c:             factory.NewContainerAppsClient(),
		subscription:  cfg.SubscriptionID,
		resourceGroup: cfg.ResourceGroup,
		location:      loc,
		envID:         cfg.ManagedEnvironmentID,
	}, nil
}

var _ domain.Functions = (*Backend)(nil)

func domainStatus(state armappcontainers.ContainerAppProvisioningState) domain.Status {
	switch state {
	case armappcontainers.ContainerAppProvisioningStateSucceeded:
		return domain.StatusAvailable
	case armappcontainers.ContainerAppProvisioningStateInProgress:
		return domain.StatusCreating
	case armappcontainers.ContainerAppProvisioningStateDeleting:
		return domain.StatusDeleting
	case armappcontainers.ContainerAppProvisioningStateCanceled, armappcontainers.ContainerAppProvisioningStateFailed:
		return domain.StatusUnknown
	default:
		return domain.StatusCreating
	}
}

func (b *Backend) toDomain(in *armappcontainers.ContainerApp) domain.Function {
	if in == nil || in.Name == nil {
		return domain.Function{}
	}
	fn := domain.Function{
		Name: *in.Name,
	}
	if in.Properties != nil {
		if in.Properties.ProvisioningState != nil {
			fn.Status = domainStatus(*in.Properties.ProvisioningState)
		}
		if in.Properties.Configuration != nil && in.Properties.Configuration.Ingress != nil && in.Properties.Configuration.Ingress.Fqdn != nil {
			if fn.Status == domain.StatusAvailable {
				fn.Endpoint = domain.Endpoint{URL: "https://" + *in.Properties.Configuration.Ingress.Fqdn}
			}
		}
		if in.Properties.Template != nil && len(in.Properties.Template.Containers) > 0 {
			c := in.Properties.Template.Containers[0]
			if c.Image != nil {
				fn.Image = *c.Image
			}
			if c.Resources != nil {
				if c.Resources.Memory != nil {
					fn.MemoryBytes = parseMemoryString(*c.Resources.Memory)
				}
				if c.Resources.CPU != nil {
					fn.CPUMilliCores = int(*c.Resources.CPU * 1000)
				}
			}
			if len(c.Env) > 0 {
				fn.Environment = map[string]string{}
				for _, e := range c.Env {
					if e.Name != nil && e.Value != nil {
						fn.Environment[*e.Name] = *e.Value
					}
				}
			}
		}
	}
	return fn
}

func parseMemoryString(s string) int64 {
	// Azure uses Gi / Mi forms.
	if strings.HasSuffix(s, "Gi") {
		var n float64
		_, _ = fmt.Sscanf(s, "%fGi", &n)
		return int64(n * 1024 * 1024 * 1024)
	}
	if strings.HasSuffix(s, "Mi") {
		var n int64
		_, _ = fmt.Sscanf(s, "%dMi", &n)
		return n * 1024 * 1024
	}
	return 0
}

func (b *Backend) CreateFunction(ctx context.Context, name string, opt domain.CreateFunctionOptions) (domain.Function, error) {
	if opt.Role != "" {
		return domain.Function{}, domain.InvalidArgument("Role is AWS Lambda-specific; not supported on Azure Container Apps")
	}
	if opt.Publish {
		return domain.Function{}, domain.InvalidArgument("Publish is AWS Lambda-specific; not supported on Azure Container Apps")
	}
	image := opt.Image
	container := &armappcontainers.Container{
		Name:  &name,
		Image: &image,
	}
	if len(opt.Environment) > 0 {
		for k, v := range opt.Environment {
			kCopy, vCopy := k, v
			container.Env = append(container.Env, &armappcontainers.EnvironmentVar{
				Name: &kCopy, Value: &vCopy,
			})
		}
	}
	if opt.MemoryBytes > 0 || opt.CPUMilliCores > 0 {
		container.Resources = &armappcontainers.ContainerResources{}
		if opt.CPUMilliCores > 0 {
			cpu := float64(opt.CPUMilliCores) / 1000.0
			container.Resources.CPU = &cpu
		}
		if opt.MemoryBytes > 0 {
			mem := fmt.Sprintf("%dMi", opt.MemoryBytes/(1024*1024))
			container.Resources.Memory = &mem
		}
	}
	envID := b.envID
	props := &armappcontainers.ContainerAppProperties{
		ManagedEnvironmentID: &envID,
		Template: &armappcontainers.Template{
			Containers: []*armappcontainers.Container{container},
		},
	}
	app := armappcontainers.ContainerApp{
		Location:   &b.location,
		Properties: props,
	}
	if _, err := b.c.BeginCreateOrUpdate(ctx, b.resourceGroup, name, app, nil); err != nil {
		return domain.Function{}, translateErr(err, name)
	}
	return domain.Function{
		Name:          name,
		Image:         opt.Image,
		Status:        domain.StatusCreating,
		Environment:   opt.Environment,
		MemoryBytes:   opt.MemoryBytes,
		CPUMilliCores: opt.CPUMilliCores,
		CreatedAt:     time.Now().UTC(),
	}, nil
}

func (b *Backend) DeleteFunction(ctx context.Context, name string) error {
	if _, err := b.c.BeginDelete(ctx, b.resourceGroup, name, nil); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) DescribeFunction(ctx context.Context, name string) (domain.Function, error) {
	out, err := b.c.Get(ctx, b.resourceGroup, name, nil)
	if err != nil {
		return domain.Function{}, translateErr(err, name)
	}
	return b.toDomain(&out.ContainerApp), nil
}

func (b *Backend) ListFunctions(ctx context.Context, opt domain.ListFunctionsOptions) (domain.ListFunctionsResult, error) {
	pager := b.c.NewListByResourceGroupPager(b.resourceGroup, nil)
	res := domain.ListFunctionsResult{}
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
			res.Functions = append(res.Functions, b.toDomain(r))
			if opt.MaxResults > 0 && len(res.Functions) >= opt.MaxResults {
				return res, nil
			}
		}
	}
	return res, nil
}

func (b *Backend) UpdateFunction(ctx context.Context, name string, opt domain.UpdateFunctionOptions) error {
	// ContainerApps Update is a PATCH that replaces the spec.
	// Build a minimal patch with only the fields the caller set.
	patch := armappcontainers.ContainerAppProperties{}
	if opt.Image != "" || opt.Environment != nil || opt.MemoryBytes > 0 || opt.CPUMilliCores > 0 {
		container := &armappcontainers.Container{Name: &name}
		if opt.Image != "" {
			container.Image = &opt.Image
		}
		if len(opt.Environment) > 0 {
			for k, v := range opt.Environment {
				kCopy, vCopy := k, v
				container.Env = append(container.Env, &armappcontainers.EnvironmentVar{
					Name: &kCopy, Value: &vCopy,
				})
			}
		}
		if opt.MemoryBytes > 0 || opt.CPUMilliCores > 0 {
			container.Resources = &armappcontainers.ContainerResources{}
			if opt.CPUMilliCores > 0 {
				cpu := float64(opt.CPUMilliCores) / 1000.0
				container.Resources.CPU = &cpu
			}
			if opt.MemoryBytes > 0 {
				mem := fmt.Sprintf("%dMi", opt.MemoryBytes/(1024*1024))
				container.Resources.Memory = &mem
			}
		}
		patch.Template = &armappcontainers.Template{
			Containers: []*armappcontainers.Container{container},
		}
	}
	app := armappcontainers.ContainerApp{Properties: &patch}
	if _, err := b.c.BeginUpdate(ctx, b.resourceGroup, name, app, nil); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func translateErr(err error, name string) error {
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		switch re.StatusCode {
		case 404:
			return domain.NoSuchFunction(name)
		case 409:
			return domain.FunctionAlreadyExists(name)
		}
	}
	return fmt.Errorf("containerapps %q: %w", name, err)
}
