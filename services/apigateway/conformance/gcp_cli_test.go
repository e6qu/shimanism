// Phase 8 conformance: GCP API Gateway-shaped frontend driven by
// `gcloud api-gateway`. Skipped if `gcloud` isn't on PATH.
package conformance_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

func requireGCloudAPIGW(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		t.Skipf("gcloud not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runGCloudAPIGW(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://apigateway.googleapis.com/",
		15*time.Minute,
	)
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_APIGATEWAY="+srvURL+"/",
		"CLOUDSDK_AUTH_ACCESS_TOKEN="+jwt,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestGCloudCLI_APIGatewayLifecycle(t *testing.T) {
	g := requireGCloudAPIGW(t)
	srv := harness.StartAPIGatewayServerGCP(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runGCloudAPIGW(t, srv.URL, g, args...)
		if err != nil {
			t.Fatalf("gcloud %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("api-gateway", "gateways", "list",
		"--project", "shim-proj",
		"--location", "us-central1")
}
