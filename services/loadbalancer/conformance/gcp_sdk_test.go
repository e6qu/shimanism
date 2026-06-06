// Conformance: GCP Compute Engine LB-shaped frontend exercised by the
// official google.golang.org/api/compute/v1 SDK via forwarding rules
// and backend services.
package conformance_test

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oauth2"
	computeraw "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/loadbalancer/backends/inmem"
)

const gcpLBProject = "shim-lb-project"

// newGCPComputeLBClient builds a Compute v1 client pointed at the
// LB shim endpoint.
func newGCPComputeLBClient(t *testing.T, endpoint string) *computeraw.Service {
	t.Helper()
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://compute.googleapis.com/",
		15*time.Minute,
	)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	svc, err := computeraw.NewService(context.Background(),
		option.WithEndpoint(endpoint),
		option.WithTokenSource(ts),
	)
	if err != nil {
		t.Fatalf("build GCP compute client: %v", err)
	}
	return svc
}

func TestGCPSDK_LB_ForwardingRuleLifecycle(t *testing.T) {
	srv := harness.StartLoadBalancerServerGCP(t, inmem.New())
	svc := newGCPComputeLBClient(t, srv.URL)
	ctx := context.Background()

	// Insert forwarding rule (= create LB)
	op, err := svc.ForwardingRules.Insert(gcpLBProject, "us-central1", &computeraw.ForwardingRule{
		Name:       "my-fr",
		IPProtocol: "TCP",
		PortRange:  "80",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ForwardingRules.Insert: %v", err)
	}
	if op.Status != "DONE" {
		t.Errorf("operation status = %q, want DONE", op.Status)
	}

	// List forwarding rules
	list, err := svc.ForwardingRules.List(gcpLBProject, "us-central1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ForwardingRules.List: %v", err)
	}
	found := false
	for _, fr := range list.Items {
		if fr.Name == "my-fr" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ForwardingRules.List: my-fr not found")
	}

	// Get forwarding rule
	fr, err := svc.ForwardingRules.Get(gcpLBProject, "us-central1", "my-fr").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ForwardingRules.Get: %v", err)
	}
	if fr.Name != "my-fr" {
		t.Errorf("ForwardingRules.Get name = %q", fr.Name)
	}

	// Delete forwarding rule
	delOp, err := svc.ForwardingRules.Delete(gcpLBProject, "us-central1", "my-fr").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ForwardingRules.Delete: %v", err)
	}
	if delOp.Status != "DONE" {
		t.Errorf("delete operation status = %q", delOp.Status)
	}
}

