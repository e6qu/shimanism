// Conformance: GCP Artifact Registry control plane exercised by the
// official `gcloud artifacts` CLI via the ARTIFACTREGISTRY endpoint
// override. Skipped if `gcloud` isn't on PATH.
package conformance_test

import (
	"bytes"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/registry/frontends/gcp_artifactregistry"
	"github.com/e6qu/shimanism/services/registry/backends/inmem"
)

func runGcloudAR(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	full := strings.TrimRight(srvURL, "/") + "/"
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://artifactregistry.googleapis.com/",
		15*time.Minute,
	)
	cmd := exec.Command(bin, append([]string{"--quiet"}, args...)...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_ARTIFACTREGISTRY="+full,
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

func TestGCPCLI_AR_RepositoryLifecycle(t *testing.T) {
	gcloud, err := exec.LookPath("gcloud")
	if err != nil {
		t.Skipf("gcloud not installed: %v", err)
	}
	srv := httptest.NewServer(gcp_artifactregistry.Handler(inmem.New()))
	defer srv.Close()

	const repo = "cli-repo"
	const loc = "us-central1"
	run := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runGcloudAR(t, srv.URL, gcloud, args...)
		if err != nil {
			t.Fatalf("gcloud %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	run("artifacts", "repositories", "create", repo,
		"--location="+loc, "--repository-format=docker")

	if out := run("artifacts", "repositories", "describe", repo, "--location="+loc); !strings.Contains(string(out), repo) {
		t.Errorf("describe output missing %q:\n%s", repo, out)
	}

	if out := run("artifacts", "repositories", "list", "--location="+loc); !strings.Contains(string(out), repo) {
		t.Errorf("list output missing %q:\n%s", repo, out)
	}

	run("artifacts", "repositories", "delete", repo, "--location="+loc)
}
