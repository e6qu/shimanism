// Package gcp is the GCP Cloud KMS passthrough backend for shimanism's
// key-management service (Phase 19). It uses google.golang.org/api/
// cloudkms/v1 to drive real Cloud KMS.
//
// The flat domain key ID maps to a cryptoKey short name within a fixed
// project/location/keyRing (Config). Encrypt/Decrypt target the
// cryptoKey's primary version. Key material never leaves Cloud KMS.
//
// Note: sockerless has no Cloud KMS simulator (filed upstream), so this
// backend is exercised only against real GCP (Track A) — not the
// sockerless lane.
package gcp

import (
	"context"
	"encoding/base64"
	"fmt"

	kmsraw "google.golang.org/api/cloudkms/v1"

	"github.com/e6qu/shimanism/internal/kms/domain"
)

// Config locates keys within Cloud KMS.
type Config struct {
	Project  string
	Location string
	KeyRing  string
}

// Backend implements domain.KMS via real GCP Cloud KMS.
type Backend struct {
	svc *kmsraw.Service
	cfg Config
}

// New wraps an already-configured Cloud KMS service.
func New(svc *kmsraw.Service, cfg Config) *Backend {
	if cfg.Location == "" {
		cfg.Location = "global"
	}
	return &Backend{svc: svc, cfg: cfg}
}

var _ domain.KMS = (*Backend)(nil)

func (b *Backend) ringPath() string {
	return fmt.Sprintf("projects/%s/locations/%s/keyRings/%s", b.cfg.Project, b.cfg.Location, b.cfg.KeyRing)
}

func (b *Backend) keyPath(id string) string {
	return b.ringPath() + "/cryptoKeys/" + id
}

func (b *Backend) CreateKey(ctx context.Context, opt domain.CreateKeyOptions) (domain.Key, error) {
	id := opt.KeyID
	if id == "" {
		return domain.Key{}, fmt.Errorf("GCP Cloud KMS requires a key ID: %w", domain.ErrInvalidInput)
	}
	ck := &kmsraw.CryptoKey{
		Purpose: "ENCRYPT_DECRYPT",
		Labels:  opt.Tags,
	}
	out, err := b.svc.Projects.Locations.KeyRings.CryptoKeys.Create(b.ringPath(), ck).
		CryptoKeyId(id).Context(ctx).Do()
	if err != nil {
		return domain.Key{}, fmt.Errorf("cryptoKeys.create: %w", err)
	}
	return gcpKeyToDomain(out, id), nil
}

func (b *Backend) DescribeKey(ctx context.Context, id string) (domain.Key, error) {
	out, err := b.svc.Projects.Locations.KeyRings.CryptoKeys.Get(b.keyPath(id)).Context(ctx).Do()
	if err != nil {
		return domain.Key{}, fmt.Errorf("cryptoKeys.get: %w", err)
	}
	return gcpKeyToDomain(out, id), nil
}

func (b *Backend) ListKeys(ctx context.Context) (domain.ListKeysResult, error) {
	out, err := b.svc.Projects.Locations.KeyRings.CryptoKeys.List(b.ringPath()).Context(ctx).Do()
	if err != nil {
		return domain.ListKeysResult{}, fmt.Errorf("cryptoKeys.list: %w", err)
	}
	var keys []domain.Key
	for _, ck := range out.CryptoKeys {
		keys = append(keys, gcpKeyToDomain(ck, gcpLastSeg(ck.Name)))
	}
	return domain.ListKeysResult{Keys: keys}, nil
}

func (b *Backend) Encrypt(ctx context.Context, keyID string, plaintext []byte) (domain.EncryptResult, error) {
	out, err := b.svc.Projects.Locations.KeyRings.CryptoKeys.Encrypt(b.keyPath(keyID), &kmsraw.EncryptRequest{
		Plaintext: base64.StdEncoding.EncodeToString(plaintext),
	}).Context(ctx).Do()
	if err != nil {
		return domain.EncryptResult{}, fmt.Errorf("cryptoKeys.encrypt: %w", err)
	}
	ct, err := base64.StdEncoding.DecodeString(out.Ciphertext)
	if err != nil {
		return domain.EncryptResult{}, fmt.Errorf("decode ciphertext: %w", err)
	}
	return domain.EncryptResult{KeyID: keyID, Ciphertext: ct}, nil
}

func (b *Backend) Decrypt(ctx context.Context, keyID string, ciphertext []byte) (domain.DecryptResult, error) {
	if keyID == "" {
		return domain.DecryptResult{}, fmt.Errorf("GCP Cloud KMS decrypt requires a key ID: %w", domain.ErrInvalidInput)
	}
	out, err := b.svc.Projects.Locations.KeyRings.CryptoKeys.Decrypt(b.keyPath(keyID), &kmsraw.DecryptRequest{
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}).Context(ctx).Do()
	if err != nil {
		return domain.DecryptResult{}, fmt.Errorf("cryptoKeys.decrypt: %w", err)
	}
	pt, err := base64.StdEncoding.DecodeString(out.Plaintext)
	if err != nil {
		return domain.DecryptResult{}, fmt.Errorf("decode plaintext: %w", err)
	}
	return domain.DecryptResult{KeyID: keyID, Plaintext: pt}, nil
}

// ScheduleKeyDeletion / CancelKeyDeletion: Cloud KMS cannot delete keys
// (only destroy versions). Out of intersection for the GCP backend.
func (b *Backend) ScheduleKeyDeletion(_ context.Context, _ string, _ int) (domain.Key, error) {
	return domain.Key{}, domain.ErrNotSupported
}
func (b *Backend) CancelKeyDeletion(_ context.Context, _ string) (domain.Key, error) {
	return domain.Key{}, domain.ErrNotSupported
}

func (b *Backend) EnableKeyRotation(ctx context.Context, keyID string) error {
	_, err := b.svc.Projects.Locations.KeyRings.CryptoKeys.Patch(b.keyPath(keyID), &kmsraw.CryptoKey{
		RotationPeriod: "7776000s", // 90 days
	}).UpdateMask("rotationPeriod,nextRotationTime").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("cryptoKeys.patch (enable rotation): %w", err)
	}
	return nil
}

func (b *Backend) DisableKeyRotation(ctx context.Context, keyID string) error {
	_, err := b.svc.Projects.Locations.KeyRings.CryptoKeys.Patch(b.keyPath(keyID), &kmsraw.CryptoKey{}).
		UpdateMask("rotationPeriod,nextRotationTime").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("cryptoKeys.patch (disable rotation): %w", err)
	}
	return nil
}

func (b *Backend) GetKeyRotationStatus(ctx context.Context, keyID string) (bool, error) {
	out, err := b.svc.Projects.Locations.KeyRings.CryptoKeys.Get(b.keyPath(keyID)).Context(ctx).Do()
	if err != nil {
		return false, fmt.Errorf("cryptoKeys.get: %w", err)
	}
	return out.RotationPeriod != "", nil
}

func gcpKeyToDomain(ck *kmsraw.CryptoKey, id string) domain.Key {
	k := domain.Key{
		ID:              id,
		Usage:           domain.KeyUsageEncryptDecrypt,
		KeySpec:         "google-symmetric-encryption",
		State:           domain.KeyStateEnabled,
		RotationEnabled: ck.RotationPeriod != "",
		Tags:            ck.Labels,
	}
	return k
}

func gcpLastSeg(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			return name[i+1:]
		}
	}
	return name
}
