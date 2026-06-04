// Sockerless lane for the compute service, Phase 16.B + 16.C.
//
// Through-shim paths:
//
//	AWS lane: AWS SDK EC2 → shim EC2 frontend → AWS EC2 backend → sockerless.
//	Azure lane: azurerm Terraform → shim azure_compute frontend → inmem backend;
//	  Microsoft.Network + resource-group paths forwarded to sockerless ARM mock.
//
// VPC/SG/EIP calls are pure metadata; instance lifecycle requires KVM
// (sockerless #373/#374/#375 closed by PR #372).
//
// Set SOCKERLESS_AWS_ENDPOINT for the AWS lane.
// Set SOCKERLESS_AZURE_TLS_PORT + SOCKERLESS_AZURE_TLS_CERT for the Azure lane.
package conformance_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/e6qu/shimanism/internal/azurebearer"
	azurecomputefront "github.com/e6qu/shimanism/internal/compute/frontends/azure_compute"
	"github.com/e6qu/shimanism/internal/harness"
	awsbackend "github.com/e6qu/shimanism/services/compute/backends/aws"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

// ─── helpers ─────────────────────────────────────────────────────────

func newSockerlessAWSEC2Client(t *testing.T, endpoint string) *awsec2sdk.Client {
	t.Helper()
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Setenv("AWS_ACCESS_KEY_ID", "test")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	}
	if os.Getenv("AWS_REGION") == "" {
		t.Setenv("AWS_REGION", "us-east-1")
	}
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{AccessKeyID: "test", SecretAccessKey: "test"},
		}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	if os.Getenv("AWS_S3_CONFORMANCE_INSECURE_TLS") == "1" {
		cfg.HTTPClient = awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
		})
	}
	return awsec2sdk.NewFromConfig(cfg, func(o *awsec2sdk.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
}

// ─── Tests ───────────────────────────────────────────────────────────

// TestSockerless_AWSEC2_Through_Shim_VPCLifecycle drives:
//
//	AWS SDK → shim's EC2 frontend → AWS EC2 backend → sockerless EC2 sim.
//
// VPC create/describe/delete — no Firecracker required.
func TestSockerless_AWSEC2_Through_Shim_VPCLifecycle(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}

	// Backend leg: shim's AWS EC2 backend → sockerless EC2 sim.
	backendClient := newSockerlessAWSEC2Client(t, endpoint)
	backend := awsbackend.New(backendClient)
	shim := harness.StartComputeServerAWS(t, backend)

	// Frontend leg: official EC2 SDK → shim.
	frontendClient := newEC2Client(t, shim.URL)
	ctx := context.Background()

	// CreateVpc through shim → sockerless.
	create, err := frontendClient.CreateVpc(ctx, &awsec2sdk.CreateVpcInput{
		CidrBlock: awsapi.String("10.1.0.0/16"),
	})
	if err != nil {
		t.Fatalf("CreateVpc (through shim → sockerless): %v", err)
	}
	vpcID := awsapi.ToString(create.Vpc.VpcId)
	if vpcID == "" {
		t.Fatalf("CreateVpc returned empty VpcId")
	}
	t.Cleanup(func() {
		frontendClient.DeleteVpc(ctx, &awsec2sdk.DeleteVpcInput{VpcId: awsapi.String(vpcID)})
	})

	// DescribeVpcs — verify presence.
	desc, err := frontendClient.DescribeVpcs(ctx, &awsec2sdk.DescribeVpcsInput{
		VpcIds: []string{vpcID},
	})
	if err != nil {
		t.Fatalf("DescribeVpcs: %v", err)
	}
	if len(desc.Vpcs) != 1 || awsapi.ToString(desc.Vpcs[0].VpcId) != vpcID {
		t.Errorf("DescribeVpcs: got %d VPCs, want 1 with id %q", len(desc.Vpcs), vpcID)
	}

	// DeleteVpc.
	if _, err := frontendClient.DeleteVpc(ctx, &awsec2sdk.DeleteVpcInput{VpcId: awsapi.String(vpcID)}); err != nil {
		t.Fatalf("DeleteVpc: %v", err)
	}
}

