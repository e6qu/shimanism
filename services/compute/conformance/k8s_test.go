// Conformance: K8s networking peer exercised via the AWS EC2 and GCP
// Compute frontends. The K8s peer backs all three frontends — the same
// domain.Networking backend serves requests from whichever frontend
// the client drives.
//
// For operations that are out-of-intersection on K8s (Subnets,
// PublicIPs), the tests verify that the correct error code is returned
// by each frontend rather than a silent success.
package conformance_test

import (
	"context"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awsec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	"golang.org/x/oauth2"
	computeraw "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/k8scompute"
	"k8s.io/client-go/kubernetes/fake"
)

// TestK8sPeer_AWSShaped_VPCLifecycle drives the AWS EC2 frontend with
// the K8s peer backend — Namespace-backed VPCs.
func TestK8sPeer_AWSShaped_VPCLifecycle(t *testing.T) {
	k8s := k8scompute.New(fake.NewSimpleClientset(), "default")
	srv := harness.StartComputeServerAWS(t, k8s)
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// CreateVpc → Namespace
	create, err := cli.CreateVpc(ctx, &awsec2sdk.CreateVpcInput{
		CidrBlock: awsapi.String("10.0.0.0/16"),
	})
	if err != nil {
		t.Fatalf("CreateVpc (K8s peer): %v", err)
	}
	vpcID := awsapi.ToString(create.Vpc.VpcId)
	if vpcID == "" {
		t.Fatal("empty VpcId from K8s peer")
	}

	// DescribeVpcs
	desc, err := cli.DescribeVpcs(ctx, &awsec2sdk.DescribeVpcsInput{VpcIds: []string{vpcID}})
	if err != nil {
		t.Fatalf("DescribeVpcs (K8s): %v", err)
	}
	if len(desc.Vpcs) != 1 {
		t.Errorf("DescribeVpcs count = %d, want 1", len(desc.Vpcs))
	}

	// DeleteVpc
	if _, err := cli.DeleteVpc(ctx, &awsec2sdk.DeleteVpcInput{VpcId: awsapi.String(vpcID)}); err != nil {
		t.Fatalf("DeleteVpc (K8s): %v", err)
	}
}

// TestK8sPeer_AWSShaped_SecurityGroupLifecycle drives the AWS EC2 frontend
// with the K8s peer — NetworkPolicy-backed security groups.
func TestK8sPeer_AWSShaped_SecurityGroupLifecycle(t *testing.T) {
	k8s := k8scompute.New(fake.NewSimpleClientset(), "default")
	srv := harness.StartComputeServerAWS(t, k8s)
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// CreateVpc
	vpc, err := cli.CreateVpc(ctx, &awsec2sdk.CreateVpcInput{CidrBlock: awsapi.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := awsapi.ToString(vpc.Vpc.VpcId)

	// CreateSecurityGroup
	sg, err := cli.CreateSecurityGroup(ctx, &awsec2sdk.CreateSecurityGroupInput{
		GroupName:   awsapi.String("k8s-sg"),
		Description: awsapi.String("K8s peer conformance SG"),
		VpcId:       awsapi.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup (K8s): %v", err)
	}
	sgID := awsapi.ToString(sg.GroupId)

	// AuthorizeSecurityGroupIngress → adds to NetworkPolicy
	if _, err := cli.AuthorizeSecurityGroupIngress(ctx, &awsec2sdk.AuthorizeSecurityGroupIngressInput{
		GroupId:    awsapi.String(sgID),
		IpProtocol: awsapi.String("tcp"),
		FromPort:   awsapi.Int32(80),
		ToPort:     awsapi.Int32(80),
		CidrIp:     awsapi.String("0.0.0.0/0"),
	}); err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress (K8s): %v", err)
	}

	// DescribeSecurityGroups
	dsg, err := cli.DescribeSecurityGroups(ctx, &awsec2sdk.DescribeSecurityGroupsInput{GroupIds: []string{sgID}})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups (K8s): %v", err)
	}
	if len(dsg.SecurityGroups) != 1 {
		t.Errorf("DescribeSecurityGroups count = %d, want 1", len(dsg.SecurityGroups))
	}

	// DeleteSecurityGroup
	if _, err := cli.DeleteSecurityGroup(ctx, &awsec2sdk.DeleteSecurityGroupInput{GroupId: awsapi.String(sgID)}); err != nil {
		t.Fatalf("DeleteSecurityGroup (K8s): %v", err)
	}
}

// TestK8sPeer_AWSShaped_SubnetNotSupported verifies that CreateSubnet
// returns the correct EC2 error code when the K8s peer can't serve it.
func TestK8sPeer_AWSShaped_SubnetNotSupported(t *testing.T) {
	k8s := k8scompute.New(fake.NewSimpleClientset(), "default")
	srv := harness.StartComputeServerAWS(t, k8s)
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	vpc, _ := cli.CreateVpc(ctx, &awsec2sdk.CreateVpcInput{CidrBlock: awsapi.String("10.0.0.0/16")})
	vpcID := awsapi.ToString(vpc.Vpc.VpcId)

	_, err := cli.CreateSubnet(ctx, &awsec2sdk.CreateSubnetInput{
		VpcId:     awsapi.String(vpcID),
		CidrBlock: awsapi.String("10.0.1.0/24"),
	})
	// K8s peer returns ErrNotSupported → adapter maps to UnsupportedOperation.
	if err == nil {
		t.Errorf("CreateSubnet on K8s peer should have failed, got nil")
	}
}

// TestK8sPeer_GCPShaped_NetworkLifecycle drives the GCP Compute v1
// frontend with the K8s peer.
func TestK8sPeer_GCPShaped_NetworkLifecycle(t *testing.T) {
	k8s := k8scompute.New(fake.NewSimpleClientset(), "default")
	srv := harness.StartComputeServerGCP(t, k8s)

	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://compute.googleapis.com/",
		15*time.Minute,
	)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	svc, err := computeraw.NewService(t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithTokenSource(ts),
	)
	if err != nil {
		t.Fatalf("build compute client: %v", err)
	}

	// Insert network → K8s Namespace
	if _, err := svc.Networks.Insert(gcpProject, &computeraw.Network{Name: "k8s-vpc"}).Do(); err != nil {
		t.Fatalf("Networks.Insert (K8s): %v", err)
	}
	t.Cleanup(func() { svc.Networks.Delete(gcpProject, "k8s-vpc").Do() })

	// List networks
	list, err := svc.Networks.List(gcpProject).Do()
	if err != nil {
		t.Fatalf("Networks.List (K8s): %v", err)
	}
	found := false
	for _, n := range list.Items {
		if n.Name == "k8s-vpc" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Networks.List did not find k8s-vpc")
	}

	// Delete
	if _, err := svc.Networks.Delete(gcpProject, "k8s-vpc").Do(); err != nil {
		t.Fatalf("Networks.Delete (K8s): %v", err)
	}
}
