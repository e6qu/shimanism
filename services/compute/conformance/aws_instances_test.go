// Conformance: AWS EC2-shaped instance lifecycle exercised by the
// official aws-sdk-go-v2/service/ec2 SDK. Covers Phase 16.C:
// RunInstances, DescribeInstances, StartInstances, StopInstances,
// TerminateInstances, RebootInstances, DescribeInstanceTypes.
package conformance_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

func TestAWSSDK_EC2_InstanceLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAWS(t, inmem.New())
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// RunInstances
	run, err := cli.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-12345678"),
		InstanceType: ec2types.InstanceTypeT3Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	if len(run.Instances) != 1 {
		t.Fatalf("RunInstances: expected 1 instance, got %d", len(run.Instances))
	}
	inst := run.Instances[0]
	instanceID := aws.ToString(inst.InstanceId)
	if instanceID == "" {
		t.Fatalf("RunInstances returned empty InstanceId")
	}
	if inst.State == nil || inst.State.Name != ec2types.InstanceStateNameRunning {
		t.Errorf("RunInstances state = %v, want running", inst.State)
	}
	if aws.ToString(inst.ImageId) != "ami-12345678" {
		t.Errorf("ImageId = %q, want ami-12345678", aws.ToString(inst.ImageId))
	}

	// DescribeInstances — by instance ID
	desc, err := cli.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}
	if len(desc.Reservations) == 0 || len(desc.Reservations[0].Instances) == 0 {
		t.Fatalf("DescribeInstances: empty reservations")
	}
	if aws.ToString(desc.Reservations[0].Instances[0].InstanceId) != instanceID {
		t.Errorf("DescribeInstances InstanceId = %q, want %q",
			aws.ToString(desc.Reservations[0].Instances[0].InstanceId), instanceID)
	}

	// StopInstances
	stop, err := cli.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		t.Fatalf("StopInstances: %v", err)
	}
	if len(stop.StoppingInstances) == 0 {
		t.Fatalf("StopInstances returned empty state list")
	}
	if stop.StoppingInstances[0].CurrentState.Name != ec2types.InstanceStateNameStopped {
		t.Errorf("StopInstances current state = %v, want stopped",
			stop.StoppingInstances[0].CurrentState.Name)
	}

	// StartInstances
	start, err := cli.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		t.Fatalf("StartInstances: %v", err)
	}
	if len(start.StartingInstances) == 0 {
		t.Fatalf("StartInstances returned empty state list")
	}
	if start.StartingInstances[0].CurrentState.Name != ec2types.InstanceStateNameRunning {
		t.Errorf("StartInstances current state = %v, want running",
			start.StartingInstances[0].CurrentState.Name)
	}

	// RebootInstances (returns empty response)
	_, err = cli.RebootInstances(ctx, &ec2.RebootInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		t.Fatalf("RebootInstances: %v", err)
	}

	// TerminateInstances
	term, err := cli.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}
	if len(term.TerminatingInstances) == 0 {
		t.Fatalf("TerminateInstances returned empty state list")
	}
	if term.TerminatingInstances[0].CurrentState.Name != ec2types.InstanceStateNameTerminated {
		t.Errorf("TerminateInstances current state = %v, want terminated",
			term.TerminatingInstances[0].CurrentState.Name)
	}

	// After termination, DescribeInstances should return empty
	desc2, err := cli.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		t.Fatalf("DescribeInstances post-terminate: %v", err)
	}
	total := 0
	for _, r := range desc2.Reservations {
		total += len(r.Instances)
	}
	if total != 0 {
		t.Errorf("expected 0 instances after terminate, got %d", total)
	}
}

func TestAWSSDK_EC2_DescribeInstanceTypes(t *testing.T) {
	srv := harness.StartComputeServerAWS(t, inmem.New())
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// All types
	all, err := cli.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{})
	if err != nil {
		t.Fatalf("DescribeInstanceTypes: %v", err)
	}
	if len(all.InstanceTypes) == 0 {
		t.Fatalf("DescribeInstanceTypes returned no types")
	}

	// Filter to t3.micro
	filtered, err := cli.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []ec2types.InstanceType{ec2types.InstanceTypeT3Micro},
	})
	if err != nil {
		t.Fatalf("DescribeInstanceTypes filtered: %v", err)
	}
	if len(filtered.InstanceTypes) != 1 {
		t.Fatalf("DescribeInstanceTypes filtered: expected 1, got %d", len(filtered.InstanceTypes))
	}
	if filtered.InstanceTypes[0].InstanceType != ec2types.InstanceTypeT3Micro {
		t.Errorf("InstanceType = %v, want t3.micro", filtered.InstanceTypes[0].InstanceType)
	}
	if filtered.InstanceTypes[0].VCpuInfo == nil || aws.ToInt32(filtered.InstanceTypes[0].VCpuInfo.DefaultVCpus) != 2 {
		t.Errorf("VCPUs = %v, want 2", filtered.InstanceTypes[0].VCpuInfo)
	}
}

func TestAWSSDK_EC2_RunInstances_Multiple(t *testing.T) {
	srv := harness.StartComputeServerAWS(t, inmem.New())
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	run, err := cli.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-abcdef12"),
		InstanceType: ec2types.InstanceTypeM5Large,
		MinCount:     aws.Int32(2),
		MaxCount:     aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("RunInstances(2): %v", err)
	}
	if len(run.Instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(run.Instances))
	}
	// IDs must be distinct
	if aws.ToString(run.Instances[0].InstanceId) == aws.ToString(run.Instances[1].InstanceId) {
		t.Errorf("RunInstances returned duplicate IDs")
	}
}
