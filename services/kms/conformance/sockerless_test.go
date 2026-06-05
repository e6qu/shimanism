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
	"os"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskmssdk "github.com/aws/aws-sdk-go-v2/service/kms"

	"github.com/e6qu/shimanism/internal/harness"
	awskmsbackend "github.com/e6qu/shimanism/services/kms/backends/aws"
)

func TestSockerless_AWSKMS_Through_Shim(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}

	// Backend leg: shim's AWS KMS backend → sockerless KMS sim.
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{AccessKeyID: "test", SecretAccessKey: "test"},
		}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	backendClient := awskmssdk.NewFromConfig(cfg, func(o *awskmssdk.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
	backend := awskmsbackend.New(backendClient)
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

// TestSockerless_GCPKMS_Through_Shim is gated on sockerless adding a
// Cloud KMS simulator. Sockerless has no GCP Cloud KMS surface (probed
// 2026-06-04, re-confirmed 2026-06-05); filed upstream as
// e6qu/sockerless#419. Un-skip once it lands.
func TestSockerless_GCPKMS_Through_Shim(t *testing.T) {
	t.Skip("sockerless has no GCP Cloud KMS simulator (e6qu/sockerless#419); un-skip when it lands")
}

// TestSockerless_AzureKVKeys_Through_Shim exercises the Azure Key Vault
// keys frontend → Azure backend → sockerless KV sim. Gated on the
// SOCKERLESS_AZURE_TLS_PORT through-shim wiring (same plumbing as the
// secrets Azure sockerless lane); added in a follow-on.
func TestSockerless_AzureKVKeys_Through_Shim(t *testing.T) {
	if os.Getenv("SOCKERLESS_AZURE_TLS_PORT") == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	t.Skip("Azure KV keys through-shim sockerless wiring is a 19.D follow-on")
}
