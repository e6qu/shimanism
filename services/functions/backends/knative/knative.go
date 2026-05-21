// Package knative is the Knative Serving backend for shimanism's
// functions service, acting as the K8s peer.
//
// Targets Knative Serving's `Service` CRD (`serving.knative.dev/v1`).
// Same dynamic-client + unstructured-CR pattern as Phase 5 (cnpg)
// and Phase 6 (Redis Operator).
package knative

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/e6qu/shimanism/internal/functions/domain"
)

var serviceGVR = schema.GroupVersionResource{
	Group:    "serving.knative.dev",
	Version:  "v1",
	Resource: "services",
}

type Config struct {
	Namespace string
}

type Backend struct {
	dyn       dynamic.Interface
	namespace string
}

func New(dyn dynamic.Interface, cfg Config) *Backend {
	ns := cfg.Namespace
	if ns == "" {
		ns = "default"
	}
	return &Backend{dyn: dyn, namespace: ns}
}

var _ domain.Functions = (*Backend)(nil)

func (b *Backend) CreateFunction(ctx context.Context, name string, opt domain.CreateFunctionOptions) (domain.Function, error) {
	if opt.Image == "" {
		return domain.Function{}, domain.InvalidArgument("Image is required")
	}
	if opt.Role != "" {
		return domain.Function{}, domain.InvalidArgument("Role is AWS Lambda-specific; not supported on Knative")
	}
	if opt.Publish {
		return domain.Function{}, domain.InvalidArgument("Publish is AWS Lambda-specific; not supported on Knative")
	}
	svc := &unstructured.Unstructured{}
	svc.SetAPIVersion("serving.knative.dev/v1")
	svc.SetKind("Service")
	svc.SetName(name)
	svc.SetNamespace(b.namespace)
	container := map[string]interface{}{"image": opt.Image}
	if len(opt.Environment) > 0 {
		env := []interface{}{}
		for k, v := range opt.Environment {
			env = append(env, map[string]interface{}{"name": k, "value": v})
		}
		container["env"] = env
	}
	resources := map[string]interface{}{}
	limits := map[string]interface{}{}
	if opt.MemoryBytes > 0 {
		limits["memory"] = fmt.Sprintf("%dMi", opt.MemoryBytes/(1024*1024))
	}
	if opt.CPUMilliCores > 0 {
		limits["cpu"] = fmt.Sprintf("%dm", opt.CPUMilliCores)
	}
	if len(limits) > 0 {
		resources["limits"] = limits
		container["resources"] = resources
	}
	revSpec := map[string]interface{}{
		"containers": []interface{}{container},
	}
	if opt.TimeoutSeconds > 0 {
		revSpec["timeoutSeconds"] = int64(opt.TimeoutSeconds)
	}
	svc.Object["spec"] = map[string]interface{}{
		"template": map[string]interface{}{
			"spec": revSpec,
		},
	}
	if _, err := b.dyn.Resource(serviceGVR).Namespace(b.namespace).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.Function{}, domain.FunctionAlreadyExists(name)
		}
		return domain.Function{}, fmt.Errorf("create Knative Service: %w", err)
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
	if err := b.dyn.Resource(serviceGVR).Namespace(b.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return domain.NoSuchFunction(name)
		}
		return fmt.Errorf("delete Knative Service: %w", err)
	}
	return nil
}

func (b *Backend) DescribeFunction(ctx context.Context, name string) (domain.Function, error) {
	u, err := b.dyn.Resource(serviceGVR).Namespace(b.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.Function{}, domain.NoSuchFunction(name)
		}
		return domain.Function{}, fmt.Errorf("get Knative Service: %w", err)
	}
	return b.toDomain(u), nil
}

