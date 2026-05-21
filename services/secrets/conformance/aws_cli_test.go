// Phase 2 conformance: AWS Secrets Manager-shaped frontend exercised
// by the official `aws secretsmanager` CLI. Each command shells out
// against the shim's endpoint. Skipped if the `aws` binary isn't on
// PATH; CI's main lane has it preinstalled.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
)

func requireAWSCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runAWSSecrets(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"--endpoint-url=" + srvURL, "--no-cli-pager"}, args...)...)
	cmd.Env = append(os.Environ(),
		// Verifier's trusted test credentials.
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

func TestAWSCLI_SecretLifecycle(t *testing.T) {
	aws := requireAWSCLI(t)
	srv := harness.StartSecretsServerAWS(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWSSecrets(t, srv.URL, aws, args...)
		if err != nil {
			t.Fatalf("aws %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("secretsmanager", "create-secret",
		"--name", "cli/token",
		"--secret-string", "hello-cli")

	out := mustRun("secretsmanager", "get-secret-value", "--secret-id", "cli/token")
	var got struct {
		SecretString string `json:"SecretString"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse get-secret-value JSON: %v\n%s", err, out)
	}
	if got.SecretString != "hello-cli" {
		t.Errorf("SecretString = %q, want hello-cli", got.SecretString)
	}

	mustRun("secretsmanager", "put-secret-value",
		"--secret-id", "cli/token",
		"--secret-string", "hello-cli-v2")

	out = mustRun("secretsmanager", "get-secret-value", "--secret-id", "cli/token")
	_ = json.Unmarshal(out, &got)
	if got.SecretString != "hello-cli-v2" {
		t.Errorf("v2 SecretString = %q, want hello-cli-v2", got.SecretString)
	}

	mustRun("secretsmanager", "delete-secret",
		"--secret-id", "cli/token",
		"--force-delete-without-recovery")
}
