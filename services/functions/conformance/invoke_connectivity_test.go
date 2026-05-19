// Phase 7 exit criterion (sub-phase 7.15): the shim deploys a
// container to Knative via the AWS Lambda frontend, then the test
// opens a real HTTP connection to the returned URL and asserts the
// function's response.
//
// The shim is invisible to the HTTP invocation — that's what makes
// this honest. The function image is the upstream
// gcr.io/knative-samples/helloworld-go which returns "Hello World!".
//
// Gated on KNATIVE_CONFORMANCE=1 + KUBECONFIG; CI's
// conformance-knative lane sets both.
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
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/functions/conformance"
)

func TestInvokeConnectivity_Knative(t *testing.T) {
	if os.Getenv("KNATIVE_CONFORMANCE") != "1" {
		t.Skip("KNATIVE_CONFORMANCE!=1 (HTTP-invoke connectivity test disabled)")
	}
	ctx := context.Background()
	be := conformance.NewKnative(t)
	srv := harness.StartFunctionsServerAWS(t, be)
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(awsapi.AnonymousCredentials{}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	client := lambda.NewFromConfig(cfg, func(o *lambda.Options) {
		o.BaseEndpoint = awsapi.String(srv.URL)
	})

	const name = "invoke-test"
	if _, err := client.CreateFunction(ctx, &lambda.CreateFunctionInput{
		FunctionName: awsapi.String(name),
		PackageType:  lambdatypes.PackageTypeImage,
		Code: &lambdatypes.FunctionCode{
			ImageUri: awsapi.String("gcr.io/knative-samples/helloworld-go"),
		},
		Role:    awsapi.String("arn:aws:iam::000000000000:role/lambda"),
		Timeout: awsapi.Int32(60),
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
	t.Cleanup(func() {
		_, _ = client.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
			FunctionName: awsapi.String(name),
		})
	})

	// The Lambda frontend doesn't expose the real Knative URL via
	// the Lambda wire shape (it emits aws-lambda://<arn>). To find
	// the real URL we ask the backend directly. But the backend
	// surfaces it via DescribeFunction → Endpoint.URL through the
	// shim's domain, which the Lambda frontend translates into the
	// aws-lambda://... placeholder.
	//
	// For the connectivity test, fetch the Knative Service URL by
	// reading it from kubectl. That's the closest analog to what a
	// real consumer would do once the AWS Lambda frontend emitted
	// a FunctionURL config.
	url := waitForKnativeURL(t, ctx, name)
	t.Logf("function ready, url=%s", url)

	// Port-forward to the Knative Service since the test runs
	// outside kind. The ingress is via kourier-system or
	// knative-serving's local-gateway; port-forwarding the Service
	// directly lets us bypass the gateway for the test.
	host, port := portForwardKnativeService(t, ctx, name)

	httpClient := &http.Client{Timeout: 30 * time.Second}
	dst := fmt.Sprintf("http://%s:%d/", host, port)

	var lastErr error
	for i := 0; i < 30; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, dst, nil)
		// Knative routes by Host header. Strip the URL prefix from the
		// returned URL to compute the expected Host.
		hostHeader := strings.TrimPrefix(url, "http://")
		hostHeader = strings.TrimPrefix(hostHeader, "https://")
		req.Host = hostHeader
		resp, err := httpClient.Do(req)
		if err == nil && resp.StatusCode == 200 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if !strings.Contains(string(body), "Hello") {
				t.Errorf("body = %q, want it to contain 'Hello'", body)
			}
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("invoke never succeeded: %v", lastErr)
}

// waitForKnativeURL polls `kubectl get ksvc <name> -o jsonpath` until
// the Service's status.url is populated.
func waitForKnativeURL(t *testing.T, ctx context.Context, name string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		out, err := exec.CommandContext(ctx, "kubectl",
			"get", "ksvc", name,
			"-o", "jsonpath={.status.url}").CombinedOutput()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			return strings.TrimSpace(string(out))
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("Knative Service %q never reported a URL", name)
	return ""
}

// portForwardKnativeService spawns `kubectl port-forward` against
// the Knative ingress gateway (kourier-system/kourier) so the test
// can dial Knative-routed HTTP. The caller sets the Host header to
// the ksvc's URL host; kourier dispatches to the backing Pod.
//
// `name` is unused here because all Knative Services share one
// gateway; routing is by Host header. Kept in the signature for
// future symmetry.
func portForwardKnativeService(t *testing.T, ctx context.Context, name string) (string, int) {
	t.Helper()
	_ = name
	lst, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	localPort := lst.Addr().(*net.TCPAddr).Port
	lst.Close()

	// kourier-system is the namespace the Knative operator installs
	// kourier into when KnativeServing.spec.ingress.kourier.enabled
	// is true. The Service is named `kourier` and listens on :80.
	cmd := exec.CommandContext(ctx, "kubectl",
		"-n", "kourier-system",
		"port-forward",
		"svc/kourier",
		fmt.Sprintf("%d:80", localPort),
	)
	// Capture stderr so failures (kourier not installed, wrong name)
	// surface in the test log instead of being silent.
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("kubectl port-forward: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), time.Second)
		if err == nil {
			conn.Close()
			return "127.0.0.1", localPort
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("kubectl port-forward never opened localhost:%d; stderr=%s",
		localPort, stderr.String())
	return "", 0
}
