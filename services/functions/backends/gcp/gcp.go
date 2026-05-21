// Package gcp is the GCP Cloud Run passthrough backend.
package gcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	runapi "google.golang.org/api/run/v2"

	"github.com/e6qu/shimanism/internal/functions/domain"
)

type Config struct {
	ProjectID string
	Region    string // default us-central1
}

type Backend struct {
	svc     *runapi.Service
	project string
	region  string
}

func New(svc *runapi.Service, cfg Config) *Backend {
	r := cfg.Region
	if r == "" {
		r = "us-central1"
	}
	return &Backend{svc: svc, project: cfg.ProjectID, region: r}
}

var _ domain.Functions = (*Backend)(nil)

func (b *Backend) parent() string {
	return fmt.Sprintf("projects/%s/locations/%s", b.project, b.region)
}

func (b *Backend) servicePath(name string) string {
	return fmt.Sprintf("%s/services/%s", b.parent(), name)
}

func nameFromPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func domainStatus(svc *runapi.GoogleCloudRunV2Service) domain.Status {
	if svc.DeleteTime != "" {
		return domain.StatusDeleting
	}
	for _, c := range svc.Conditions {
		if c.Type == "Ready" {
			switch c.State {
			case "CONDITION_SUCCEEDED":
				return domain.StatusAvailable
			case "CONDITION_FAILED":
				return domain.StatusUnknown
			default:
				return domain.StatusCreating
			}
		}
	}
	if svc.LatestReadyRevision != "" {
		return domain.StatusAvailable
	}
	return domain.StatusCreating
}

func (b *Backend) toDomain(in *runapi.GoogleCloudRunV2Service) domain.Function {
	fn := domain.Function{
		Name:   nameFromPath(in.Name),
		Status: domainStatus(in),
	}
	if t, err := time.Parse(time.RFC3339, in.CreateTime); err == nil {
		fn.CreatedAt = t
	}
	if in.Template != nil && len(in.Template.Containers) > 0 {
		c := in.Template.Containers[0]
		fn.Image = c.Image
		if c.Resources != nil && len(c.Resources.Limits) > 0 {
			if mem, ok := c.Resources.Limits["memory"]; ok {
				fn.MemoryBytes = parseMemoryString(mem)
			}
			if cpu, ok := c.Resources.Limits["cpu"]; ok {
				fn.CPUMilliCores = parseCPUString(cpu)
			}
		}
		if len(c.Env) > 0 {
			fn.Environment = map[string]string{}
			for _, e := range c.Env {
				fn.Environment[e.Name] = e.Value
			}
		}
	}
	if in.Template != nil && in.Template.Timeout != "" {
		// Timeout is a Google duration string like "3s".
		fn.TimeoutSeconds = parseTimeoutSeconds(in.Template.Timeout)
	}
	if fn.Status == domain.StatusAvailable && in.Uri != "" {
		fn.Endpoint = domain.Endpoint{URL: in.Uri}
	}
	return fn
}

func parseMemoryString(s string) int64 {
	// Accepts "128Mi", "512Mi", "1Gi" forms. Best-effort.
	if strings.HasSuffix(s, "Mi") {
		var n int64
		_, _ = fmt.Sscanf(s, "%dMi", &n)
		return n * 1024 * 1024
	}
	if strings.HasSuffix(s, "Gi") {
		var n int64
		_, _ = fmt.Sscanf(s, "%dGi", &n)
		return n * 1024 * 1024 * 1024
	}
	return 0
}

