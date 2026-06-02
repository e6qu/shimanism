package k8scompute_test

import (
	"context"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/e6qu/shimanism/internal/compute/domain"
	"github.com/e6qu/shimanism/services/compute/backends/k8scompute"
)

func TestK8s_Network_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := k8scompute.New(fake.NewSimpleClientset(), "default")

	// CreateNetwork
	n, err := b.CreateNetwork(ctx, "my-vpc", domain.CreateNetworkOptions{CIDR: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	if n.ID != "my-vpc" || n.CIDR != "10.0.0.0/16" {
		t.Fatalf("unexpected network: %+v", n)
	}

	// GetNetwork
	got, err := b.GetNetwork(ctx, "my-vpc")
	if err != nil {
		t.Fatalf("GetNetwork: %v", err)
	}
	if got.ID != "my-vpc" {
		t.Errorf("GetNetwork ID = %q", got.ID)
	}

	// ListNetworks
	res, err := b.ListNetworks(ctx, domain.ListNetworksOptions{})
	if err != nil {
		t.Fatalf("ListNetworks: %v", err)
	}
	if len(res.Networks) != 1 {
		t.Errorf("ListNetworks count = %d, want 1", len(res.Networks))
	}

	// Duplicate name rejected
	_, err = b.CreateNetwork(ctx, "my-vpc", domain.CreateNetworkOptions{})
	if !domain.IsAlreadyExists(err) {
		t.Errorf("expected AlreadyExists on duplicate, got: %v", err)
	}

	// DeleteNetwork
	if err := b.DeleteNetwork(ctx, "my-vpc"); err != nil {
		t.Fatalf("DeleteNetwork: %v", err)
	}
	if _, err := b.GetNetwork(ctx, "my-vpc"); !domain.IsNotFound(err) {
		t.Errorf("expected NotFound after delete, got: %v", err)
	}
}

func TestK8s_SecurityGroup_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := k8scompute.New(fake.NewSimpleClientset(), "default")

	// CreateSecurityGroup
	sg, err := b.CreateSecurityGroup(ctx, "web-sg", domain.CreateSecurityGroupOptions{
		Description: "web tier",
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}
	if sg.ID != "web-sg" {
		t.Fatalf("unexpected SG: %+v", sg)
	}

	// AddRule (inbound TCP 80)
	rule := domain.SecurityGroupRule{
		Protocol: "tcp", PortFrom: 80, PortTo: 80,
		CIDRs: []string{"0.0.0.0/0"}, Direction: domain.Inbound,
	}
	if err := b.AddRule(ctx, "web-sg", rule); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	// GetSecurityGroup — verify rule present
	got, err := b.GetSecurityGroup(ctx, "web-sg")
	if err != nil {
		t.Fatalf("GetSecurityGroup: %v", err)
	}
	if len(got.Rules) != 1 {
		t.Errorf("Rules count = %d, want 1", len(got.Rules))
	}
	if got.Rules[0].Protocol != "tcp" || got.Rules[0].PortFrom != 80 {
		t.Errorf("unexpected rule: %+v", got.Rules[0])
	}

	// ListSecurityGroups
	list, err := b.ListSecurityGroups(ctx, domain.ListSecurityGroupsOptions{})
	if err != nil {
		t.Fatalf("ListSecurityGroups: %v", err)
	}
	if len(list.SecurityGroups) != 1 {
		t.Errorf("ListSecurityGroups count = %d, want 1", len(list.SecurityGroups))
	}

	// RemoveRule
	if err := b.RemoveRule(ctx, "web-sg", rule); err != nil {
		t.Fatalf("RemoveRule: %v", err)
	}
	got, _ = b.GetSecurityGroup(ctx, "web-sg")
	if len(got.Rules) != 0 {
		t.Errorf("Rules after remove = %d, want 0", len(got.Rules))
	}

	// DeleteSecurityGroup
	if err := b.DeleteSecurityGroup(ctx, "web-sg"); err != nil {
		t.Fatalf("DeleteSecurityGroup: %v", err)
	}
	if _, err := b.GetSecurityGroup(ctx, "web-sg"); !domain.IsNotFound(err) {
		t.Errorf("expected NotFound after delete, got: %v", err)
	}
}

func TestK8s_Subnet_NotSupported(t *testing.T) {
	ctx := context.Background()
	b := k8scompute.New(fake.NewSimpleClientset(), "default")
	_, err := b.CreateSubnet(ctx, "sub", domain.CreateSubnetOptions{NetworkID: "n"})
	if !domain.IsNotSupported(err) {
		t.Errorf("expected NotSupported for CreateSubnet, got: %v", err)
	}
}

func TestK8s_PublicIP_NotSupported(t *testing.T) {
	ctx := context.Background()
	b := k8scompute.New(fake.NewSimpleClientset(), "default")
	_, err := b.AllocatePublicIP(ctx, domain.AllocatePublicIPOptions{})
	if !domain.IsNotSupported(err) {
		t.Errorf("expected NotSupported for AllocatePublicIP, got: %v", err)
	}
}