func TestGCPSDK_LB_BackendServiceLifecycle(t *testing.T) {
	srv := harness.StartLoadBalancerServerGCP(t, inmem.New())
	svc := newGCPComputeLBClient(t, srv.URL)
	ctx := context.Background()

	// Insert backend service (= create TargetGroup)
	op, err := svc.RegionBackendServices.Insert(gcpLBProject, "us-central1", &computeraw.BackendService{
		Name:     "my-bs",
		Protocol: "TCP",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("RegionBackendServices.Insert: %v", err)
	}
	if op.Status != "DONE" {
		t.Errorf("operation status = %q", op.Status)
	}

	// Get backend service
	bs, err := svc.RegionBackendServices.Get(gcpLBProject, "us-central1", "my-bs").Context(ctx).Do()
	if err != nil {
		t.Fatalf("RegionBackendServices.Get: %v", err)
	}
	if bs.Name != "my-bs" {
		t.Errorf("RegionBackendServices.Get name = %q", bs.Name)
	}

	// List backend services
	list, err := svc.RegionBackendServices.List(gcpLBProject, "us-central1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("RegionBackendServices.List: %v", err)
	}
	found := false
	for _, b := range list.Items {
		if b.Name == "my-bs" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("RegionBackendServices.List: my-bs not found")
	}

	// Delete backend service
	if _, err := svc.RegionBackendServices.Delete(gcpLBProject, "us-central1", "my-bs").Context(ctx).Do(); err != nil {
		t.Fatalf("RegionBackendServices.Delete: %v", err)
	}
}

func TestGCPSDK_LB_L7Lifecycle(t *testing.T) {
	srv := harness.StartLoadBalancerServerGCP(t, inmem.New())
	svc := newGCPComputeLBClient(t, srv.URL)
	ctx := context.Background()

	// 1. Insert SslCertificate (opaque cert pass-through)
	certOp, err := svc.SslCertificates.Insert(gcpLBProject, &computeraw.SslCertificate{
		Name: "my-cert",
		Type: "SELF_MANAGED",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SslCertificates.Insert: %v", err)
	}
	if certOp.Status != "DONE" {
		t.Errorf("SslCertificates.Insert operation status = %q, want DONE", certOp.Status)
	}

	// GET the cert back
	cert, err := svc.SslCertificates.Get(gcpLBProject, "my-cert").Context(ctx).Do()
	if err != nil {
		t.Fatalf("SslCertificates.Get: %v", err)
	}
	if cert.Name != "my-cert" {
		t.Errorf("SslCertificates.Get name = %q, want my-cert", cert.Name)
	}

	// 2. Insert global BackendService (HTTP target group)
	beOp, err := svc.BackendServices.Insert(gcpLBProject, &computeraw.BackendService{
		Name:     "my-backend",
		Protocol: "HTTP",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("BackendServices.Insert: %v", err)
	}
	if beOp.Status != "DONE" {
		t.Errorf("BackendServices.Insert operation status = %q, want DONE", beOp.Status)
	}

	// GET the backend service
	bs, err := svc.BackendServices.Get(gcpLBProject, "my-backend").Context(ctx).Do()
	if err != nil {
		t.Fatalf("BackendServices.Get: %v", err)
	}
	if bs.Name != "my-backend" {
		t.Errorf("BackendServices.Get name = %q, want my-backend", bs.Name)
	}
	if bs.Protocol != "HTTP" {
		t.Errorf("BackendServices.Get protocol = %q, want HTTP", bs.Protocol)
	}

	// 3. Insert UrlMap referencing the backend service
	beSelfLink := "https://www.googleapis.com/compute/v1/projects/" + gcpLBProject + "/global/backendServices/my-backend"
	umOp, err := svc.UrlMaps.Insert(gcpLBProject, &computeraw.UrlMap{
		Name:           "my-urlmap",
		DefaultService: beSelfLink,
		PathMatchers: []*computeraw.PathMatcher{
			{
				Name:           "pm1",
				DefaultService: beSelfLink,
				PathRules: []*computeraw.PathRule{
					{Paths: []string{"/api/*"}, Service: beSelfLink},
				},
			},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("UrlMaps.Insert: %v", err)
	}
	if umOp.Status != "DONE" {
		t.Errorf("UrlMaps.Insert operation status = %q, want DONE", umOp.Status)
	}

	// GET the url map
	um, err := svc.UrlMaps.Get(gcpLBProject, "my-urlmap").Context(ctx).Do()
	if err != nil {
		t.Fatalf("UrlMaps.Get: %v", err)
	}
	if um.Name != "my-urlmap" {
		t.Errorf("UrlMaps.Get name = %q, want my-urlmap", um.Name)
	}
	if len(um.PathMatchers) == 0 {
		t.Errorf("UrlMaps.Get: expected at least one path matcher")
	}

	// 4. Insert TargetHttpsProxy referencing urlMap + cert
	certSelfLink := "https://www.googleapis.com/compute/v1/projects/" + gcpLBProject + "/global/sslCertificates/my-cert"
	umSelfLink := "https://www.googleapis.com/compute/v1/projects/" + gcpLBProject + "/global/urlMaps/my-urlmap"
	proxyOp, err := svc.TargetHttpsProxies.Insert(gcpLBProject, &computeraw.TargetHttpsProxy{
		Name:            "my-proxy",
		UrlMap:          umSelfLink,
		SslCertificates: []string{certSelfLink},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("TargetHttpsProxies.Insert: %v", err)
	}
	if proxyOp.Status != "DONE" {
		t.Errorf("TargetHttpsProxies.Insert operation status = %q, want DONE", proxyOp.Status)
	}

	// GET the proxy
	proxy, err := svc.TargetHttpsProxies.Get(gcpLBProject, "my-proxy").Context(ctx).Do()
	if err != nil {
		t.Fatalf("TargetHttpsProxies.Get: %v", err)
	}
	if proxy.Name != "my-proxy" {
		t.Errorf("TargetHttpsProxies.Get name = %q, want my-proxy", proxy.Name)
	}
	if len(proxy.SslCertificates) == 0 {
		t.Errorf("TargetHttpsProxies.Get: expected sslCertificates to be populated")
	}

	// 5. Insert GlobalForwardingRule (final assembly → LB + Listener + Rules)
	proxySelfLink := "https://www.googleapis.com/compute/v1/projects/" + gcpLBProject + "/global/targetHttpsProxies/my-proxy"
	frOp, err := svc.GlobalForwardingRules.Insert(gcpLBProject, &computeraw.ForwardingRule{
		Name:       "my-l7-lb",
		Target:     proxySelfLink,
		PortRange:  "443",
		IPProtocol: "TCP",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GlobalForwardingRules.Insert: %v", err)
	}
	if frOp.Status != "DONE" {
		t.Errorf("GlobalForwardingRules.Insert operation status = %q, want DONE", frOp.Status)
	}

	// GET the forwarding rule
	fr, err := svc.GlobalForwardingRules.Get(gcpLBProject, "my-l7-lb").Context(ctx).Do()
	if err != nil {
		t.Fatalf("GlobalForwardingRules.Get: %v", err)
	}
	if fr.Name != "my-l7-lb" {
		t.Errorf("GlobalForwardingRules.Get name = %q, want my-l7-lb", fr.Name)
	}
	if fr.PortRange != "443" {
		t.Errorf("GlobalForwardingRules.Get portRange = %q, want 443", fr.PortRange)
	}

	// List global forwarding rules
	frList, err := svc.GlobalForwardingRules.List(gcpLBProject).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GlobalForwardingRules.List: %v", err)
	}
	found := false
	for _, item := range frList.Items {
		if item.Name == "my-l7-lb" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GlobalForwardingRules.List: my-l7-lb not found")
	}

	// 6. Cleanup in reverse order.
	if _, err := svc.GlobalForwardingRules.Delete(gcpLBProject, "my-l7-lb").Context(ctx).Do(); err != nil {
		t.Fatalf("GlobalForwardingRules.Delete: %v", err)
	}
	if _, err := svc.TargetHttpsProxies.Delete(gcpLBProject, "my-proxy").Context(ctx).Do(); err != nil {
		t.Fatalf("TargetHttpsProxies.Delete: %v", err)
	}
	if _, err := svc.UrlMaps.Delete(gcpLBProject, "my-urlmap").Context(ctx).Do(); err != nil {
		t.Fatalf("UrlMaps.Delete: %v", err)
	}
	if _, err := svc.BackendServices.Delete(gcpLBProject, "my-backend").Context(ctx).Do(); err != nil {
		t.Fatalf("BackendServices.Delete: %v", err)
	}
	if _, err := svc.SslCertificates.Delete(gcpLBProject, "my-cert").Context(ctx).Do(); err != nil {
		t.Fatalf("SslCertificates.Delete: %v", err)
	}
}