func parseCPUString(s string) int {
	if strings.HasSuffix(s, "m") {
		var n int
		_, _ = fmt.Sscanf(s, "%dm", &n)
		return n
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err == nil {
		return int(f * 1000)
	}
	return 0
}

func parseTimeoutSeconds(s string) int {
	if strings.HasSuffix(s, "s") {
		var n int
		_, _ = fmt.Sscanf(s, "%ds", &n)
		return n
	}
	return 0
}

func (b *Backend) CreateFunction(ctx context.Context, name string, opt domain.CreateFunctionOptions) (domain.Function, error) {
	// Role + Publish are AWS Lambda-specific. Cross-cloud, the
	// destination's identity model (Cloud Run service-account, IAM
	// binding) replaces the function-level execution role. The shim
	// accepts the input attribute, doesn't apply it, and leaves the
	// migration-tool's responsibility to rebind identity on the
	// destination cloud (see PHASE_10_PLAN.md — IAM rebinding is a
	// follow-on phase). Same posture for Publish, which has no Cloud
	// Run analog (revisions are atomic; no separate "published"
	// concept).
	_ = opt.Role
	_ = opt.Publish
	container := &runapi.GoogleCloudRunV2Container{
		Image: opt.Image,
	}
	if len(opt.Environment) > 0 {
		for k, v := range opt.Environment {
			container.Env = append(container.Env, &runapi.GoogleCloudRunV2EnvVar{Name: k, Value: v})
		}
	}
	if opt.MemoryBytes > 0 || opt.CPUMilliCores > 0 {
		container.Resources = &runapi.GoogleCloudRunV2ResourceRequirements{Limits: map[string]string{}}
		if opt.MemoryBytes > 0 {
			container.Resources.Limits["memory"] = fmt.Sprintf("%dMi", opt.MemoryBytes/(1024*1024))
		}
		if opt.CPUMilliCores > 0 {
			container.Resources.Limits["cpu"] = fmt.Sprintf("%dm", opt.CPUMilliCores)
		}
	}
	template := &runapi.GoogleCloudRunV2RevisionTemplate{
		Containers: []*runapi.GoogleCloudRunV2Container{container},
	}
	if opt.TimeoutSeconds > 0 {
		template.Timeout = fmt.Sprintf("%ds", opt.TimeoutSeconds)
	}
	svc := &runapi.GoogleCloudRunV2Service{
		Template: template,
	}
	if _, err := b.svc.Projects.Locations.Services.Create(b.parent(), svc).ServiceId(name).Context(ctx).Do(); err != nil {
		return domain.Function{}, translateErr(err, name)
	}
	return domain.Function{
		Name:           name,
		Image:          opt.Image,
		Status:         domain.StatusCreating,
		Environment:    opt.Environment,
		MemoryBytes:    opt.MemoryBytes,
		CPUMilliCores:  opt.CPUMilliCores,
		TimeoutSeconds: opt.TimeoutSeconds,
		CreatedAt:      time.Now().UTC(),
	}, nil
}

func (b *Backend) DeleteFunction(ctx context.Context, name string) error {
	if _, err := b.svc.Projects.Locations.Services.Delete(b.servicePath(name)).Context(ctx).Do(); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) DescribeFunction(ctx context.Context, name string) (domain.Function, error) {
	in, err := b.svc.Projects.Locations.Services.Get(b.servicePath(name)).Context(ctx).Do()
	if err != nil {
		return domain.Function{}, translateErr(err, name)
	}
	return b.toDomain(in), nil
}

func (b *Backend) ListFunctions(ctx context.Context, opt domain.ListFunctionsOptions) (domain.ListFunctionsResult, error) {
	out, err := b.svc.Projects.Locations.Services.List(b.parent()).Context(ctx).Do()
	if err != nil {
		return domain.ListFunctionsResult{}, translateErr(err, "")
	}
	res := domain.ListFunctionsResult{}
	for _, s := range out.Services {
		name := nameFromPath(s.Name)
		if opt.Prefix != "" && !strings.HasPrefix(name, opt.Prefix) {
			continue
		}
		res.Functions = append(res.Functions, b.toDomain(s))
		if opt.MaxResults > 0 && len(res.Functions) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) UpdateFunction(ctx context.Context, name string, opt domain.UpdateFunctionOptions) error {
	// Cloud Run's replaceService overwrites the whole spec. Fetch
	// the current spec, mutate, then PATCH (the v2 SDK exposes
	// Patch).
	in, err := b.svc.Projects.Locations.Services.Get(b.servicePath(name)).Context(ctx).Do()
	if err != nil {
		return translateErr(err, name)
	}
	if in.Template == nil {
		in.Template = &runapi.GoogleCloudRunV2RevisionTemplate{}
	}
	if len(in.Template.Containers) == 0 {
		in.Template.Containers = []*runapi.GoogleCloudRunV2Container{{}}
	}
	c := in.Template.Containers[0]
	if opt.Image != "" {
		c.Image = opt.Image
	}
	if opt.Environment != nil {
		c.Env = c.Env[:0]
		for k, v := range opt.Environment {
			c.Env = append(c.Env, &runapi.GoogleCloudRunV2EnvVar{Name: k, Value: v})
		}
	}
	if opt.MemoryBytes > 0 || opt.CPUMilliCores > 0 {
		if c.Resources == nil {
			c.Resources = &runapi.GoogleCloudRunV2ResourceRequirements{Limits: map[string]string{}}
		}
		if c.Resources.Limits == nil {
			c.Resources.Limits = map[string]string{}
		}
		if opt.MemoryBytes > 0 {
			c.Resources.Limits["memory"] = fmt.Sprintf("%dMi", opt.MemoryBytes/(1024*1024))
		}
		if opt.CPUMilliCores > 0 {
			c.Resources.Limits["cpu"] = fmt.Sprintf("%dm", opt.CPUMilliCores)
		}
	}
	if opt.TimeoutSeconds > 0 {
		in.Template.Timeout = fmt.Sprintf("%ds", opt.TimeoutSeconds)
	}
	if _, err := b.svc.Projects.Locations.Services.Patch(b.servicePath(name), in).Context(ctx).Do(); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "404"), strings.Contains(msg, "notFound"):
		return domain.NoSuchFunction(name)
	case strings.Contains(msg, "409"), strings.Contains(msg, "alreadyExists"):
		return domain.FunctionAlreadyExists(name)
	}
	return fmt.Errorf("cloudrun %q: %w", name, err)
}
