// Phase 7 conformance: AWS Lambda CLI cell.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/functions/backends/inmem"
)

func TestAWSCLI_FunctionsLifecycle(t *testing.T) {
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed: %v", err)
	}
	srv := harness.StartFunctionsServerAWS(t, inmem.New())

	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"--endpoint-url=" + srv.URL, "--no-cli-pager"}, args...)...)
		cmd.Env = append(os.Environ(),
			"AWS_ACCESS_KEY_ID=test",
			"AWS_SECRET_ACCESS_KEY=test",
			"AWS_DEFAULT_REGION=us-east-1",
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("aws %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout.Bytes(), stderr.Bytes(), err)
		}
		return stdout.Bytes()
	}

	run("lambda", "create-function",
		"--function-name", "cli-fn",
		"--package-type", "Image",
		"--code", "ImageUri=docker.io/library/hello-world:latest",
		"--role", "arn:aws:iam::000000000000:role/lambda",
		"--memory-size", "128",
		"--timeout", "3")

	out := run("lambda", "get-function-configuration",
		"--function-name", "cli-fn")
	var cfg struct {
		FunctionName string `json:"FunctionName"`
		State        string `json:"State"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, out)
	}
	if cfg.FunctionName != "cli-fn" {
		t.Errorf("FunctionName = %q, want cli-fn", cfg.FunctionName)
	}

	run("lambda", "delete-function", "--function-name", "cli-fn")
}
