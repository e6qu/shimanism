// Package envoy is the Envoy Gateway backend for shimanism's API
// Gateway service, acting as the K8s peer.
//
// Envoy Gateway uses the gateway-api CRDs (`Gateway` +
// `HTTPRoute` from gateway.networking.k8s.io/v1). The shim's
// DeployGateway atomically replaces the HTTPRoute set bound to
// the parent Gateway: delete all old HTTPRoutes, create new ones.
//
// Same dynamic-client + unstructured-CR pattern as Phases 5-7.
package envoy

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

	"github.com/e6qu/shimanism/internal/apigateway/domain"
)

var (
	gatewayGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gateways",
	}
	httpRouteGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}
)

type Config struct {
	Namespace string
	// GatewayClassName is the gatewayClassName the Gateway CR
	// references. Envoy Gateway installs a `eg` class by default.
	GatewayClassName string
}

type Backend struct {
	dyn       dynamic.Interface
	namespace string
	className string
}

func New(dyn dynamic.Interface, cfg Config) *Backend {
	ns := cfg.Namespace
	if ns == "" {
		ns = "default"
	}
	cn := cfg.GatewayClassName
	if cn == "" {
		cn = "eg"
	}
	return &Backend{dyn: dyn, namespace: ns, className: cn}
}

var _ domain.APIGateway = (*Backend)(nil)

func (b *Backend) CreateGateway(ctx context.Context, name string, opt domain.CreateGatewayOptions) (domain.Gateway, error) {
	gw := &unstructured.Unstructured{}
	gw.SetAPIVersion("gateway.networking.k8s.io/v1")
	gw.SetKind("Gateway")
	gw.SetName(name)
	gw.SetNamespace(b.namespace)
	gw.Object["spec"] = map[string]interface{}{
		"gatewayClassName": b.className,
		"listeners": []interface{}{
			map[string]interface{}{
				"name":     "http",
				"port":     int64(80),
				"protocol": "HTTP",
				"allowedRoutes": map[string]interface{}{
					"namespaces": map[string]interface{}{"from": "Same"},
				},
			},
		},
	}
	if _, err := b.dyn.Resource(gatewayGVR).Namespace(b.namespace).Create(ctx, gw, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.Gateway{}, domain.GatewayAlreadyExists(name)
		}
		return domain.Gateway{}, fmt.Errorf("create Gateway CR: %w", err)
	}
	if len(opt.Routes) > 0 {
		if err := b.deployRoutes(ctx, name, opt.Routes); err != nil {
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
	// Delete all HTTPRoutes bound to this Gateway first.
	_ = b.deleteRoutesFor(ctx, name)
	if err := b.dyn.Resource(gatewayGVR).Namespace(b.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return domain.NoSuchGateway(name)
		}
		return fmt.Errorf("delete Gateway CR: %w", err)
	}
	return nil
}

func (b *Backend) DescribeGateway(ctx context.Context, name string) (domain.Gateway, error) {
	gw, err := b.dyn.Resource(gatewayGVR).Namespace(b.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.Gateway{}, domain.NoSuchGateway(name)
		}
		return domain.Gateway{}, fmt.Errorf("get Gateway CR: %w", err)
	}
	out := domain.Gateway{
		Name:      name,
		Status:    statusFromGateway(gw),
		CreatedAt: gw.GetCreationTimestamp().Time,
	}
	if out.Status == domain.StatusAvailable {
		// Read status.addresses[0].value as the endpoint.
		if status, ok := gw.Object["status"].(map[string]interface{}); ok {
			if addrs, ok := status["addresses"].([]interface{}); ok && len(addrs) > 0 {
				if a, ok := addrs[0].(map[string]interface{}); ok {
					if v, ok := a["value"].(string); ok {
						out.Endpoint = domain.Endpoint{URL: "http://" + v}
					}
				}
			}
		}
	}
	// List HTTPRoutes bound to this gateway.
	if routes, err := b.routesFor(ctx, name); err == nil {
		out.Routes = routes
	}
	return out, nil
}

func (b *Backend) ListGateways(ctx context.Context, opt domain.ListGatewaysOptions) (domain.ListGatewaysResult, error) {
	list, err := b.dyn.Resource(gatewayGVR).Namespace(b.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return domain.ListGatewaysResult{}, fmt.Errorf("list Gateway CRs: %w", err)
	}
	res := domain.ListGatewaysResult{}
	for i := range list.Items {
		gw := &list.Items[i]
		if opt.Prefix != "" && !strings.HasPrefix(gw.GetName(), opt.Prefix) {
			continue
		}
		g, _ := b.DescribeGateway(ctx, gw.GetName())
		res.Gateways = append(res.Gateways, g)
		if opt.MaxResults > 0 && len(res.Gateways) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) DeployGateway(ctx context.Context, name string, opt domain.DeployGatewayOptions) error {
	// Atomic in semantics: delete old HTTPRoutes, create new ones.
	// Not literally atomic across the K8s API but the kingress
	// reconciler eventually converges; readers see "one Gateway,
	// some route set" the whole time.
	if _, err := b.dyn.Resource(gatewayGVR).Namespace(b.namespace).Get(ctx, name, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return domain.NoSuchGateway(name)
		}
		return err
	}
	if err := b.deleteRoutesFor(ctx, name); err != nil {
		return err
	}
	return b.deployRoutes(ctx, name, opt.Routes)
}

