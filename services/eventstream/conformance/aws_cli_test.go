// Conformance: AWS MSK-shaped control-plane exercised by the `aws kafka`
// CLI. Covers cluster create/describe/list/get-bootstrap-brokers/list-nodes/
// delete. Skipped if the `aws` binary isn't on PATH.
//
// Note: the AWS CLI `kafka` subcommand does not expose topic CRUD
// (create-topic, describe-topic, list-topics, delete-topic); those
// operations are tested via the SDK conformance test.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/eventstream/backends/inmem"
)

func requireAWSKafkaCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed: %v", err)
	}
	return bin
}

func runAWSKafka(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{
		"--endpoint-url=" + srvURL,
		"--no-cli-pager",
		"--output", "json",
		"kafka",
	}, args...)...)
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

func TestAWSCLI_MSK_ClusterLifecycle(t *testing.T) {
	awsBin := requireAWSKafkaCLI(t)
	backend := inmem.New()
	kafkaSrv := harness.StartEventStreamKafkaServer(t, backend)
	srv := harness.StartEventStreamServerAWS(t, backend, []string{kafkaSrv.Address})

	run := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWSKafka(t, srv.URL, awsBin, args...)
		if err != nil {
			t.Fatalf("aws kafka %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	// create-cluster
	out := run("create-cluster",
		"--cluster-name", "shimcluster",
		"--kafka-version", "2.8.0",
		"--number-of-broker-nodes", "1",
		"--broker-node-group-info", `{"InstanceType":"kafka.m5.large","ClientSubnets":["subnet-00000000"]}`,
	)
	var created struct {
		ClusterArn  string `json:"ClusterArn"`
		ClusterName string `json:"ClusterName"`
		State       string `json:"State"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("parse create-cluster: %v\nraw: %s", err, out)
	}
	if created.ClusterArn == "" {
		t.Fatal("create-cluster returned empty ClusterArn")
	}
	if !strings.Contains(created.ClusterArn, "shimcluster") {
		t.Errorf("ClusterArn = %q, want shimcluster in ARN", created.ClusterArn)
	}
	arn := created.ClusterArn

	// describe-cluster
	out = run("describe-cluster", "--cluster-arn", arn)
	var described struct {
		ClusterInfo struct {
			ClusterName string `json:"ClusterName"`
			State       string `json:"State"`
		} `json:"ClusterInfo"`
	}
	if err := json.Unmarshal(out, &described); err != nil {
		t.Fatalf("parse describe-cluster: %v\nraw: %s", err, out)
	}
	if described.ClusterInfo.ClusterName != "shimcluster" {
		t.Errorf("ClusterName = %q, want shimcluster", described.ClusterInfo.ClusterName)
	}

	// list-clusters — our cluster must appear
	out = run("list-clusters")
	var listed struct {
		ClusterInfoList []struct {
			ClusterArn string `json:"ClusterArn"`
		} `json:"ClusterInfoList"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		t.Fatalf("parse list-clusters: %v\nraw: %s", err, out)
	}
	var found bool
	for _, c := range listed.ClusterInfoList {
		if c.ClusterArn == arn {
			found = true
		}
	}
	if !found {
		t.Fatalf("list-clusters: ARN %q not found in %s", arn, out)
	}

	// get-bootstrap-brokers
	out = run("get-bootstrap-brokers", "--cluster-arn", arn)
	var bootstrap struct {
		BootstrapBrokerString string `json:"BootstrapBrokerString"`
	}
	if err := json.Unmarshal(out, &bootstrap); err != nil {
		t.Fatalf("parse get-bootstrap-brokers: %v\nraw: %s", err, out)
	}
	if bootstrap.BootstrapBrokerString == "" {
		t.Error("get-bootstrap-brokers returned empty BootstrapBrokerString")
	}

	// list-nodes
	out = run("list-nodes", "--cluster-arn", arn)
	var nodes struct {
		NodeInfoList []struct {
			NodeARN string `json:"NodeARN"`
		} `json:"NodeInfoList"`
	}
	if err := json.Unmarshal(out, &nodes); err != nil {
		t.Fatalf("parse list-nodes: %v\nraw: %s", err, out)
	}
	if len(nodes.NodeInfoList) == 0 {
		t.Error("list-nodes returned empty NodeInfoList")
	}

	// delete-cluster
	run("delete-cluster", "--cluster-arn", arn)
	// verify cluster is no longer in list
	out = run("list-clusters")
	var afterDel struct {
		ClusterInfoList []struct {
			ClusterArn string `json:"ClusterArn"`
		} `json:"ClusterInfoList"`
	}
	if err := json.Unmarshal(out, &afterDel); err == nil {
		for _, c := range afterDel.ClusterInfoList {
			if c.ClusterArn == arn {
				t.Error("delete-cluster: cluster still listed after delete")
			}
		}
	}
}
