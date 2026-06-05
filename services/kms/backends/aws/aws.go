// Package aws is the AWS KMS passthrough backend for shimanism's
// key-management service (Phase 19). It uses aws-sdk-go-v2/service/kms to
// drive real AWS KMS (or a sockerless-pointed client for tests).
//
// Key material never leaves AWS — Encrypt/Decrypt forward to KMS and the
// ciphertext blob is AWS's own structured blob (it carries the key
// reference, so Decrypt needs no key ID). Stateless: every Describe
// re-reads KMS.
package aws

import (
	"context"
	"fmt"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"

	"github.com/e6qu/shimanism/internal/kms/domain"
)

// Backend implements domain.KMS via real AWS KMS.
type Backend struct {
	c *kms.Client
}

// New wraps an already-configured KMS client.
func New(c *kms.Client) *Backend { return &Backend{c: c} }

var _ domain.KMS = (*Backend)(nil)

// CreateKeyRing / GetKeyRing: AWS KMS has no keyRing container (its
// keyspace is flat, with aliases the only grouping). The container is out
// of intersection for the AWS backend, so these return the source cloud's
// not-supported error rather than fabricating a ring.
func (b *Backend) CreateKeyRing(_ context.Context, _ string) (domain.KeyRing, error) {
	return domain.KeyRing{}, domain.ErrNotSupported
}

func (b *Backend) GetKeyRing(_ context.Context, _ string) (domain.KeyRing, error) {
	return domain.KeyRing{}, domain.ErrNotSupported
}

func (b *Backend) CreateKey(ctx context.Context, opt domain.CreateKeyOptions) (domain.Key, error) {
	in := &kms.CreateKeyInput{}
	if opt.Description != "" {
		in.Description = awsapi.String(opt.Description)
	}
	for k, v := range opt.Tags {
		in.Tags = append(in.Tags, kmstypes.Tag{TagKey: awsapi.String(k), TagValue: awsapi.String(v)})
	}
	out, err := b.c.CreateKey(ctx, in)
	if err != nil {
		return domain.Key{}, fmt.Errorf("CreateKey: %w", err)
	}
	return awsKeyToDomain(out.KeyMetadata), nil
}

func (b *Backend) DescribeKey(ctx context.Context, id string) (domain.Key, error) {
	out, err := b.c.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: awsapi.String(id)})
	if err != nil {
		return domain.Key{}, fmt.Errorf("DescribeKey: %w", err)
	}
	return awsKeyToDomain(out.KeyMetadata), nil
}

func (b *Backend) ListKeys(ctx context.Context) (domain.ListKeysResult, error) {
	out, err := b.c.ListKeys(ctx, &kms.ListKeysInput{})
	if err != nil {
		return domain.ListKeysResult{}, fmt.Errorf("ListKeys: %w", err)
	}
	var keys []domain.Key
	for _, k := range out.Keys {
		keys = append(keys, domain.Key{ID: awsapi.ToString(k.KeyId), State: domain.KeyStateEnabled})
	}
	return domain.ListKeysResult{Keys: keys}, nil
}

func (b *Backend) Encrypt(ctx context.Context, keyID string, plaintext []byte) (domain.EncryptResult, error) {
	out, err := b.c.Encrypt(ctx, &kms.EncryptInput{KeyId: awsapi.String(keyID), Plaintext: plaintext})
	if err != nil {
		return domain.EncryptResult{}, fmt.Errorf("Encrypt: %w", err)
	}
	return domain.EncryptResult{KeyID: awsapi.ToString(out.KeyId), Ciphertext: out.CiphertextBlob}, nil
}

func (b *Backend) Decrypt(ctx context.Context, keyID string, ciphertext []byte) (domain.DecryptResult, error) {
	in := &kms.DecryptInput{CiphertextBlob: ciphertext}
	if keyID != "" {
		in.KeyId = awsapi.String(keyID)
	}
	out, err := b.c.Decrypt(ctx, in)
	if err != nil {
		return domain.DecryptResult{}, fmt.Errorf("Decrypt: %w", err)
	}
	return domain.DecryptResult{KeyID: awsapi.ToString(out.KeyId), Plaintext: out.Plaintext}, nil
}

func (b *Backend) ScheduleKeyDeletion(ctx context.Context, keyID string, pendingWindowDays int) (domain.Key, error) {
	in := &kms.ScheduleKeyDeletionInput{KeyId: awsapi.String(keyID)}
	if pendingWindowDays > 0 {
		in.PendingWindowInDays = awsapi.Int32(int32(pendingWindowDays))
	}
	out, err := b.c.ScheduleKeyDeletion(ctx, in)
	if err != nil {
		return domain.Key{}, fmt.Errorf("ScheduleKeyDeletion: %w", err)
	}
	k := domain.Key{ID: keyID, State: domain.KeyStatePendingDeletion}
	if out.DeletionDate != nil {
		k.DeletionDate = *out.DeletionDate
	}
	return k, nil
}

func (b *Backend) CancelKeyDeletion(ctx context.Context, keyID string) (domain.Key, error) {
	if _, err := b.c.CancelKeyDeletion(ctx, &kms.CancelKeyDeletionInput{KeyId: awsapi.String(keyID)}); err != nil {
		return domain.Key{}, fmt.Errorf("CancelKeyDeletion: %w", err)
	}
	return domain.Key{ID: keyID, State: domain.KeyStateDisabled}, nil
}

func (b *Backend) EnableKeyRotation(ctx context.Context, keyID string) error {
	_, err := b.c.EnableKeyRotation(ctx, &kms.EnableKeyRotationInput{KeyId: awsapi.String(keyID)})
	if err != nil {
		return fmt.Errorf("EnableKeyRotation: %w", err)
	}
	return nil
}

func (b *Backend) DisableKeyRotation(ctx context.Context, keyID string) error {
	_, err := b.c.DisableKeyRotation(ctx, &kms.DisableKeyRotationInput{KeyId: awsapi.String(keyID)})
	if err != nil {
		return fmt.Errorf("DisableKeyRotation: %w", err)
	}
	return nil
}

func (b *Backend) GetKeyRotationStatus(ctx context.Context, keyID string) (bool, error) {
	out, err := b.c.GetKeyRotationStatus(ctx, &kms.GetKeyRotationStatusInput{KeyId: awsapi.String(keyID)})
	if err != nil {
		return false, fmt.Errorf("GetKeyRotationStatus: %w", err)
	}
	return out.KeyRotationEnabled, nil
}

func awsKeyToDomain(md *kmstypes.KeyMetadata) domain.Key {
	if md == nil {
		return domain.Key{}
	}
	k := domain.Key{
		ID:          awsapi.ToString(md.KeyId),
		Description: awsapi.ToString(md.Description),
		Usage:       domain.KeyUsage(md.KeyUsage),
		State:       domain.KeyState(md.KeyState),
	}
	if md.KeySpec != "" {
		k.KeySpec = string(md.KeySpec)
	}
	if md.CreationDate != nil {
		k.CreatedAt = *md.CreationDate
	}
	if md.DeletionDate != nil {
		k.DeletionDate = *md.DeletionDate
	}
	return k
}