func (b *Backend) deployRoutes(ctx context.Context, gateway string, routes []domain.Route) error {
	for i, route := range routes {
		hr := &unstructured.Unstructured{}
		hr.SetAPIVersion("gateway.networking.k8s.io/v1")
		hr.SetKind("HTTPRoute")
		hr.SetName(fmt.Sprintf("%s-route-%d", gateway, i))
		hr.SetNamespace(b.namespace)
		hr.SetLabels(map[string]string{"shim.apigateway/gateway": gateway})
		// Parse the backend URL into a Gateway API backendRef:
		// {name: <Service name>, namespace?: <Service ns>, port: <int>}.
		// Service names can't contain dots, so a backend URL like
		// http://svc.ns.svc.cluster.local:80 must be split.
		svcName, svcNS, port := parseBackend(route.Backend)
		method := route.Method
		if method == "" || method == "ANY" {
			method = ""
		}
		match := map[string]interface{}{
			"path": map[string]interface{}{
				"type":  "PathPrefix",
				"value": route.Path,
			},
		}
		if method != "" {
			match["method"] = method
		}
		backendRef := map[string]interface{}{
			"name": svcName,
			"port": int64(port),
		}
		if svcNS != "" && svcNS != b.namespace {
			backendRef["namespace"] = svcNS
		}
		hr.Object["spec"] = map[string]interface{}{
			"parentRefs": []interface{}{
				map[string]interface{}{
					"name":      gateway,
					"namespace": b.namespace,
				},
			},
			"rules": []interface{}{
				map[string]interface{}{
					"matches":     []interface{}{match},
					"backendRefs": []interface{}{backendRef},
				},
			},
		}
		if _, err := b.dyn.Resource(httpRouteGVR).Namespace(b.namespace).Create(ctx, hr, metav1.CreateOptions{}); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("create HTTPRoute %d: %w", i, err)
			}
		}
	}
	return nil
}

func (b *Backend) deleteRoutesFor(ctx context.Context, gateway string) error {
	list, err := b.dyn.Resource(httpRouteGVR).Namespace(b.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "shim.apigateway/gateway=" + gateway,
	})
	if err != nil {
		return fmt.Errorf("list HTTPRoutes: %w", err)
	}
	for i := range list.Items {
		_ = b.dyn.Resource(httpRouteGVR).Namespace(b.namespace).Delete(ctx, list.Items[i].GetName(), metav1.DeleteOptions{})
	}
	return nil
}

func (b *Backend) routesFor(ctx context.Context, gateway string) ([]domain.Route, error) {
	list, err := b.dyn.Resource(httpRouteGVR).Namespace(b.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "shim.apigateway/gateway=" + gateway,
	})
	if err != nil {
		return nil, err
	}
	out := []domain.Route{}
	for i := range list.Items {
		hr := &list.Items[i]
		spec, ok := hr.Object["spec"].(map[string]interface{})
		if !ok {
			continue
		}
		rules, _ := spec["rules"].([]interface{})
		for _, ru := range rules {
			rm, _ := ru.(map[string]interface{})
			matches, _ := rm["matches"].([]interface{})
			for _, mch := range matches {
				mm, _ := mch.(map[string]interface{})
				method, _ := mm["method"].(string)
				if method == "" {
					method = "ANY"
				}
				path := ""
				if pathObj, ok := mm["path"].(map[string]interface{}); ok {
					if v, ok := pathObj["value"].(string); ok {
						path = v
					}
				}
				backend := ""
				if refs, ok := rm["backendRefs"].([]interface{}); ok && len(refs) > 0 {
					if br, ok := refs[0].(map[string]interface{}); ok {
						name, _ := br["name"].(string)
						ns, _ := br["namespace"].(string)
						port := int64(80)
						if p, ok := br["port"].(int64); ok {
							port = p
						} else if p, ok := br["port"].(float64); ok {
							port = int64(p)
						}
						if ns == "" {
							ns = b.namespace
						}
						backend = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", name, ns, port)
					}
				}
				out = append(out, domain.Route{
					Method:  method,
					Path:    path,
					Backend: backend,
				})
			}
		}
	}
	return out, nil
}

func statusFromGateway(u *unstructured.Unstructured) domain.Status {
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
	// "Programmed" + "Accepted" both True → Available.
	programmed, accepted := false, false
	for _, c := range conds {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		t, _ := cm["type"].(string)
		s, _ := cm["status"].(string)
		if t == "Programmed" && s == "True" {
			programmed = true
		}
		if t == "Accepted" && s == "True" {
			accepted = true
		}
	}
	if programmed && accepted {
		return domain.StatusAvailable
	}
	return domain.StatusCreating
}

// parseBackend extracts a Gateway-API-style backendRef from a URL.
// Gateway API backendRefs use `name: <Service>` (bare Service name,
// no DNS suffix) + an optional `namespace`. So a URL like
// "http://svc.ns.svc.cluster.local:8080" splits into (svc, ns, 8080),
// and a bare "shim-echo:80" becomes ("shim-echo", "", 80).
//
// Heuristics:
//   - If the host part contains dots and the second-from-the-end
//     label is "svc", it's an in-cluster DNS name — first label is
//     the Service, second label is the namespace.
//   - Otherwise the host part is taken as the bare Service name and
//     namespace is left empty (HTTPRoute's namespace is implied).
//
// Port defaults to 80 if not in the URL.
func parseBackend(url string) (name, namespace string, port int) {
	u := strings.TrimPrefix(url, "https://")
	u = strings.TrimPrefix(u, "http://")
	if i := strings.IndexByte(u, '/'); i >= 0 {
		u = u[:i]
	}
	host := u
	port = 80
	if i := strings.IndexByte(u, ':'); i >= 0 {
		host = u[:i]
		fmt.Sscanf(u[i+1:], "%d", &port)
	}
	labels := strings.Split(host, ".")
	if len(labels) >= 3 && labels[2] == "svc" {
		return labels[0], labels[1], port
	}
	if len(labels) >= 2 && labels[1] == "svc" {
		return labels[0], "", port
	}
	return labels[0], "", port
}
