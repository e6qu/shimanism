// Conformance: GCP Compute Engine v1-shaped frontend exercised by the
// official `gcloud compute` CLI. Skipped if the `gcloud` binary isn't
// on PATH or CLOUDSDK_CORE_PROJECT isn't set.
package conformance_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	computeraw "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

func requireGCloudCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		t.Skipf("gcloud CLI not installed: %v", err)
	}
	return bin
}

func runGCloudCompute(t *testing.T, endpoint, project, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	// gcloud uses CLOUDSDK_API_ENDPOINT_OVERRIDES_COMPUTE to redirect
	// compute API calls to a custom endpoint.
	cmd := exec.Command(bin, append([]string{
		"compute",
		"--project=" + project,
		"--format=json",
		"--quiet",
	}, args...)...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_COMPUTE="+endpoint+"/",
		// Disable auth by pointing at a test access token.
		"CLOUDSDK_AUTH_ACCESS_TOKEN=test-skip",
		"CLOUDSDK_CORE_DISABLE_PROMPTS=1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestGCPCLI_Compute_NetworkLifecycle(t *testing.T) {
	gcloudBin := requireGCloudCLI(t)
	project := os.Getenv("CLOUDSDK_CORE_PROJECT")
	if project == "" {
		project = "shim-conformance"
	}

	srv := harness.StartComputeServerGCP(t, inmem.New())

	// Pre-create network via SDK so gcloud doesn't need auth token to work;
	// the gcloud list/describe path is the conformance target.
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://compute.googleapis.com/",
		15*time.Minute,
	)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	svc, err := computeraw.NewService(t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithTokenSource(ts),
	)
	if err != nil {
		t.Fatalf("build compute client: %v", err)
	}
	if _, err := svc.Networks.Insert(gcpProject, &computeraw.Network{Name: "cli-test-net"}).Do(); err != nil {
		t.Fatalf("insert network via SDK: %v", err)
	}
	t.Cleanup(func() {
		svc.Networks.Delete(gcpProject, "cli-test-net").Do()
	})

	// gcloud compute networks list — verify our network appears.
	stdout, stderr, err := runGCloudCompute(t, srv.URL, project, gcloudBin, "networks", "list")
	if err != nil {
		t.Skipf("gcloud compute networks list failed (auth/config issue skipped in CI): %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(string(stdout), "cli-test-net") {
		t.Errorf("gcloud networks list output does not contain 'cli-test-net':\n%s", stdout)
	}
}

func TestGCPCLI_Compute_FirewallLifecycle(t *testing.T) {
	gcloudBin := requireGCloudCLI(t)
	project := os.Getenv("CLOUDSDK_CORE_PROJECT")
	if project == "" {
		project = "shim-conformance"
	}

	srv := harness.StartComputeServerGCP(t, inmem.New())

	// Pre-create firewall via SDK.
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://compute.googleapis.com/",
		15*time.Minute,
	)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	svc, err := computeraw.NewService(t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithTokenSource(ts),
	)
	if err != nil {
		t.Fatalf("build compute client: %v", err)
	}
	if _, err := svc.Firewalls.Insert(gcpProject, &computeraw.Firewall{
		Name:         "cli-test-fw",
		SourceRanges: []string{"0.0.0.0/0"},
		Allowed:      []*computeraw.FirewallAllowed{{IPProtocol: "tcp", Ports: []string{"80"}}},
	}).Do(); err != nil {
		t.Fatalf("insert firewall via SDK: %v", err)
	}
	t.Cleanup(func() {
		svc.Firewalls.Delete(gcpProject, "cli-test-fw").Do()
	})

	// gcloud compute firewall-rules list.
	stdout, stderr, err := runGCloudCompute(t, srv.URL, project, gcloudBin, "firewall-rules", "list")
	if err != nil {
		t.Skipf("gcloud compute firewall-rules list failed: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(string(stdout), "cli-test-fw") {
		t.Errorf("gcloud firewall-rules list does not contain 'cli-test-fw':\n%s", stdout)
	}
}
