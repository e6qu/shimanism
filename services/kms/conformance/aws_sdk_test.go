// Conformance: AWS KMS-shaped frontend exercised by the official
// aws-sdk-go-v2/service/kms SDK. Covers Phase 19: CreateKey, DescribeKey,
// ListKeys, Encrypt, Decrypt, ScheduleKeyDeletion, CancelKeyDeletion,
// rotation enable/disable/status.
package conformance_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/kms/backends/inmem"
)

func newAWSKMSClient(t *testing.T, endpoint string) *kms.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	return kms.NewFromConfig(cfg, func(o *kms.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

func TestAWSSDK_KMS_KeyLifecycle(t *testing.T) {
	srv := harness.StartKMSServerAWS(t, inmem.New())
	cli := newAWSKMSClient(t, srv.URL)
	ctx := context.Background()

	// CreateKey
	create, err := cli.CreateKey(ctx, &kms.CreateKeyInput{
		Description: aws.String("phase-19 test key"),
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if create.KeyMetadata == nil || aws.ToString(create.KeyMetadata.KeyId) == "" {
		t.Fatal("CreateKey returned no KeyId")
	}
	keyID := aws.ToString(create.KeyMetadata.KeyId)
	if create.KeyMetadata.KeyState != kmstypes.KeyStateEnabled {
		t.Errorf("CreateKey state = %v, want Enabled", create.KeyMetadata.KeyState)
	}

	// DescribeKey
	desc, err := cli.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("DescribeKey: %v", err)
	}
	if aws.ToString(desc.KeyMetadata.KeyId) != keyID {
		t.Errorf("DescribeKey KeyId = %q, want %q", aws.ToString(desc.KeyMetadata.KeyId), keyID)
	}
	if aws.ToString(desc.KeyMetadata.Description) != "phase-19 test key" {
		t.Errorf("DescribeKey Description = %q", aws.ToString(desc.KeyMetadata.Description))
	}

	// ListKeys
	list, err := cli.ListKeys(ctx, &kms.ListKeysInput{})
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	found := false
	for _, k := range list.Keys {
		if aws.ToString(k.KeyId) == keyID {
			found = true
		}
	}
	if !found {
		t.Errorf("ListKeys does not contain %q", keyID)
	}

	// ScheduleKeyDeletion
	sched, err := cli.ScheduleKeyDeletion(ctx, &kms.ScheduleKeyDeletionInput{
		KeyId:               aws.String(keyID),
		PendingWindowInDays: aws.Int32(7),
	})
	if err != nil {
		t.Fatalf("ScheduleKeyDeletion: %v", err)
	}
	if sched.KeyState != kmstypes.KeyStatePendingDeletion {
		t.Errorf("ScheduleKeyDeletion state = %v, want PendingDeletion", sched.KeyState)
	}
	if sched.DeletionDate == nil {
		t.Error("ScheduleKeyDeletion: nil DeletionDate")
	}

	// CancelKeyDeletion
	if _, err := cli.CancelKeyDeletion(ctx, &kms.CancelKeyDeletionInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatalf("CancelKeyDeletion: %v", err)
	}
}

func TestAWSSDK_KMS_EncryptDecrypt(t *testing.T) {
	srv := harness.StartKMSServerAWS(t, inmem.New())
	cli := newAWSKMSClient(t, srv.URL)
	ctx := context.Background()

	create, err := cli.CreateKey(ctx, &kms.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	keyID := aws.ToString(create.KeyMetadata.KeyId)

	plaintext := []byte("attack at dawn")

	// Encrypt
	enc, err := cli.Encrypt(ctx, &kms.EncryptInput{
		KeyId:     aws.String(keyID),
		Plaintext: plaintext,
	})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(enc.CiphertextBlob) == 0 {
		t.Fatal("Encrypt returned empty CiphertextBlob")
	}
	if bytes.Equal(enc.CiphertextBlob, plaintext) {
		t.Error("ciphertext equals plaintext — not encrypted")
	}

	// Decrypt (no KeyId — recovered from the ciphertext blob, like real KMS)
	dec, err := cli.Decrypt(ctx, &kms.DecryptInput{CiphertextBlob: enc.CiphertextBlob})
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(dec.Plaintext, plaintext) {
		t.Errorf("Decrypt round-trip mismatch: got %q, want %q", dec.Plaintext, plaintext)
	}
}

func TestAWSSDK_KMS_Rotation(t *testing.T) {
	srv := harness.StartKMSServerAWS(t, inmem.New())
	cli := newAWSKMSClient(t, srv.URL)
	ctx := context.Background()

	create, err := cli.CreateKey(ctx, &kms.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	keyID := aws.ToString(create.KeyMetadata.KeyId)

	// Initially disabled.
	st, err := cli.GetKeyRotationStatus(ctx, &kms.GetKeyRotationStatusInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("GetKeyRotationStatus: %v", err)
	}
	if st.KeyRotationEnabled {
		t.Error("rotation should be disabled initially")
	}

	// Enable.
	if _, err := cli.EnableKeyRotation(ctx, &kms.EnableKeyRotationInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatalf("EnableKeyRotation: %v", err)
	}
	st2, _ := cli.GetKeyRotationStatus(ctx, &kms.GetKeyRotationStatusInput{KeyId: aws.String(keyID)})
	if !st2.KeyRotationEnabled {
		t.Error("rotation should be enabled after EnableKeyRotation")
	}

	// Disable.
	if _, err := cli.DisableKeyRotation(ctx, &kms.DisableKeyRotationInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatalf("DisableKeyRotation: %v", err)
	}
	st3, _ := cli.GetKeyRotationStatus(ctx, &kms.GetKeyRotationStatusInput{KeyId: aws.String(keyID)})
	if st3.KeyRotationEnabled {
		t.Error("rotation should be disabled after DisableKeyRotation")
	}
}