// TestSockerless_AWSEC2_Through_Shim_SecurityGroup drives:
//
//	AWS SDK → shim → AWS backend → sockerless.
//
// SecurityGroup create/authorize-ingress/describe/delete.
func TestSockerless_AWSEC2_Through_Shim_SecurityGroup(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}

	backendClient := newSockerlessAWSEC2Client(t, endpoint)
	backend := awsbackend.New(backendClient)
	shim := harness.StartComputeServerAWS(t, backend)
	frontendClient := newEC2Client(t, shim.URL)
	ctx := context.Background()

	// CreateVpc as parent.
	vpc, err := frontendClient.CreateVpc(ctx, &awsec2sdk.CreateVpcInput{
		CidrBlock: awsapi.String("10.2.0.0/16"),
	})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := awsapi.ToString(vpc.Vpc.VpcId)
	t.Cleanup(func() {
		frontendClient.DeleteVpc(ctx, &awsec2sdk.DeleteVpcInput{VpcId: awsapi.String(vpcID)})
	})

	// CreateSecurityGroup.
	sg, err := frontendClient.CreateSecurityGroup(ctx, &awsec2sdk.CreateSecurityGroupInput{
		GroupName:   awsapi.String("sockerless-sg"),
		Description: awsapi.String("sockerless conformance SG"),
		VpcId:       awsapi.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}
	sgID := awsapi.ToString(sg.GroupId)
	t.Cleanup(func() {
		frontendClient.DeleteSecurityGroup(ctx, &awsec2sdk.DeleteSecurityGroupInput{GroupId: awsapi.String(sgID)})
	})

	// AuthorizeSecurityGroupIngress.
	if _, err := frontendClient.AuthorizeSecurityGroupIngress(ctx, &awsec2sdk.AuthorizeSecurityGroupIngressInput{
		GroupId: awsapi.String(sgID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: awsapi.String("tcp"),
			FromPort:   awsapi.Int32(443),
			ToPort:     awsapi.Int32(443),
			IpRanges:   []ec2types.IpRange{{CidrIp: awsapi.String("10.0.0.0/8")}},
		}},
	}); err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress: %v", err)
	}

	// DescribeSecurityGroups — verify rule.
	dsg, err := frontendClient.DescribeSecurityGroups(ctx, &awsec2sdk.DescribeSecurityGroupsInput{
		GroupIds: []string{sgID},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups: %v", err)
	}
	if len(dsg.SecurityGroups) != 1 {
		t.Fatalf("DescribeSecurityGroups count = %d, want 1", len(dsg.SecurityGroups))
	}

	// DeleteSecurityGroup.
	if _, err := frontendClient.DeleteSecurityGroup(ctx, &awsec2sdk.DeleteSecurityGroupInput{
		GroupId: awsapi.String(sgID),
	}); err != nil {
		t.Fatalf("DeleteSecurityGroup: %v", err)
	}
}

