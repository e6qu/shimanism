// Package inmem is an in-memory KMS backend for tests. It is a real
// backend, not a fake: each key holds a real AES-256-GCM key, and
// Encrypt/Decrypt perform real authenticated encryption. The ciphertext
// blob carries the key ID so Decrypt can recover the key without the
// caller supplying it — mirroring every cloud KMS's Decrypt semantics.
//
// All state lives in maps guarded by a single RWMutex; the shim never
// holds key material (this backend IS the source of truth for tests).
package inmem

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/kms/domain"
)

type keyEntry struct {
	meta     domain.Key
	material []byte // 32-byte AES-256 key
}

// Backend implements domain.KMS entirely in memory.
type Backend struct {
	mu   sync.RWMutex
	keys map[string]*keyEntry
	seq  int
	now  func() time.Time
}

// New returns an empty in-memory KMS backend.
func New() *Backend {
	return &Backend{keys: map[string]*keyEntry{}, now: time.Now}
}

var _ domain.KMS = (*Backend)(nil)

func (b *Backend) nextID() string {
	b.seq++
	return fmt.Sprintf("key-%08d", b.seq)
}

func copyTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (b *Backend) CreateKey(_ context.Context, opts domain.CreateKeyOptions) (domain.Key, error) {
	usage := opts.Usage
	if usage == "" {
		usage = domain.KeyUsageEncryptDecrypt
	}
	if usage != domain.KeyUsageEncryptDecrypt {
		// Phase 19.A intersection is symmetric encrypt/decrypt; Sign/Verify
		// is a noted follow-on.
		return domain.Key{}, fmt.Errorf("usage %q: %w", usage, domain.ErrNotSupported)
	}
	spec := opts.KeySpec
	if spec == "" {
		spec = "SYMMETRIC_DEFAULT"
	}
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		return domain.Key{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	id := opts.KeyID
	if id == "" {
		id = b.nextID()
	} else if _, exists := b.keys[id]; exists {
		return domain.Key{}, fmt.Errorf("key %q: %w", id, domain.ErrAlreadyExists)
	}
	e := &keyEntry{
		meta: domain.Key{
			ID:          id,
			Description: opts.Description,
			State:       domain.KeyStateEnabled,
			Usage:       usage,
			KeySpec:     spec,
			CreatedAt:   b.now().UTC(),
			Tags:        copyTags(opts.Tags),
		},
		material: material,
	}
	b.keys[id] = e
	return e.meta, nil
}

func (b *Backend) DescribeKey(_ context.Context, id string) (domain.Key, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	e, ok := b.keys[id]
	if !ok {
		return domain.Key{}, fmt.Errorf("key %q: %w", id, domain.ErrNotFound)
	}
	return e.meta, nil
}

func (b *Backend) ListKeys(_ context.Context) (domain.ListKeysResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []domain.Key
	for _, e := range b.keys {
		out = append(out, e.meta)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return domain.ListKeysResult{Keys: out}, nil
}

func (b *Backend) Encrypt(_ context.Context, keyID string, plaintext []byte) (domain.EncryptResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	e, ok := b.keys[keyID]
	if !ok {
		return domain.EncryptResult{}, fmt.Errorf("key %q: %w", keyID, domain.ErrNotFound)
	}
	if e.meta.State != domain.KeyStateEnabled {
		return domain.EncryptResult{}, fmt.Errorf("key %q: %w", keyID, domain.ErrKeyDisabled)
	}
	gcm, err := newGCM(e.material)
	if err != nil {
		return domain.EncryptResult{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return domain.EncryptResult{}, err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, []byte(keyID))
	// Ciphertext blob: keyIDLen(2) || keyID || nonce || sealed.
	blob := make([]byte, 2+len(keyID)+len(nonce)+len(sealed))
	binary.BigEndian.PutUint16(blob[:2], uint16(len(keyID)))
	off := 2
	off += copy(blob[off:], keyID)
	off += copy(blob[off:], nonce)
	copy(blob[off:], sealed)
	return domain.EncryptResult{KeyID: keyID, Ciphertext: blob}, nil
}

func (b *Backend) Decrypt(_ context.Context, wantKey string, ciphertext []byte) (domain.DecryptResult, error) {
	if len(ciphertext) < 2 {
		return domain.DecryptResult{}, fmt.Errorf("malformed ciphertext: %w", domain.ErrInvalidInput)
	}
	keyLen := int(binary.BigEndian.Uint16(ciphertext[:2]))
	if len(ciphertext) < 2+keyLen {
		return domain.DecryptResult{}, fmt.Errorf("malformed ciphertext: %w", domain.ErrInvalidInput)
	}
	keyID := string(ciphertext[2 : 2+keyLen])
	// If the caller named a key (GCP/Azure decrypt-at-key), it must match the
	// key embedded in the blob.
	if wantKey != "" && wantKey != keyID {
		return domain.DecryptResult{}, fmt.Errorf("ciphertext key %q != requested %q: %w", keyID, wantKey, domain.ErrInvalidInput)
	}
	rest := ciphertext[2+keyLen:]

	b.mu.RLock()
	defer b.mu.RUnlock()
	e, ok := b.keys[keyID]
	if !ok {
		return domain.DecryptResult{}, fmt.Errorf("key %q: %w", keyID, domain.ErrNotFound)
	}
	if e.meta.State != domain.KeyStateEnabled {
		return domain.DecryptResult{}, fmt.Errorf("key %q: %w", keyID, domain.ErrKeyDisabled)
	}
	gcm, err := newGCM(e.material)
	if err != nil {
		return domain.DecryptResult{}, err
	}
	ns := gcm.NonceSize()
	if len(rest) < ns {
		return domain.DecryptResult{}, fmt.Errorf("malformed ciphertext: %w", domain.ErrInvalidInput)
	}
	nonce, sealed := rest[:ns], rest[ns:]
	plain, err := gcm.Open(nil, nonce, sealed, []byte(keyID))
	if err != nil {
		return domain.DecryptResult{}, fmt.Errorf("decrypt: %w", domain.ErrInvalidInput)
	}
	return domain.DecryptResult{KeyID: keyID, Plaintext: plain}, nil
}

func (b *Backend) ScheduleKeyDeletion(_ context.Context, keyID string, pendingWindowDays int) (domain.Key, error) {
	if pendingWindowDays <= 0 {
		pendingWindowDays = 30
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.keys[keyID]
	if !ok {
		return domain.Key{}, fmt.Errorf("key %q: %w", keyID, domain.ErrNotFound)
	}
	e.meta.State = domain.KeyStatePendingDeletion
	e.meta.DeletionDate = b.now().UTC().AddDate(0, 0, pendingWindowDays)
	return e.meta, nil
}

func (b *Backend) CancelKeyDeletion(_ context.Context, keyID string) (domain.Key, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.keys[keyID]
	if !ok {
		return domain.Key{}, fmt.Errorf("key %q: %w", keyID, domain.ErrNotFound)
	}
	if e.meta.State != domain.KeyStatePendingDeletion {
		return domain.Key{}, fmt.Errorf("key %q not pending deletion: %w", keyID, domain.ErrInvalidInput)
	}
	e.meta.State = domain.KeyStateDisabled // AWS returns the key disabled after cancel
	e.meta.DeletionDate = time.Time{}
	return e.meta, nil
}

func (b *Backend) EnableKeyRotation(_ context.Context, keyID string) error {
	return b.setRotation(keyID, true)
}

func (b *Backend) DisableKeyRotation(_ context.Context, keyID string) error {
	return b.setRotation(keyID, false)
}

func (b *Backend) setRotation(keyID string, on bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.keys[keyID]
	if !ok {
		return fmt.Errorf("key %q: %w", keyID, domain.ErrNotFound)
	}
	e.meta.RotationEnabled = on
	return nil
}

func (b *Backend) GetKeyRotationStatus(_ context.Context, keyID string) (bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	e, ok := b.keys[keyID]
	if !ok {
		return false, fmt.Errorf("key %q: %w", keyID, domain.ErrNotFound)
	}
	return e.meta.RotationEnabled, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
