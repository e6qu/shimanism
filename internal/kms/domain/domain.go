// Package domain holds shimanism's neutral key-management interface and
// types — the lingua franca between three frontend protocols (AWS KMS,
// GCP Cloud KMS, Azure Key Vault keys) and four backends (the three
// clouds + a K8s peer).
//
// Phase 19 intersection: symmetric key lifecycle (create / describe /
// list / schedule-deletion / cancel-deletion), encrypt / decrypt, and
// rotation enable / disable / status. Asymmetric Sign/Verify is a noted
// follow-on (the key-spec surface diverges more across clouds).
//
// The shim is stateless: key material never lives in the shim. Encrypt
// and Decrypt forward to the backend, which holds the keys (the cloud's
// HSM in production; the inmem backend's in-process AES keys in tests).
// Decrypt does not take a key ID — the ciphertext blob carries the key
// reference, mirroring every cloud KMS's Decrypt semantics.
package domain

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors. Frontends map these to each cloud's error vocabulary;
// backends produce them from their cloud's native error codes.
var (
	ErrNotFound      = errors.New("key not found")
	ErrAlreadyExists = errors.New("key already exists")
	ErrInvalidInput  = errors.New("invalid input")
	ErrNotSupported  = errors.New("operation not supported")
	ErrKeyDisabled   = errors.New("key is disabled")
)

func IsNotFound(err error) bool      { return errors.Is(err, ErrNotFound) }
func IsAlreadyExists(err error) bool { return errors.Is(err, ErrAlreadyExists) }
func IsInvalidInput(err error) bool  { return errors.Is(err, ErrInvalidInput) }
func IsNotSupported(err error) bool  { return errors.Is(err, ErrNotSupported) }
func IsKeyDisabled(err error) bool   { return errors.Is(err, ErrKeyDisabled) }

// KeyState is the normalized key lifecycle state.
type KeyState string

const (
	KeyStateEnabled         KeyState = "Enabled"
	KeyStateDisabled        KeyState = "Disabled"
	KeyStatePendingDeletion KeyState = "PendingDeletion"
)

// KeyUsage is what the key may be used for.
type KeyUsage string

const (
	KeyUsageEncryptDecrypt KeyUsage = "ENCRYPT_DECRYPT"
	KeyUsageSignVerify     KeyUsage = "SIGN_VERIFY"
)

// Key is the neutral representation of a KMS key's metadata. Key
// material is never exposed here — it stays in the backend.
type Key struct {
	// ID is the backend-native key identifier (AWS key ID / GCP crypto
	// key resource name / Azure key name).
	ID          string
	Description string
	State       KeyState
	Usage       KeyUsage
	// KeySpec is opaque per-cloud (SYMMETRIC_DEFAULT / google-symmetric-
	// encryption / oct-256 …) — passes through untranslated.
	KeySpec         string
	RotationEnabled bool
	CreatedAt       time.Time
	// DeletionDate is set when State == PendingDeletion.
	DeletionDate time.Time
	Tags         map[string]string
}

// CreateKeyOptions carries inputs for CreateKey.
type CreateKeyOptions struct {
	// KeyID, when non-empty, requests a specific key identifier. GCP
	// Cloud KMS and Azure Key Vault address keys by a user-chosen name;
	// their frontends set this. AWS leaves it empty (KMS auto-generates
	// a key ID).
	KeyID       string
	Description string
	Usage       KeyUsage // default ENCRYPT_DECRYPT
	KeySpec     string   // opaque; backend default if empty
	Tags        map[string]string
}

// ListKeysResult is the result of ListKeys.
type ListKeysResult struct {
	Keys []Key
}

// EncryptResult carries the ciphertext and the key that produced it.
type EncryptResult struct {
	KeyID      string
	Ciphertext []byte
}

// DecryptResult carries the recovered plaintext and the key that the
// ciphertext was encrypted under.
type DecryptResult struct {
	KeyID     string
	Plaintext []byte
}

// KMS is the neutral key-management interface.
type KMS interface {
	CreateKey(ctx context.Context, opts CreateKeyOptions) (Key, error)
	DescribeKey(ctx context.Context, id string) (Key, error)
	ListKeys(ctx context.Context) (ListKeysResult, error)

	Encrypt(ctx context.Context, keyID string, plaintext []byte) (EncryptResult, error)
	// Decrypt recovers plaintext. keyID is optional: AWS symmetric decrypt
	// omits it (the key reference rides in the ciphertext blob), while GCP
	// Cloud KMS and Azure Key Vault address decrypt at a specific key, so
	// their frontends pass the key from the request path. Backends that
	// can recover the key from the blob ignore an empty keyID.
	Decrypt(ctx context.Context, keyID string, ciphertext []byte) (DecryptResult, error)

	ScheduleKeyDeletion(ctx context.Context, keyID string, pendingWindowDays int) (Key, error)
	CancelKeyDeletion(ctx context.Context, keyID string) (Key, error)

	EnableKeyRotation(ctx context.Context, keyID string) error
	DisableKeyRotation(ctx context.Context, keyID string) error
	GetKeyRotationStatus(ctx context.Context, keyID string) (bool, error)
}