// TestSockerless_EC2_Instances_ThroughShim drives:
//
//	AWS SDK → shim's EC2 frontend → AWS EC2 backend → sockerless EC2 sim.
//
// Instance lifecycle: VPC + subnet prereqs, RunInstances, poll until
// running, TerminateInstances. Requires Firecracker + KVM on the host
// (sockerless #373/#374/#375 now closed; lane unblocked as of PR #372).
func TestSockerless_EC2_Instances_ThroughShim(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}

	backendClient := newSockerlessAWSEC2Client(t, endpoint)
	backend := awsbackend.New(backendClient)
	shim := harness.StartComputeServerAWS(t, backend)
	frontendClient := newEC2Client(t, shim.URL)
	ctx := context.Background()

	// Create VPC + subnet as prerequisites for RunInstances.
	vpc, err := frontendClient.CreateVpc(ctx, &awsec2sdk.CreateVpcInput{
		CidrBlock: awsapi.String("10.10.0.0/16"),
	})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := awsapi.ToString(vpc.Vpc.VpcId)
	t.Cleanup(func() {
		frontendClient.DeleteVpc(ctx, &awsec2sdk.DeleteVpcInput{VpcId: awsapi.String(vpcID)}) //nolint:errcheck
	})

	subnet, err := frontendClient.CreateSubnet(ctx, &awsec2sdk.CreateSubnetInput{
		VpcId:            awsapi.String(vpcID),
		CidrBlock:        awsapi.String("10.10.1.0/24"),
		AvailabilityZone: awsapi.String("us-east-1a"),
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}
	subnetID := awsapi.ToString(subnet.Subnet.SubnetId)
	t.Cleanup(func() {
		frontendClient.DeleteSubnet(ctx, &awsec2sdk.DeleteSubnetInput{SubnetId: awsapi.String(subnetID)}) //nolint:errcheck
	})

	// RunInstances — triggers a real Firecracker VM boot inside sockerless.
	run, err := frontendClient.RunInstances(ctx, &awsec2sdk.RunInstancesInput{
		ImageId:      awsapi.String("ami-simulated"),
		InstanceType: ec2types.InstanceTypeT3Micro,
		MinCount:     awsapi.Int32(1),
		MaxCount:     awsapi.Int32(1),
		SubnetId:     awsapi.String(subnetID),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	if len(run.Instances) != 1 {
		t.Fatalf("RunInstances: expected 1 instance, got %d", len(run.Instances))
	}
	instanceID := awsapi.ToString(run.Instances[0].InstanceId)
	t.Cleanup(func() {
		frontendClient.TerminateInstances(ctx, &awsec2sdk.TerminateInstancesInput{ //nolint:errcheck
			InstanceIds: []string{instanceID},
		})
	})

	// Poll DescribeInstances until running (Firecracker boot takes ~10–30s).
	t.Logf("waiting for instance %s to reach running state", instanceID)
	running := false
	for range 60 { // up to 60 × 2s = 2 minutes
		desc, err := frontendClient.DescribeInstances(ctx, &awsec2sdk.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err != nil {
			t.Fatalf("DescribeInstances: %v", err)
		}
		if len(desc.Reservations) > 0 && len(desc.Reservations[0].Instances) > 0 {
			state := desc.Reservations[0].Instances[0].State.Name
			if state == ec2types.InstanceStateNameRunning {
				running = true
				break
			}
			if state == ec2types.InstanceStateNameTerminated || state == ec2types.InstanceStateNameStopped {
				t.Fatalf("instance reached unexpected terminal state %q before running", state)
			}
		}
		waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		<-waitCtx.Done()
		cancel()
	}
	if !running {
		t.Fatalf("instance %s did not reach running state within 2 minutes", instanceID)
	}

	// TerminateInstances.
	term, err := frontendClient.TerminateInstances(ctx, &awsec2sdk.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}
	if len(term.TerminatingInstances) == 0 {
		t.Fatal("TerminateInstances: empty state list")
	}
	finalState := term.TerminatingInstances[0].CurrentState.Name
	if finalState != ec2types.InstanceStateNameTerminated && finalState != ec2types.InstanceStateNameShuttingDown {
		t.Errorf("TerminateInstances state = %v, want terminated or shutting-down", finalState)
	}
}

// TestSockerless_EBS_Through_Shim drives:
//
//	AWS SDK → shim's EC2 frontend → AWS EC2 backend → sockerless EC2 sim.
//
// Block storage lifecycle: CreateVolume, DescribeVolumes, CreateSnapshot,
// DescribeSnapshots, DeleteSnapshot, DeleteVolume — all pure metadata in
// sockerless (no Firecracker required).
func TestSockerless_EBS_Through_Shim(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}

	backendClient := newSockerlessAWSEC2Client(t, endpoint)
	backend := awsbackend.New(backendClient)
	shim := harness.StartComputeServerAWS(t, backend)
	frontendClient := newEC2Client(t, shim.URL)
	ctx := context.Background()

	// CreateVolume through shim → sockerless.
	vol, err := frontendClient.CreateVolume(ctx, &awsec2sdk.CreateVolumeInput{
		AvailabilityZone: awsapi.String("us-east-1a"),
		Size:             awsapi.Int32(10),
		VolumeType:       ec2types.VolumeTypeGp3,
	})
	if err != nil {
		t.Fatalf("CreateVolume (through shim → sockerless): %v", err)
	}
	volID := awsapi.ToString(vol.VolumeId)
	if volID == "" {
		t.Fatal("CreateVolume returned empty VolumeId")
	}
	t.Cleanup(func() {
		frontendClient.DeleteVolume(ctx, &awsec2sdk.DeleteVolumeInput{VolumeId: awsapi.String(volID)}) //nolint:errcheck
	})

	// DescribeVolumes — verify presence.
	desc, err := frontendClient.DescribeVolumes(ctx, &awsec2sdk.DescribeVolumesInput{
		VolumeIds: []string{volID},
	})
	if err != nil {
		t.Fatalf("DescribeVolumes: %v", err)
	}
	if len(desc.Volumes) != 1 || awsapi.ToString(desc.Volumes[0].VolumeId) != volID {
		t.Fatalf("DescribeVolumes: got %d volumes, want 1 with id %q", len(desc.Volumes), volID)
	}

	// CreateSnapshot.
	snap, err := frontendClient.CreateSnapshot(ctx, &awsec2sdk.CreateSnapshotInput{
		VolumeId:    awsapi.String(volID),
		Description: awsapi.String("sockerless ebs conformance"),
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	snapID := awsapi.ToString(snap.SnapshotId)
	t.Cleanup(func() {
		frontendClient.DeleteSnapshot(ctx, &awsec2sdk.DeleteSnapshotInput{SnapshotId: awsapi.String(snapID)}) //nolint:errcheck
	})

	// DescribeSnapshots — verify presence.
	dsnap, err := frontendClient.DescribeSnapshots(ctx, &awsec2sdk.DescribeSnapshotsInput{
		SnapshotIds: []string{snapID},
	})
	if err != nil {
		t.Fatalf("DescribeSnapshots: %v", err)
	}
	if len(dsnap.Snapshots) != 1 || awsapi.ToString(dsnap.Snapshots[0].SnapshotId) != snapID {
		t.Fatalf("DescribeSnapshots: got %d, want 1 with id %q", len(dsnap.Snapshots), snapID)
	}

	// DeleteSnapshot.
	if _, err := frontendClient.DeleteSnapshot(ctx, &awsec2sdk.DeleteSnapshotInput{
		SnapshotId: awsapi.String(snapID),
	}); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}

	// DeleteVolume.
	if _, err := frontendClient.DeleteVolume(ctx, &awsec2sdk.DeleteVolumeInput{
		VolumeId: awsapi.String(volID),
	}); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
}

// TestSockerless_AzureCompute_Through_Shim_Terraform_Apply exercises the
// shim's azure_compute frontend end-to-end with the `hashicorp/azurerm`
// Terraform provider.
//
// Closes BUG-56. The shim handles Microsoft.Compute paths locally (inmem
// backend). Resource groups, Microsoft.Network resources (VNet, Subnet,
// NIC), and Entra ID token requests are forwarded to sockerless via the
// passthrough proxy — the same split used by the DNS TF test.
//
// Requires: SOCKERLESS_AZURE_TLS_PORT + SOCKERLESS_AZURE_TLS_CERT, az CLI
// not required. Linux-only (SSL_CERT_FILE).
// azureSockerlessTFSession sets up the shim+sockerless azurerm Terraform
// session and returns the working dir, a runTf runner, and the azurerm
// provider substitution values (metadata_host, subscription, tenant,
// client). All teardown is registered on t.Cleanup; the test is skipped
// if any prerequisite is missing.
func azureSockerlessTFSession(t *testing.T) (dir string, runTf func(args ...string), metaHost, sub, tenant, client string) {
	t.Helper()
	azureTLSPort := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if azureTLSPort == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	sockCertPath := os.Getenv("SOCKERLESS_AZURE_TLS_CERT")
	if sockCertPath == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set")
	}
	sockCertPEM, err := os.ReadFile(sockCertPath)
	if err != nil {
		t.Fatalf("read sockerless cert: %v", err)
	}
	systemCA := findSystemCABundleForCompute()
	if systemCA == "" {
		t.Skip("no system CA bundle — SSL_CERT_FILE workaround requires Linux")
	}
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}

	sub = "00000000-0000-0000-0000-000000000001"
	tenant = "00000000-0000-0000-0000-000000000000"
	client = "00000000-0000-0000-0000-000000000000"

	sockerlessARM, err := url.Parse("https://localhost:" + azureTLSPort)
	if err != nil {
		t.Fatalf("parse sockerless URL: %v", err)
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(sockCertPEM) {
		t.Fatalf("append sockerless cert to pool")
	}
	proxy := httputil.NewSingleHostReverseProxy(sockerlessARM)
	proxy.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: rootCAs}} //nolint:gosec

	jwks := fetchSockerlessComputeJWKS(t, azureTLSPort, tenant, sockCertPEM)
	shim := harness.StartComputeServerAzureVMWithConfig(t, inmem.New(), azurecomputefront.Config{
		Passthrough:      proxy,
		MetadataLoginURL: sockerlessARM.String(),
		BearerOptions: azurebearer.Options{
			Issuer: fmt.Sprintf("https://sts.windows.net/%s/", tenant),
			JWKS:   jwks,
		},
	})

	dir = t.TempDir()
	systemBytes, err := os.ReadFile(systemCA)
	if err != nil {
		t.Fatalf("read system CA: %v", err)
	}
	combined := append(append([]byte{}, systemBytes...), '\n')
	combined = append(combined, sockCertPEM...)
	combined = append(combined, '\n')
	combined = append(combined, shim.CertPEM...)
	combinedPath := filepath.Join(dir, "combined-ca.pem")
	if err := os.WriteFile(combinedPath, combined, 0o644); err != nil {
		t.Fatalf("write combined CA: %v", err)
	}

	cacheDir := filepath.Join(dir, ".terraform-plugin-cache")
	_ = os.MkdirAll(cacheDir, 0o755)
	runTf = func(args ...string) {
		t.Helper()
		cmd := exec.Command(tfBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1",
			"TF_PLUGIN_CACHE_DIR="+cacheDir,
			"SSL_CERT_FILE="+combinedPath,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout.String(), stderr.String(), err)
		}
	}
	return dir, runTf, computeShimHost(shim.URL), sub, tenant, client
}

