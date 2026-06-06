// Conformance: GCP Managed Service for Apache Kafka exercised by the
// `gcloud managed-kafka` CLI. The CLOUDSDK_API_ENDPOINT_OVERRIDES_MANAGEDKAFKA
// env var redirects the CLI to the shim. Covers cluster create/describe/list/
// delete and topic create/describe/list/delete. Skipped if `gcloud` isn't on PATH.
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
	"github.com/e6qu/shimanism/services/eventstream/backends/inmem"
)

const gcpMKProject = "shim-conformance"
const gcpMKLocation = "us-central1"

func requireGcloudManagedKafka(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		t.Skipf("gcloud not installed: %v", err)
	}
	return bin
}

func runGcloudManagedKafka(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	full := strings.TrimRight(srvURL, "/") + "/"
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://managedkafka.googleapis.com/",
		15*time.Minute,
	)
	cmd := exec.Command(bin, append([]string{"--quiet"}, args...)...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_MANAGEDKAFKA="+full,
		"CLOUDSDK_AUTH_ACCESS_TOKEN="+jwt,
		"CLOUDSDK_CORE_DISABLE_PROMPTS=1",
		"CLOUDSDK_CORE_PROJECT="+gcpMKProject,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestGCPCLI_ManagedKafka_ClusterAndTopicLifecycle(t *testing.T) {
	gcloud := requireGcloudManagedKafka(t)
	srv := harness.StartEventStreamServerGCP(t, inmem.New())

	run := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runGcloudManagedKafka(t, srv.URL, gcloud, args...)
		if err != nil {
			t.Fatalf("gcloud %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	const (
		clusterID = "shim-cli-cluster"
		topicID   = "shim-cli-topic"
		subnet    = "projects/shim-conformance/regions/us-central1/subnetworks/default"
	)

	// cluster create
	run("managed-kafka", "clusters", "create", clusterID,
		"--location="+gcpMKLocation,
		"--cpu=3", "--memory=3GiB",
		"--subnets="+subnet,
	)

	// cluster describe
	out := run("managed-kafka", "clusters", "describe", clusterID,
		"--location="+gcpMKLocation,
	)
	if !strings.Contains(string(out), clusterID) {
		t.Errorf("cluster describe: missing %q in output:\n%s", clusterID, out)
	}

	// cluster list
	out = run("managed-kafka", "clusters", "list",
		"--location="+gcpMKLocation,
	)
	if !strings.Contains(string(out), clusterID) {
		t.Errorf("cluster list: missing %q in output:\n%s", clusterID, out)
	}

	// topic create
	run("managed-kafka", "topics", "create", topicID,
		"--cluster="+clusterID, "--location="+gcpMKLocation,
		"--partitions=1", "--replication-factor=1",
	)

	// topic describe
	out = run("managed-kafka", "topics", "describe", topicID,
		"--cluster="+clusterID, "--location="+gcpMKLocation,
	)
	if !strings.Contains(string(out), topicID) {
		t.Errorf("topic describe: missing %q in output:\n%s", topicID, out)
	}

	// topic list — cluster is a positional argument for the list command
	out = run("managed-kafka", "topics", "list", clusterID,
		"--location="+gcpMKLocation,
	)
	if !strings.Contains(string(out), topicID) {
		t.Errorf("topic list: missing %q in output:\n%s", topicID, out)
	}

	// topic delete
	run("managed-kafka", "topics", "delete", topicID,
		"--cluster="+clusterID, "--location="+gcpMKLocation,
	)
	out = run("managed-kafka", "topics", "list", clusterID,
		"--location="+gcpMKLocation,
	)
	if strings.Contains(string(out), topicID) {
		t.Errorf("topic list after delete: still contains %q:\n%s", topicID, out)
	}

	// cluster delete
	run("managed-kafka", "clusters", "delete", clusterID,
		"--location="+gcpMKLocation,
	)
	out = run("managed-kafka", "clusters", "list",
		"--location="+gcpMKLocation,
	)
	if strings.Contains(string(out), clusterID) {
		t.Errorf("cluster list after delete: still contains %q:\n%s", clusterID, out)
	}
}
