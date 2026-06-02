package inmem_test

import (
	"context"
	"testing"

	"github.com/e6qu/shimanism/internal/compute/domain"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

func TestNetwork_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()

	n, err := b.CreateNetwork(ctx, "vpc-test", domain.CreateNetworkOptions{CIDR: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n.Name != "vpc-test" || n.CIDR != "10.0.0.0/16" || n.ID == "" {
		t.Fatalf("unexpected network: %+v", n)
	}

	got, err := b.GetNetwork(ctx, n.ID)
	if err != nil || got.Name != "vpc-test" {
		t.Fatalf("get: %v %+v", err, got)
	}

	res, err := b.ListNetworks(ctx, domain.ListNetworksOptions{})
	if err != nil || len(res.Networks) != 1 {
		t.Fatalf("list: %v %d", err, len(res.Networks))
	}

	if err := b.DeleteNetwork(ctx, n.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := b.GetNetwork(ctx, n.ID); !domain.IsNotFound(err) {
		t.Fatalf("expected not found after delete, got: %v", err)
	}
}

func TestNetwork_DuplicateNameRejected(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()
	if _, err := b.CreateNetwork(ctx, "vpc-dup", domain.CreateNetworkOptions{}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := b.CreateNetwork(ctx, "vpc-dup", domain.CreateNetworkOptions{}); !domain.IsAlreadyExists(err) {
		t.Fatalf("expected AlreadyExists, got: %v", err)
	}
}

func TestSubnet_RequiresParentNetwork(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()
	_, err := b.CreateSubnet(ctx, "sub", domain.CreateSubnetOptions{NetworkID: "nonexistent", CIDR: "10.0.1.0/24"})
	if !domain.IsNotFound(err) {
		t.Fatalf("expected not found for missing parent, got: %v", err)
	}
}

func TestSubnet_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()
	n, _ := b.CreateNetwork(ctx, "vpc", domain.CreateNetworkOptions{CIDR: "10.0.0.0/16"})

	s, err := b.CreateSubnet(ctx, "sub-a", domain.CreateSubnetOptions{
		NetworkID: n.ID, CIDR: "10.0.1.0/24", Zone: "us-east-1a",
	})
	if err != nil || s.NetworkID != n.ID || s.Zone != "us-east-1a" {
		t.Fatalf("create subnet: %v %+v", err, s)
	}

	res, err := b.ListSubnets(ctx, domain.ListSubnetsOptions{NetworkID: n.ID})
	if err != nil || len(res.Subnets) != 1 {
		t.Fatalf("list subnets: %v %d", err, len(res.Subnets))
	}

	if err := b.DeleteSubnet(ctx, s.ID); err != nil {
		t.Fatalf("delete subnet: %v", err)
	}
}

func TestSecurityGroup_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()
	n, _ := b.CreateNetwork(ctx, "vpc", domain.CreateNetworkOptions{})

	sg, err := b.CreateSecurityGroup(ctx, "sg-web", domain.CreateSecurityGroupOptions{
		NetworkID: n.ID, Description: "web tier",
	})
	if err != nil || sg.Name != "sg-web" {
		t.Fatalf("create SG: %v %+v", err, sg)
	}

	rule := domain.SecurityGroupRule{
		Protocol: domain.ProtocolTCP, PortFrom: 80, PortTo: 80,
		CIDRs: []string{"0.0.0.0/0"}, Direction: domain.Inbound,
	}
	if err := b.AddRule(ctx, sg.ID, rule); err != nil {
		t.Fatalf("add rule: %v", err)
	}

	got, err := b.GetSecurityGroup(ctx, sg.ID)
	if err != nil || len(got.Rules) != 1 {
		t.Fatalf("get SG after add rule: %v rules=%d", err, len(got.Rules))
	}

	if err := b.RemoveRule(ctx, sg.ID, rule); err != nil {
		t.Fatalf("remove rule: %v", err)
	}
	got, _ = b.GetSecurityGroup(ctx, sg.ID)
	if len(got.Rules) != 0 {
		t.Fatalf("expected 0 rules after remove, got %d", len(got.Rules))
	}

	if err := b.DeleteSecurityGroup(ctx, sg.ID); err != nil {
		t.Fatalf("delete SG: %v", err)
	}
}

func TestPublicIP_AllocateAssociateRelease(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()

	ip, err := b.AllocatePublicIP(ctx, domain.AllocatePublicIPOptions{Region: "us-east-1"})
	if err != nil || ip.Address == "" {
		t.Fatalf("allocate: %v %+v", err, ip)
	}
	if ip.InstanceID != "" {
		t.Fatal("freshly allocated IP should not be associated")
	}

	if err := b.AssociatePublicIP(ctx, ip.ID, "i-123"); err != nil {
		t.Fatalf("associate: %v", err)
	}
	listed, _ := b.ListPublicIPs(ctx, domain.ListPublicIPsOptions{})
	if listed.PublicIPs[0].InstanceID != "i-123" {
		t.Fatalf("expected instance association, got %q", listed.PublicIPs[0].InstanceID)
	}

	if err := b.DisassociatePublicIP(ctx, ip.ID); err != nil {
		t.Fatalf("disassociate: %v", err)
	}

	if err := b.ReleasePublicIP(ctx, ip.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := b.ListPublicIPs(ctx, domain.ListPublicIPsOptions{}); err != nil {
		t.Fatalf("list after release: %v", err)
	}
	res, _ := b.ListPublicIPs(ctx, domain.ListPublicIPsOptions{})
	if len(res.PublicIPs) != 0 {
		t.Fatalf("expected 0 IPs after release, got %d", len(res.PublicIPs))
	}
}

func TestPublicIP_ReleaseNotFound(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()
	if err := b.ReleasePublicIP(ctx, "nonexistent"); !domain.IsNotFound(err) {
		t.Fatalf("expected not found, got: %v", err)
	}
}

func TestInstance_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()

	// RunInstances
	instances, err := b.RunInstances(ctx, domain.RunInstancesOptions{
		ImageID:      "ami-12345678",
		InstanceType: "t3.micro",
		MinCount:     1,
		MaxCount:     1,
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("RunInstances: expected 1 instance, got %d", len(instances))
	}
	inst := instances[0]
	if inst.State != domain.InstanceStateRunning {
		t.Errorf("state = %q, want running", inst.State)
	}
	if inst.PrivateIP == "" {
		t.Errorf("PrivateIP empty")
	}

	// DescribeInstances
	res, err := b.DescribeInstances(ctx, domain.DescribeInstancesOptions{IDs: []string{inst.ID}})
	if err != nil || len(res.Instances) != 1 {
		t.Fatalf("DescribeInstances: %v count=%d", err, len(res.Instances))
	}

	// StopInstances
	stopped, err := b.StopInstances(ctx, []string{inst.ID})
	if err != nil || stopped[0].State != domain.InstanceStateStopped {
		t.Fatalf("StopInstances: %v state=%s", err, stopped[0].State)
	}

	// StartInstances
	started, err := b.StartInstances(ctx, []string{inst.ID})
	if err != nil || started[0].State != domain.InstanceStateRunning {
		t.Fatalf("StartInstances: %v state=%s", err, started[0].State)
	}

	// RebootInstances
	if err := b.RebootInstances(ctx, []string{inst.ID}); err != nil {
		t.Fatalf("RebootInstances: %v", err)
	}

	// TerminateInstances
	terminated, err := b.TerminateInstances(ctx, []string{inst.ID})
	if err != nil || terminated[0].State != domain.InstanceStateTerminated {
		t.Fatalf("TerminateInstances: %v state=%s", err, terminated[0].State)
	}

	// Should be gone
	res, _ = b.DescribeInstances(ctx, domain.DescribeInstancesOptions{IDs: []string{inst.ID}})
	if len(res.Instances) != 0 {
		t.Errorf("expected 0 instances after terminate, got %d", len(res.Instances))
	}
}

func TestInstance_RunMultiple(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()

	instances, err := b.RunInstances(ctx, domain.RunInstancesOptions{
		ImageID:      "ami-12345678",
		InstanceType: "m5.large",
		MinCount:     3,
		MaxCount:     3,
	})
	if err != nil || len(instances) != 3 {
		t.Fatalf("RunInstances: %v count=%d", err, len(instances))
	}
	res, err := b.DescribeInstances(ctx, domain.DescribeInstancesOptions{})
	if err != nil || len(res.Instances) != 3 {
		t.Fatalf("DescribeInstances all: %v count=%d", err, len(res.Instances))
	}
}

func TestDescribeInstanceTypes(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()

	// All types
	res, err := b.DescribeInstanceTypes(ctx, domain.DescribeInstanceTypesOptions{})
	if err != nil || len(res.InstanceTypes) == 0 {
		t.Fatalf("DescribeInstanceTypes all: %v count=%d", err, len(res.InstanceTypes))
	}

	// Filter to t3.micro
	res, err = b.DescribeInstanceTypes(ctx, domain.DescribeInstanceTypesOptions{
		InstanceTypes: []string{"t3.micro"},
	})
	if err != nil || len(res.InstanceTypes) != 1 || res.InstanceTypes[0].VCPUs != 2 {
		t.Fatalf("DescribeInstanceTypes filtered: %v count=%d", err, len(res.InstanceTypes))
	}
}
