// Conformance: GCP Cloud DNS-shaped frontend exercised by the
// official `gcloud dns` CLI. Skipped if gcloud is not on PATH.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/dns/backends/inmem"
)

func requireGcloud(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		t.Skipf("gcloud not installed (PATH lookup: %v)", err)
	}
	return bin
}

func runGcloudDNS(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	full := strings.TrimRight(srvURL, "/") + "/"
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://dns.googleapis.com/",
		15*time.Minute,
	)
	cmd := exec.Command(bin, append([]string{"--quiet"}, args...)...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_DNS="+full,
		"CLOUDSDK_AUTH_ACCESS_TOKEN="+jwt,
		"CLOUDSDK_CORE_DISABLE_PROMPTS=1",
		"CLOUDSDK_CORE_PROJECT=shim-conformance",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestGCPCLI_CloudDNS_ZoneLifecycle(t *testing.T) {
	bin := requireGcloud(t)
	srv := harness.StartDNSServerGCP(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runGcloudDNS(t, srv.URL, bin, args...)
		if err != nil {
			t.Fatalf("gcloud %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("dns", "managed-zones", "create", "cli-example-com",
		"--project=shim-conformance",
		"--dns-name=cli.example.com.",
		"--description=cli-conformance",
		"--visibility=public",
	)

	out := mustRun("dns", "managed-zones", "list",
		"--project=shim-conformance", "--format=json")
	var zones []struct {
		Name    string `json:"name"`
		DnsName string `json:"dnsName"`
	}
	if err := json.Unmarshal(out, &zones); err != nil {
		t.Fatalf("parse list output: %v\n%s", err, out)
	}
	if len(zones) != 1 || zones[0].DnsName != "cli.example.com." {
		t.Errorf("list output unexpected: %+v", zones)
	}

	mustRun("dns", "managed-zones", "delete", "cli-example-com",
		"--project=shim-conformance")
}