func TestSockerless_AzureCompute_Through_Shim_Terraform_Apply(t *testing.T) {
	dir, runTf, metaHost, sub, tenant, client := azureSockerlessTFSession(t)

	hcl := fmt.Sprintf(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.0" }
  }
}

provider "azurerm" {
  features {}
  metadata_host                   = %q
  subscription_id                 = %q
  tenant_id                       = %q
  client_id                       = %q
  client_secret                   = "shim-test"
  use_oidc                        = false
  use_cli                         = false
  resource_provider_registrations = "none"
}

resource "azurerm_resource_group" "shim" {
  name     = "shim-compute-tf-rg"
  location = "East US"
}

resource "azurerm_virtual_network" "shim" {
  name                = "shim-vnet"
  location            = azurerm_resource_group.shim.location
  resource_group_name = azurerm_resource_group.shim.name
  address_space       = ["10.0.0.0/16"]
}

resource "azurerm_subnet" "shim" {
  name                 = "shim-subnet"
  resource_group_name  = azurerm_resource_group.shim.name
  virtual_network_name = azurerm_virtual_network.shim.name
  address_prefixes     = ["10.0.1.0/24"]
}

resource "azurerm_network_interface" "shim" {
  name                = "shim-nic"
  location            = azurerm_resource_group.shim.location
  resource_group_name = azurerm_resource_group.shim.name

  ip_configuration {
    name                          = "internal"
    subnet_id                     = azurerm_subnet.shim.id
    private_ip_address_allocation = "Dynamic"
  }
}

