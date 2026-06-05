// Sockerless lane for the KMS service, Phase 19.C.
//
// Through-shim path (AWS):
//
//	AWS SDK KMS → shim's AWS KMS frontend → AWS KMS backend
//	    → sockerless's KMS sim.
//
// Sockerless coverage (probed PR #128 era): AWS KMS present
// (CreateKey/DescribeKey/ListKeys/Encrypt/Decrypt/ScheduleKeyDeletion/
// GetKeyRotationStatus/...). Azure Key Vault keys present under the KV
// sim. GCP Cloud KMS absent — filed upstream; the GCP lane skips with a
// message referencing the gap.
//
// Set SOCKERLESS_AWS_ENDPOINT to opt in to the AWS lane.
package conformance_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
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
	awskmssdk "github.com/aws/aws-sdk-go-v2/service/kms"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"golang.org/x/oauth2"
	kmsraw "google.golang.org/api/cloudkms/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/harness"
	awskmsbackend "github.com/e6qu/shimanism/services/kms/backends/aws"
	azurekmsbackend "github.com/e6qu/shimanism/services/kms/backends/azure"
	gcpkmsbackend "github.com/e6qu/shimanism/services/kms/backends/gcp"
)

// sockerlessAWSKMSBackend wires the shim's AWS KMS backend to
// sockerless's KMS sim. Skips if SOCKERLESS_AWS_ENDPOINT is unset.
func sockerlessAWSKMSBackend(t *testing.T) *awskmsbackend.Backend {
	t.Helper()
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{AccessKeyID: "test", SecretAccessKey: "test"},
		}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	// sockerless serves over a self-signed cert; trust it for the test
	// backend leg (same switch the other AWS sockerless lanes use).
	if os.Getenv("AWS_S3_CONFORMANCE_INSECURE_TLS") == "1" {
		cfg.HTTPClient = awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
		})
	}
	backendClient := awskmssdk.NewFromConfig(cfg, func(o *awskmssdk.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
	return awskmsbackend.New(backendClient)
}

func TestSockerless_AWSKMS_Through_Shim(t *testing.T) {
	// Backend leg: shim's AWS KMS backend → sockerless KMS sim.
	backend := sockerlessAWSKMSBackend(t)
	shim := harness.StartKMSServerAWS(t, backend)

	// Frontend leg: official KMS SDK → shim.
	cli := newAWSKMSClient(t, shim.URL)
	ctx := context.Background()

	// CreateKey through shim → sockerless.
	create, err := cli.CreateKey(ctx, &awskmssdk.CreateKeyInput{
		Description: awsapi.String("sockerless kms conformance"),
	})
	if err != nil {
		t.Fatalf("CreateKey (through shim → sockerless): %v", err)
	}
	keyID := awsapi.ToString(create.KeyMetadata.KeyId)
	if keyID == "" {
		t.Fatal("CreateKey returned empty KeyId")
	}

	// DescribeKey.
	if _, err := cli.DescribeKey(ctx, &awskmssdk.DescribeKeyInput{KeyId: awsapi.String(keyID)}); err != nil {
		t.Fatalf("DescribeKey: %v", err)
	}

	// Encrypt / Decrypt round-trip.
	plaintext := []byte("sockerless-secret")
	enc, err := cli.Encrypt(ctx, &awskmssdk.EncryptInput{
		KeyId:     awsapi.String(keyID),
		Plaintext: plaintext,
	})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	dec, err := cli.Decrypt(ctx, &awskmssdk.DecryptInput{
		CiphertextBlob: enc.CiphertextBlob,
		KeyId:          awsapi.String(keyID),
	})
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(dec.Plaintext, plaintext) {
		t.Errorf("Decrypt round-trip = %q, want %q", dec.Plaintext, plaintext)
	}

	// ScheduleKeyDeletion (cleanup).
	if _, err := cli.ScheduleKeyDeletion(ctx, &awskmssdk.ScheduleKeyDeletionInput{
		KeyId:               awsapi.String(keyID),
		PendingWindowInDays: awsapi.Int32(7),
	}); err != nil {
		t.Fatalf("ScheduleKeyDeletion: %v", err)
	}
}

// terraformAWSKMSTaggedConfig drives a tagged aws_kms_key through the
// shim's AWS KMS frontend. Tags exercise the CreateKey tag round-trip +
// ListResourceTags read path end-to-end into sockerless (a tagged key
// previously hung the provider 10m until sockerless#413 / PR #415 fixed
// KMS tagging).
//
// %s = shim AWS KMS frontend URL
const terraformAWSKMSTaggedConfig = `
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "AKIAIOSFODNN7EXAMPLE"
  secret_key                  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    kms = "%s"
  }
}

resource "aws_kms_key" "tagged" {
  description             = "shim-sockerless-tagged-kms-key"
  deletion_window_in_days = 7
  tags = {
    Environment = "shim-conformance"
    Team        = "kms"
  }
}
`

// TestSockerless_AWSKMS_Through_Shim_TerraformTaggedKey drives the
// hashicorp/aws Terraform provider through the shim's AWS KMS frontend
// → AWS KMS backend → sockerless. A tagged aws_kms_key exercises the
// tag CreateKey + ListResourceTags round-trip into sockerless; a clean
// refresh-plan after apply proves the tags round-trip (no perpetual
// diff / provider hang).
func TestSockerless_AWSKMS_Through_Shim_TerraformTaggedKey(t *testing.T) {
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	backend := sockerlessAWSKMSBackend(t)
	shim := harness.StartKMSServerAWS(t, backend)

	dir := t.TempDir()
	cfg := fmt.Sprintf(terraformAWSKMSTaggedConfig, shim.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	cacheDir := filepath.Join(dir, ".terraform-plugin-cache")
	_ = os.MkdirAll(cacheDir, 0o755)

	run := func(args ...string) ([]byte, []byte, error) {
		// Bound each invocation so a backend stall can't consume the
		// whole package timeout and take the sibling KMS lanes down with
		// it. A healthy apply finishes in seconds.
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, tfBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1",
			"TF_PLUGIN_CACHE_DIR="+cacheDir,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}

	if stdout, stderr, err := run("init", "-no-color"); err != nil {
		t.Fatalf("terraform init\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	stdout, stderr, err := run("apply", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform apply\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	if !strings.Contains(string(stdout), "Apply complete!") {
		t.Errorf("terraform apply: missing 'Apply complete!':\n%s", stdout)
	}
	t.Cleanup(func() { _, _, _ = run("destroy", "-auto-approve", "-no-color") })

	// A refresh-plan with no changes proves the tags round-tripped (the
	// tagging bug manifested as a never-settling diff / 10-minute hang).
	_, stderr, err = run("plan", "-detailed-exitcode", "-no-color")
	if err != nil {
		t.Fatalf("terraform plan after apply expected no diff (tags round-trip), got err: %v\nstderr: %s", err, stderr)
	}
}

// TestSockerless_GCPKMS_Through_Shim exercises the full through-shim path
// against sockerless's GCP Cloud KMS simulator (added in
// e6qu/sockerless#422):
//
//	cloudkms/v1 SDK → shim's GCP Cloud KMS frontend → shim's GCP Cloud KMS
//	    backend → sockerless's Cloud KMS sim.
//
// sockerless does real per-version AES-256-GCM, so the encrypt/decrypt
// round-trip is end-to-end real. The backend addresses keys within a
// fixed keyRing (Config.KeyRing), so the test creates and uses that same
// ring. Set SOCKERLESS_GCP_ENDPOINT (e.g. localhost:14567) to opt in.
func TestSockerless_GCPKMS_Through_Shim(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	ring := "shim-sk-ring-" + randomHex(4)

	// Backend leg: shim's GCP Cloud KMS backend → sockerless GCP sim.
	svc, err := kmsraw.NewService(context.Background(),
		option.WithEndpoint("http://"+endpoint+"/"),
		option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "sockerless-test"})),
	)
	if err != nil {
		t.Fatalf("cloudkms service: %v", err)
	}
	backend := gcpkmsbackend.New(svc, gcpkmsbackend.Config{
		Project: gcpProject, Location: gcpLocation, KeyRing: ring,
	})
	shim := harness.StartKMSServerGCP(t, backend)

	// Frontend leg: official cloudkms SDK → shim.
	cli := newGCPKMSClient(t, shim.URL)
	ctx := context.Background()
	parent := "projects/" + gcpProject + "/locations/" + gcpLocation
	ringPath := parent + "/keyRings/" + ring

	// keyRings.create → backend.CreateKeyRing → sockerless.
	if _, err := cli.Projects.Locations.KeyRings.Create(parent, &kmsraw.KeyRing{}).
		KeyRingId(ring).Context(ctx).Do(); err != nil {
		t.Fatalf("keyRings.create (through shim → sockerless): %v", err)
	}

	// cryptoKeys.create (symmetric ENCRYPT_DECRYPT).
	if _, err := cli.Projects.Locations.KeyRings.CryptoKeys.Create(ringPath, &kmsraw.CryptoKey{
		Purpose: "ENCRYPT_DECRYPT",
	}).CryptoKeyId("shim-key").Context(ctx).Do(); err != nil {
		t.Fatalf("cryptoKeys.create: %v", err)
	}
	keyPath := ringPath + "/cryptoKeys/shim-key"

	// cryptoKeys.get.
	if _, err := cli.Projects.Locations.KeyRings.CryptoKeys.Get(keyPath).Context(ctx).Do(); err != nil {
		t.Fatalf("cryptoKeys.get: %v", err)
	}

	// Encrypt / Decrypt round-trip (sockerless does real AES-256-GCM).
	plaintext := []byte("sockerless-gcp-kms-secret")
	enc, err := cli.Projects.Locations.KeyRings.CryptoKeys.Encrypt(keyPath, &kmsraw.EncryptRequest{
		Plaintext: base64.StdEncoding.EncodeToString(plaintext),
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	dec, err := cli.Projects.Locations.KeyRings.CryptoKeys.Decrypt(keyPath, &kmsraw.DecryptRequest{
		Ciphertext: enc.Ciphertext,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	got, err := base64.StdEncoding.DecodeString(dec.Plaintext)
	if err != nil {
		t.Fatalf("decode decrypt plaintext: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("decrypt round-trip = %q, want %q", got, plaintext)
	}
}

// TestSockerless_AzureKVKeys_Through_Shim exercises the Key Vault keys
// lifecycle through-shim against sockerless's simulator:
//
//	azkeys SDK → shim's Azure KV-keys frontend → shim's Azure KV-keys
//	    backend → sockerless's Key Vault keys sim.
//
// Create / get / delete are real end-to-end. The encrypt/decrypt
// round-trip is split into TestSockerless_AzureKVKeys_Crypto_Through_Shim
// (gated on e6qu/sockerless#423). Set SOCKERLESS_AZURE_KV_URL +
// SOCKERLESS_AZURE_TLS_PORT to opt in.
func TestSockerless_AzureKVKeys_Through_Shim(t *testing.T) {
	backend := sockerlessAzureKVKeysBackend(t)
	shim := harness.StartKMSServerAzure(t, backend)

	cli := newAzureKeysClient(t, shim.URL)
	ctx := context.Background()

	keyName := "shim-sockerless-kvkey-" + randomHex(4)

	// CreateKey through shim → sockerless (RSA).
	if _, err := cli.CreateKey(ctx, keyName, azkeys.CreateKeyParameters{
		Kty: to.Ptr(azkeys.KeyTypeRSA),
	}, nil); err != nil {
		t.Fatalf("CreateKey (through shim → sockerless): %v", err)
	}

	// GetKey.
	if _, err := cli.GetKey(ctx, keyName, "", nil); err != nil {
		t.Fatalf("GetKey: %v", err)
	}

	// DeleteKey (maps to ScheduleKeyDeletion / soft-delete).
	if _, err := cli.DeleteKey(ctx, keyName, nil); err != nil {
		t.Fatalf("DeleteKey: %v", err)
	}
}

// TestSockerless_AzureKVKeys_Crypto_Through_Shim would cover the RSA-OAEP
// encrypt/decrypt round-trip (azkeys SDK → shim Azure KV-keys frontend →
// Azure backend → sockerless, ciphertext opaque to the shim). Gated on
// e6qu/sockerless#423: the sim 405s version-less key crypto
// (`POST /keys/{name}/encrypt`), which real Key Vault and the azkeys SDK
// use to target a key's current version. The shim and SDK use the valid
// no-version form, so this is a sockerless fidelity gap, not a shim bug —
// un-skip (and restore the encrypt/decrypt body) once #423 lands.
func TestSockerless_AzureKVKeys_Crypto_Through_Shim(t *testing.T) {
	if os.Getenv("SOCKERLESS_AZURE_KV_URL") == "" || os.Getenv("SOCKERLESS_AZURE_TLS_PORT") == "" {
		t.Skip("SOCKERLESS_AZURE_KV_URL / SOCKERLESS_AZURE_TLS_PORT not set")
	}
	t.Skip("blocked on e6qu/sockerless#423: sim 405s version-less key crypto (POST /keys/{name}/encrypt); un-skip when it lands")
}

// sockerlessAzureKVKeysBackend wires the shim's Azure Key Vault keys
// backend to sockerless's Azure simulator. The dial transport pins every
// connection to the sim's localhost TLS port while preserving the vault
// Host header the sim dispatches on (same plumbing as the secrets Azure
// sockerless lane).
func sockerlessAzureKVKeysBackend(t *testing.T) *azurekmsbackend.Backend {
	t.Helper()
	vaultURL := os.Getenv("SOCKERLESS_AZURE_KV_URL")
	if vaultURL == "" {
		t.Skip("SOCKERLESS_AZURE_KV_URL not set")
	}
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	httpClient := &http.Client{Transport: kmsLocalhostDialTransport(port)}
	c, err := azkeys.NewClient(vaultURL, kvKeysCred{}, &azkeys.ClientOptions{
		DisableChallengeResourceVerification: true,
		ClientOptions:                        azcore.ClientOptions{Transport: httpClient},
	})
	if err != nil {
		t.Fatalf("azkeys client: %v", err)
	}
	return azurekmsbackend.New(c)
}

// randomHex returns n random bytes hex-encoded, for unique resource
// names across parallel runs.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// kmsLocalhostDialTransport routes every dial to 127.0.0.1:<port> (the
// sockerless TLS port) while leaving the request Host intact, so no DNS
// plumbing for *.vault.azure.net is needed. InsecureSkipVerify accepts
// the sim's self-signed cert.
func kmsLocalhostDialTransport(port string) *http.Transport {
	dialer := &net.Dialer{}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "127.0.0.1:"+port)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}
}
