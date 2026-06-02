// Conformance: AWS EC2-shaped frontend exercised by the official
// `aws ec2` CLI. Each command shells out against the shim's endpoint.
// Skipped if the `aws` binary isn't on PATH.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

func requireAWSCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed: %v", err)
	}
	return bin
}

func runAWSEC2(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{
		"--endpoint-url=" + srvURL,
		"--no-cli-pager",
		"--output", "json",
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

func TestAWSCLI_EC2_VPCLifecycle(t *testing.T) {
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

	// create-vpc
	created := mustRun("ec2", "create-vpc", "--cidr-block", "10.10.0.0/16")
	var createOut struct {
		Vpc struct {
			VpcId     string `json:"VpcId"`
			CidrBlock string `json:"CidrBlock"`
			State     string `json:"State"`
		} `json:"Vpc"`
	}
	if err := json.Unmarshal(created, &createOut); err != nil {
		t.Fatalf("parse create-vpc: %v\n%s", err, created)
	}
	if createOut.Vpc.VpcId == "" {
		t.Fatalf("create-vpc returned empty VpcId")
	}
	if createOut.Vpc.CidrBlock != "10.10.0.0/16" {
		t.Errorf("CidrBlock = %q", createOut.Vpc.CidrBlock)
	}
	vpcID := createOut.Vpc.VpcId

	// describe-vpcs
	described := mustRun("ec2", "describe-vpcs", "--vpc-ids", vpcID)
	var descOut struct {
		Vpcs []struct {
			VpcId string `json:"VpcId"`
		} `json:"Vpcs"`
	}
	if err := json.Unmarshal(described, &descOut); err != nil {
		t.Fatalf("parse describe-vpcs: %v\n%s", err, described)
	}
	if len(descOut.Vpcs) != 1 || descOut.Vpcs[0].VpcId != vpcID {
		t.Errorf("describe-vpcs: got %v, want [%s]", descOut.Vpcs, vpcID)
	}

	// delete-vpc
	mustRun("ec2", "delete-vpc", "--vpc-id", vpcID)
}

func TestAWSCLI_EC2_SecurityGroupLifecycle(t *testing.T) {
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

	// Create parent VPC
	vpcOut := mustRun("ec2", "create-vpc", "--cidr-block", "10.20.0.0/16")
	var vo struct {
		Vpc struct {
			VpcId string `json:"VpcId"`
		} `json:"Vpc"`
	}
	json.Unmarshal(vpcOut, &vo)
	vpcID := vo.Vpc.VpcId
	defer mustRun("ec2", "delete-vpc", "--vpc-id", vpcID)

	// create-security-group
	sgOut := mustRun("ec2", "create-security-group",
		"--group-name", "cli-sg",
		"--description", "CLI conformance SG",
		"--vpc-id", vpcID,
	)
	var so struct {
		GroupId string `json:"GroupId"`
	}
	if err := json.Unmarshal(sgOut, &so); err != nil || so.GroupId == "" {
		t.Fatalf("create-security-group: %v\n%s", err, sgOut)
	}
	sgID := so.GroupId
	defer mustRun("ec2", "delete-security-group", "--group-id", sgID)

	// authorize-security-group-ingress
	mustRun("ec2", "authorize-security-group-ingress",
		"--group-id", sgID,
		"--protocol", "tcp",
		"--port", "443",
		"--cidr", "0.0.0.0/0",
	)

	// describe-security-groups
	dsgOut := mustRun("ec2", "describe-security-groups", "--group-ids", sgID)
	var dso struct {
		SecurityGroups []struct {
			GroupId       string `json:"GroupId"`
			IpPermissions []struct {
				IpProtocol string `json:"IpProtocol"`
			} `json:"IpPermissions"`
		} `json:"SecurityGroups"`
	}
	if err := json.Unmarshal(dsgOut, &dso); err != nil {
		t.Fatalf("parse describe-security-groups: %v\n%s", err, dsgOut)
	}
	if len(dso.SecurityGroups) != 1 {
		t.Errorf("describe-security-groups count = %d, want 1", len(dso.SecurityGroups))
	}
	if len(dso.SecurityGroups) > 0 && len(dso.SecurityGroups[0].IpPermissions) != 1 {
		t.Errorf("IpPermissions count = %d, want 1", len(dso.SecurityGroups[0].IpPermissions))
	}
}