resource "azurerm_linux_virtual_machine" "shim" {
  name                            = "shim-test-vm"
  resource_group_name             = azurerm_resource_group.shim.name
  location                        = azurerm_resource_group.shim.location
  size                            = "Standard_B1s"
  admin_username                  = "adminuser"
  admin_password                  = "Sh1mT3st!"
  disable_password_authentication = false

  network_interface_ids = [azurerm_network_interface.shim.id]

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "Standard_LRS"
  }

  source_image_reference {
    publisher = "Canonical"
    offer     = "UbuntuServer"
    sku       = "18.04-LTS"
    version   = "latest"
  }
}
`, metaHost, sub, tenant, client)

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	runTf("init", "-no-color")
	runTf("apply", "-auto-approve", "-no-color")
	runTf("destroy", "-auto-approve", "-no-color")
}

// TestSockerless_AzureDisk_Through_Shim_Terraform_Apply exercises the
// azurerm_managed_disk + azurerm_snapshot resources through the shim's
// azure_compute frontend (Microsoft.Compute paths) with resource groups
// forwarded to sockerless. Closes the Azure Terraform row of Phase 17.
func TestSockerless_AzureDisk_Through_Shim_Terraform_Apply(t *testing.T) {
	dir, runTf, metaHost, sub, tenant, client := azureSockerlessTFSession(t)

	hcl := fmt.Sprintf(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.0" }
  }
}

provider "azurerm" {
  features {}
  metadata_host                   = %q
  subscription_id                 = %q
  tenant_id                       = %q
  client_id                       = %q
  client_secret                   = "shim-test"
  use_oidc                        = false
  use_cli                         = false
  resource_provider_registrations = "none"
}

resource "azurerm_resource_group" "shim" {
  name     = "shim-disk-tf-rg"
  location = "East US"
}

resource "azurerm_managed_disk" "shim" {
  name                 = "shim-tf-disk"
  location             = azurerm_resource_group.shim.location
  resource_group_name  = azurerm_resource_group.shim.name
  storage_account_type = "Standard_LRS"
  create_option        = "Empty"
  disk_size_gb         = 16
}
`, metaHost, sub, tenant, client)

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	runTf("init", "-no-color")
	runTf("apply", "-auto-approve", "-no-color")
	runTf("destroy", "-auto-approve", "-no-color")
}

// computeShimHost extracts host:port from a URL — azurerm metadata_host
// expects this shape (no scheme).
func computeShimHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host
}
