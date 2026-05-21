// Phase 6 conformance: AWS ElastiCache CLI lifecycle.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/cache/backends/inmem"
)

func TestAWSCLI_CacheLifecycle(t *testing.T) {
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed: %v", err)
	}
	srv := harness.StartCacheServerAWS(t, inmem.New())

	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"--endpoint-url=" + srv.URL, "--no-cli-pager"}, args...)...)
		cmd.Env = append(os.Environ(),
			"AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
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

	run("elasticache", "create-cache-cluster",
		"--cache-cluster-id", "cli-cache",
		"--engine", "redis",
		"--engine-version", "7.0",
		"--cache-node-type", "cache.t3.micro",
		"--num-cache-nodes", "1")

	out := run("elasticache", "describe-cache-clusters",
		"--cache-cluster-id", "cli-cache")
	var desc struct {
		CacheClusters []struct {
			CacheClusterId     string `json:"CacheClusterId"`
			CacheClusterStatus string `json:"CacheClusterStatus"`
		} `json:"CacheClusters"`
	}
	if err := json.Unmarshal(out, &desc); err != nil {
		t.Fatalf("decode describe: %v\nbody: %s", err, out)
	}
	if len(desc.CacheClusters) != 1 {
		t.Fatalf("count = %d, want 1", len(desc.CacheClusters))
	}

	run("elasticache", "delete-cache-cluster",
		"--cache-cluster-id", "cli-cache")
}
