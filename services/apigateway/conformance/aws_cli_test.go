// Phase 8 conformance: AWS API Gateway v2-shaped frontend driven by
// the official `aws apigatewayv2` CLI. Skipped if the `aws` binary
// isn't on PATH.
package conformance_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

func requireAWSCLIAPIGW(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runAWSAPIGW(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"--endpoint-url=" + srvURL, "--no-cli-pager"}, args...)...)
	cmd.Env = append(os.Environ(),
		"AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"AWS_DEFAULT_REGION=us-east-1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestAWSCLI_APIGatewayLifecycle(t *testing.T) {
	aws := requireAWSCLIAPIGW(t)
	srv := harness.StartAPIGatewayServerAWS(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWSAPIGW(t, srv.URL, aws, args...)
		if err != nil {
			t.Fatalf("aws %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	// create + delete a minimal API.
	mustRun("apigatewayv2", "create-api",
		"--name", "shim-cli-api",
		"--protocol-type", "HTTP")

	mustRun("apigatewayv2", "get-apis")

	mustRun("apigatewayv2", "delete-api", "--api-id", "shim-cli-api")
}
