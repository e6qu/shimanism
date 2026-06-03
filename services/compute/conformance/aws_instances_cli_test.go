// Conformance: AWS EC2 instance lifecycle driven by the official
// `aws ec2` CLI. Covers Phase 16.C: run-instances, describe-instances,
// stop-instances, start-instances, terminate-instances,
// describe-instance-types. Skipped if the `aws` binary is absent.
package conformance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

func TestAWSCLI_EC2_InstanceLifecycle(t *testing.T) {
	awsBin := requireAWSCLI(t)
	srv := harness.StartComputeServerAWS(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWSEC2(t, srv.URL, awsBin, args...)
		if err != nil {
			t.Fatalf("aws %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	// run-instances
	out := mustRun("ec2", "run-instances",
		"--image-id", "ami-12345678",
		"--instance-type", "t3.micro",
		"--count", "1",
	)
	var runResult struct {
		Instances []struct {
			InstanceId string `json:"InstanceId"`
			State      struct {
				Name string `json:"Name"`
			} `json:"State"`
		} `json:"Instances"`
	}
	if err := json.Unmarshal(out, &runResult); err != nil {
		t.Fatalf("parse run-instances output: %v\nraw: %s", err, out)
	}
	if len(runResult.Instances) == 0 {
		t.Fatalf("run-instances: no instances returned")
	}
	instanceID := runResult.Instances[0].InstanceId
	if instanceID == "" {
		t.Fatalf("run-instances: empty InstanceId")
	}
	if runResult.Instances[0].State.Name != "running" {
		t.Errorf("run-instances state = %q, want running", runResult.Instances[0].State.Name)
	}

	// describe-instances
	out = mustRun("ec2", "describe-instances", "--instance-ids", instanceID)
	var descResult struct {
		Reservations []struct {
			Instances []struct {
				InstanceId string `json:"InstanceId"`
			} `json:"Instances"`
		} `json:"Reservations"`
	}
	if err := json.Unmarshal(out, &descResult); err != nil {
		t.Fatalf("parse describe-instances: %v\nraw: %s", err, out)
	}
	if len(descResult.Reservations) == 0 || len(descResult.Reservations[0].Instances) == 0 {
		t.Fatalf("describe-instances: empty result")
	}

	// stop-instances
	out = mustRun("ec2", "stop-instances", "--instance-ids", instanceID)
	var stopResult struct {
		StoppingInstances []struct {
			CurrentState struct {
				Name string `json:"Name"`
			} `json:"CurrentState"`
		} `json:"StoppingInstances"`
	}
	if err := json.Unmarshal(out, &stopResult); err != nil {
		t.Fatalf("parse stop-instances: %v\nraw: %s", err, out)
	}
	if len(stopResult.StoppingInstances) == 0 {
		t.Fatalf("stop-instances: empty StoppingInstances")
	}
	if stopResult.StoppingInstances[0].CurrentState.Name != "stopped" {
		t.Errorf("stop state = %q, want stopped", stopResult.StoppingInstances[0].CurrentState.Name)
	}

	// start-instances
	out = mustRun("ec2", "start-instances", "--instance-ids", instanceID)
	var startResult struct {
		StartingInstances []struct {
			CurrentState struct {
				Name string `json:"Name"`
			} `json:"CurrentState"`
		} `json:"StartingInstances"`
	}
	if err := json.Unmarshal(out, &startResult); err != nil {
		t.Fatalf("parse start-instances: %v\nraw: %s", err, out)
	}
	if len(startResult.StartingInstances) == 0 {
		t.Fatalf("start-instances: empty StartingInstances")
	}
	if startResult.StartingInstances[0].CurrentState.Name != "running" {
		t.Errorf("start state = %q, want running", startResult.StartingInstances[0].CurrentState.Name)
	}

	// terminate-instances
	out = mustRun("ec2", "terminate-instances", "--instance-ids", instanceID)
	var termResult struct {
		TerminatingInstances []struct {
			CurrentState struct {
				Name string `json:"Name"`
			} `json:"CurrentState"`
		} `json:"TerminatingInstances"`
	}
	if err := json.Unmarshal(out, &termResult); err != nil {
		t.Fatalf("parse terminate-instances: %v\nraw: %s", err, out)
	}
	if len(termResult.TerminatingInstances) == 0 {
		t.Fatalf("terminate-instances: empty")
	}
	if termResult.TerminatingInstances[0].CurrentState.Name != "terminated" {
		t.Errorf("terminate state = %q, want terminated", termResult.TerminatingInstances[0].CurrentState.Name)
	}
}

func TestAWSCLI_EC2_DescribeInstanceTypes(t *testing.T) {
	awsBin := requireAWSCLI(t)
	srv := harness.StartComputeServerAWS(t, inmem.New())

	out, stderr, err := runAWSEC2(t, srv.URL, awsBin,
		"ec2", "describe-instance-types",
		"--instance-types", "t3.micro",
	)
	if err != nil {
		t.Fatalf("describe-instance-types: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		InstanceTypes []struct {
			InstanceType string `json:"InstanceType"`
			VCpuInfo     struct {
				DefaultVCpus int `json:"DefaultVCpus"`
			} `json:"VCpuInfo"`
		} `json:"InstanceTypes"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("parse describe-instance-types: %v\nraw: %s", err, out)
	}
	if len(result.InstanceTypes) == 0 {
		t.Fatalf("describe-instance-types: no types returned")
	}
	if result.InstanceTypes[0].InstanceType != "t3.micro" {
		t.Errorf("instance type = %q, want t3.micro", result.InstanceTypes[0].InstanceType)
	}
	if result.InstanceTypes[0].VCpuInfo.DefaultVCpus != 2 {
		t.Errorf("vCPUs = %d, want 2", result.InstanceTypes[0].VCpuInfo.DefaultVCpus)
	}
}
