// Phase 8 exit criterion (sub-phase 8.15): the shim deploys a
// Gateway + Route via the AWS API Gateway v2 frontend onto the
// Envoy Gateway backend, then opens a real HTTP connection
// through Envoy and asserts the route serves traffic end-to-end.
//
// Gated on ENVOY_CONFORMANCE=1 + KUBECONFIG; CI's conformance-envoy
// lane sets both.
package conformance_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/apigateway/conformance"
)

func TestRouteServes_Envoy(t *testing.T) {
	if os.Getenv("ENVOY_CONFORMANCE") != "1" {
		t.Skip("ENVOY_CONFORMANCE!=1 (HTTP-route exit criterion disabled)")
	}
	ctx := context.Background()
	backend := conformance.NewEnvoy(t)

	// Deploy a tiny upstream first — Envoy needs a backend Service to
	// route to. Use the kubernetes-dashboard /healthz analog: a
	// minimal echo Deployment + Service. Keep the YAML inline so the
	// test is self-contained.
	upstreamYAML := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: shim-echo
spec:
  replicas: 1
  selector:
    matchLabels:
      app: shim-echo
  template:
    metadata:
      labels:
        app: shim-echo
    spec:
      containers:
      - name: echo
        image: ealen/echo-server:0.7.0
        ports:
        - containerPort: 80
---
apiVersion: v1
kind: Service
metadata:
  name: shim-echo
spec:
  selector:
    app: shim-echo
  ports:
  - port: 80
    targetPort: 80
`
	if err := kubectlApply(ctx, upstreamYAML); err != nil {
		t.Fatalf("apply upstream: %v", err)
	}
	t.Cleanup(func() { _ = kubectlDelete(context.Background(), upstreamYAML) })
	if out, err := exec.CommandContext(ctx,
		"kubectl", "rollout", "status", "deployment/shim-echo",
		"--timeout=180s",
	).CombinedOutput(); err != nil {
		t.Fatalf("rollout shim-echo: %v\n%s", err, out)
	}

	srv := harness.StartAPIGatewayServerAWS(t, backend)
	cfg, err := awscfg.LoadDefaultConfig(ctx,
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(awsapi.AnonymousCredentials{}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	client := apigatewayv2.NewFromConfig(cfg, func(o *apigatewayv2.Options) {
		o.BaseEndpoint = awsapi.String(srv.URL)
	})

	const apiName = "shim-route-test"
	cOut, err := client.CreateApi(ctx, &apigatewayv2.CreateApiInput{
		Name:         awsapi.String(apiName),
		ProtocolType: apigwtypes.ProtocolTypeHttp,
	})
	if err != nil {
		t.Fatalf("CreateApi: %v", err)
	}
	apiID := awsapi.ToString(cOut.ApiId)
	t.Cleanup(func() {
		_, _ = client.DeleteApi(context.Background(), &apigatewayv2.DeleteApiInput{ApiId: awsapi.String(apiID)})
	})

	iOut, err := client.CreateIntegration(ctx, &apigatewayv2.CreateIntegrationInput{
		ApiId:             awsapi.String(apiID),
		IntegrationType:   apigwtypes.IntegrationTypeHttpProxy,
		IntegrationUri:    awsapi.String("http://shim-echo.default.svc.cluster.local:80/"),
		IntegrationMethod: awsapi.String("GET"),
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	intID := awsapi.ToString(iOut.IntegrationId)

	if _, err := client.CreateRoute(ctx, &apigatewayv2.CreateRouteInput{
		ApiId:    awsapi.String(apiID),
		RouteKey: awsapi.String("GET /echo"),
		Target:   awsapi.String("integrations/" + intID),
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	if _, err := client.CreateDeployment(ctx, &apigatewayv2.CreateDeploymentInput{
		ApiId: awsapi.String(apiID),
	}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	// Envoy assigns an LB IP to the Gateway over the next few seconds.
	host, port := portForwardEnvoyGateway(t, ctx, apiID)
	httpClient := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf("http://%s:%d/echo", host, port)

	var lastErr error
	var lastStatus int
	var lastBody string
	for i := 0; i < 60; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := httpClient.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastStatus = resp.StatusCode
			lastBody = string(body)
			if resp.StatusCode == 200 {
				return
			}
		} else {
			lastErr = err
		}
		time.Sleep(2 * time.Second)
	}
	dumpEnvoyState(t, ctx)
	t.Fatalf("invoke never succeeded: lastErr=%v lastStatus=%d lastBody=%q",
		lastErr, lastStatus, lastBody)
}

func kubectlApply(ctx context.Context, yaml string) error {
	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply: %w\n%s", err, out)
	}
	return nil
}

func kubectlDelete(ctx context.Context, yaml string) error {
	cmd := exec.CommandContext(ctx, "kubectl", "delete", "--ignore-not-found", "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	_, _ = cmd.CombinedOutput()
	return nil
}

func portForwardEnvoyGateway(t *testing.T, ctx context.Context, apiID string) (string, int) {
	t.Helper()
	// Envoy Gateway creates an Envoy proxy Service per Gateway CR.
	// Names look like `envoy-default-<gateway>-<hash>` in the
	// `envoy-gateway-system` namespace. Use a label selector since
	// the hash isn't predictable.
	var svcName string
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.CommandContext(ctx, "kubectl",
			"-n", "envoy-gateway-system",
			"get", "service",
			"-l", "gateway.envoyproxy.io/owning-gateway-name="+apiID,
			"-o", "jsonpath={.items[0].metadata.name}",
		).CombinedOutput()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			svcName = strings.TrimSpace(string(out))
			break
		}
		time.Sleep(2 * time.Second)
	}
	if svcName == "" {
		dumpEnvoyState(t, ctx)
		t.Fatalf("Envoy proxy Service for gateway %s never appeared", apiID)
	}

	lst, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free port: %v", err)
	}
	localPort := lst.Addr().(*net.TCPAddr).Port
	lst.Close()

	cmd := exec.CommandContext(ctx,
		"kubectl",
		"-n", "envoy-gateway-system",
		"port-forward",
		"svc/"+svcName,
		fmt.Sprintf("%d:80", localPort),
	)
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("port-forward: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	dl := time.Now().Add(30 * time.Second)
	for time.Now().Before(dl) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), time.Second)
		if err == nil {
			conn.Close()
			return "127.0.0.1", localPort
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("port-forward never opened localhost:%d; stderr=%s", localPort, stderr.String())
	return "", 0
}

func dumpEnvoyState(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, args := range [][]string{
		{"get", "gateway", "-A"},
		{"get", "httproute", "-A"},
		{"-n", "envoy-gateway-system", "get", "all"},
		{"get", "pods", "-A"},
	} {
		out, _ := exec.CommandContext(ctx, "kubectl", args...).CombinedOutput()
		t.Logf("kubectl %s:\n%s", strings.Join(args, " "), out)
	}
}
