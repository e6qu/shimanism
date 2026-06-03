// Phase 16.C exit criterion: cross-cloud instance Apply cell.
// A single inmem backend serves both the AWS EC2 frontend and the GCP
// Compute Engine frontend. An instance created through the AWS frontend
// is visible through the GCP frontend — validating that the domain
// interface is a neutral bridge between source clouds.
package conformance_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

// TestCrossCloudApply_Roundtrip_Compute_AWStoGCP creates an instance via
// the AWS EC2 frontend and reads it back via the GCP Compute frontend.
// Both frontends are backed by the same inmem Backend.
func TestCrossCloudApply_Roundtrip_Compute_AWStoGCP(t *testing.T) {
	backend := inmem.New()
	awsSrv := harness.StartComputeServerAWS(t, backend)
	gcpSrv := harness.StartComputeServerGCP(t, backend)

	ctx := context.Background()

	// ── Step 1: create instance via AWS EC2 frontend ──────────────────
	ec2Cli := newEC2Client(t, awsSrv.URL)
	runOut, err := ec2Cli.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-crosscloud"),
		InstanceType: ec2types.InstanceTypeT3Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	if len(runOut.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(runOut.Instances))
	}
	awsID := aws.ToString(runOut.Instances[0].InstanceId)
	if awsID == "" {
		t.Fatal("empty instance ID from RunInstances")
	}

	// ── Step 2: read the same instance via GCP Compute frontend ──────
	// The GCP instance's Name comes from domain.Instance.ID (the AWS ID
	// is the domain ID), and MachineType encodes the instance type.
	gcpCli := newGCPComputeClient(t, gcpSrv.URL)
	listOut, err := gcpCli.Instances.List(gcpProject, "us-central1-a").Context(ctx).Do()
	if err != nil {
		t.Fatalf("GCP instances.list: %v", err)
	}
	if len(listOut.Items) == 0 {
		t.Fatal("GCP instances.list: no instances visible after AWS RunInstances")
	}

	// Find an instance with t3.micro machine type and RUNNING status —
	// proves the domain state is shared between frontends.
	found := false
	for _, item := range listOut.Items {
		if strings.HasSuffix(item.MachineType, "t3.micro") && item.Status == "RUNNING" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GCP instances.list: no t3.micro RUNNING instance visible after AWS RunInstances (got %d items)", len(listOut.Items))
	}

	// ── Step 3: terminate via AWS, confirm GCP sees terminated state ──
	_, err = ec2Cli.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{awsID},
	})
	if err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}

	listOut2, err := gcpCli.Instances.List(gcpProject, "us-central1-a").Context(ctx).Do()
	if err != nil {
		t.Fatalf("GCP instances.list (post-terminate): %v", err)
	}
	for _, item := range listOut2.Items {
		if strings.HasSuffix(item.MachineType, "t3.micro") && item.Status == "RUNNING" {
			t.Errorf("GCP still sees a RUNNING t3.micro instance after AWS TerminateInstances")
		}
	}
}