func (b *Backend) ListFunctions(ctx context.Context, opt domain.ListFunctionsOptions) (domain.ListFunctionsResult, error) {
	list, err := b.dyn.Resource(serviceGVR).Namespace(b.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return domain.ListFunctionsResult{}, fmt.Errorf("list Knative Services: %w", err)
	}
	res := domain.ListFunctionsResult{}
	for i := range list.Items {
		u := &list.Items[i]
		if opt.Prefix != "" && !strings.HasPrefix(u.GetName(), opt.Prefix) {
			continue
		}
		res.Functions = append(res.Functions, b.toDomain(u))
		if opt.MaxResults > 0 && len(res.Functions) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) UpdateFunction(ctx context.Context, name string, opt domain.UpdateFunctionOptions) error {
	u, err := b.dyn.Resource(serviceGVR).Namespace(b.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.NoSuchFunction(name)
		}
		return err
	}
	spec, _ := u.Object["spec"].(map[string]interface{})
	template, _ := spec["template"].(map[string]interface{})
	tspec, _ := template["spec"].(map[string]interface{})
	containers, _ := tspec["containers"].([]interface{})
	if len(containers) == 0 {
		return fmt.Errorf("knative Service %q has no containers", name)
	}
	c, _ := containers[0].(map[string]interface{})
	if opt.Image != "" {
		c["image"] = opt.Image
	}
	if opt.Environment != nil {
		env := []interface{}{}
		for k, v := range opt.Environment {
			env = append(env, map[string]interface{}{"name": k, "value": v})
		}
		c["env"] = env
	}
	if opt.MemoryBytes > 0 || opt.CPUMilliCores > 0 {
		resources, _ := c["resources"].(map[string]interface{})
		if resources == nil {
			resources = map[string]interface{}{}
		}
		limits, _ := resources["limits"].(map[string]interface{})
		if limits == nil {
			limits = map[string]interface{}{}
		}
		if opt.MemoryBytes > 0 {
			limits["memory"] = fmt.Sprintf("%dMi", opt.MemoryBytes/(1024*1024))
		}
		if opt.CPUMilliCores > 0 {
			limits["cpu"] = fmt.Sprintf("%dm", opt.CPUMilliCores)
		}
		resources["limits"] = limits
		c["resources"] = resources
	}
	if opt.TimeoutSeconds > 0 {
		tspec["timeoutSeconds"] = int64(opt.TimeoutSeconds)
	}
	containers[0] = c
	tspec["containers"] = containers
	template["spec"] = tspec
	spec["template"] = template
	u.Object["spec"] = spec
	if _, err := b.dyn.Resource(serviceGVR).Namespace(b.namespace).Update(ctx, u, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update Knative Service: %w", err)
	}
	return nil
}

func (b *Backend) toDomain(u *unstructured.Unstructured) domain.Function {
	name := u.GetName()
	fn := domain.Function{
		Name:      name,
		Status:    statusFromService(u),
		CreatedAt: u.GetCreationTimestamp().Time,
	}
	// Image from spec.template.spec.containers[0].image
	if spec, ok := u.Object["spec"].(map[string]interface{}); ok {
		if template, ok := spec["template"].(map[string]interface{}); ok {
			if tspec, ok := template["spec"].(map[string]interface{}); ok {
				if containers, ok := tspec["containers"].([]interface{}); ok && len(containers) > 0 {
					if c, ok := containers[0].(map[string]interface{}); ok {
						if img, ok := c["image"].(string); ok {
							fn.Image = img
						}
					}
				}
			}
		}
	}
	// Endpoint from status.url once Available.
	if fn.Status == domain.StatusAvailable {
		if status, ok := u.Object["status"].(map[string]interface{}); ok {
			if url, ok := status["url"].(string); ok {
				fn.Endpoint = domain.Endpoint{URL: url}
			}
		}
	}
	return fn
}

// statusFromService reads .status.conditions and returns the closest
// domain.Status. Knative reports Ready=True once the route is
// configured and at least one revision is running.
func statusFromService(u *unstructured.Unstructured) domain.Status {
	if u.GetDeletionTimestamp() != nil {
		return domain.StatusDeleting
	}
	status, ok := u.Object["status"].(map[string]interface{})
	if !ok {
		return domain.StatusCreating
	}
	conds, ok := status["conditions"].([]interface{})
	if !ok {
		return domain.StatusCreating
	}
	for _, c := range conds {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if cm["type"] == "Ready" {
			if cm["status"] == "True" {
				return domain.StatusAvailable
			}
			return domain.StatusCreating
		}
	}
	return domain.StatusCreating
}
