// Package azure is the Azure Key Vault keys passthrough backend for
// shimanism's key-management service (Phase 19). It uses
// azure-sdk-for-go/.../azkeys to drive a real Key Vault (or a
// sockerless-pointed client for tests).
//
// The flat domain key ID maps to a Key Vault key name. Standard Key
// Vault keys are asymmetric (RSA); Encrypt/Decrypt use RSA-OAEP and the
// ciphertext is opaque to the shim. Key material never leaves the vault.
package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"github.com/e6qu/shimanism/internal/kms/domain"
)

// Backend implements domain.KMS via real Azure Key Vault keys.
type Backend struct {
	c *azkeys.Client
}

// New wraps an already-configured azkeys client (bound to a vault URL).
func New(c *azkeys.Client) *Backend { return &Backend{c: c} }

var _ domain.KMS = (*Backend)(nil)

func (b *Backend) CreateKey(ctx context.Context, opt domain.CreateKeyOptions) (domain.Key, error) {
	if opt.KeyID == "" {
		return domain.Key{}, fmt.Errorf("key name required for Azure Key Vault: %w", domain.ErrInvalidInput)
	}
	kty := azkeys.KeyTypeRSA
	if opt.KeySpec != "" {
		kty = azkeys.KeyType(opt.KeySpec)
	}
	resp, err := b.c.CreateKey(ctx, opt.KeyID, azkeys.CreateKeyParameters{Kty: &kty}, nil)
	if err != nil {
		return domain.Key{}, fmt.Errorf("CreateKey: %w", err)
	}
	return azureKeyToDomain(opt.KeyID, resp.Attributes), nil
}

func (b *Backend) DescribeKey(ctx context.Context, id string) (domain.Key, error) {
	resp, err := b.c.GetKey(ctx, id, "", nil)
	if err != nil {
		return domain.Key{}, fmt.Errorf("GetKey: %w", err)
	}
	return azureKeyToDomain(id, resp.Attributes), nil
}

func (b *Backend) ListKeys(ctx context.Context) (domain.ListKeysResult, error) {
	var keys []domain.Key
	pager := b.c.NewListKeyPropertiesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return domain.ListKeysResult{}, fmt.Errorf("ListKeyProperties: %w", err)
		}
		for _, p := range page.Value {
			if p.KID != nil {
				keys = append(keys, domain.Key{ID: p.KID.Name(), State: domain.KeyStateEnabled})
			}
		}
	}
	return domain.ListKeysResult{Keys: keys}, nil
}

func (b *Backend) Encrypt(ctx context.Context, keyID string, plaintext []byte) (domain.EncryptResult, error) {
	resp, err := b.c.Encrypt(ctx, keyID, "", azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP),
		Value:     plaintext,
	}, nil)
	if err != nil {
		return domain.EncryptResult{}, fmt.Errorf("Encrypt: %w", err)
	}
	return domain.EncryptResult{KeyID: keyID, Ciphertext: resp.Result}, nil
}

func (b *Backend) Decrypt(ctx context.Context, keyID string, ciphertext []byte) (domain.DecryptResult, error) {
	if keyID == "" {
		return domain.DecryptResult{}, fmt.Errorf("key name required for Azure Key Vault decrypt: %w", domain.ErrInvalidInput)
	}
	resp, err := b.c.Decrypt(ctx, keyID, "", azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP),
		Value:     ciphertext,
	}, nil)
	if err != nil {
		return domain.DecryptResult{}, fmt.Errorf("Decrypt: %w", err)
	}
	return domain.DecryptResult{KeyID: keyID, Plaintext: resp.Result}, nil
}

func (b *Backend) ScheduleKeyDeletion(ctx context.Context, keyID string, _ int) (domain.Key, error) {
	if _, err := b.c.DeleteKey(ctx, keyID, nil); err != nil {
		return domain.Key{}, fmt.Errorf("DeleteKey: %w", err)
	}
	return domain.Key{ID: keyID, State: domain.KeyStatePendingDeletion}, nil
}

func (b *Backend) CancelKeyDeletion(ctx context.Context, keyID string) (domain.Key, error) {
	if _, err := b.c.RecoverDeletedKey(ctx, keyID, nil); err != nil {
		return domain.Key{}, fmt.Errorf("RecoverDeletedKey: %w", err)
	}
	return domain.Key{ID: keyID, State: domain.KeyStateEnabled}, nil
}

func (b *Backend) EnableKeyRotation(ctx context.Context, keyID string) error {
	_, err := b.c.UpdateKeyRotationPolicy(ctx, keyID, azkeys.KeyRotationPolicy{
		LifetimeActions: []*azkeys.LifetimeAction{{
			Action:  &azkeys.LifetimeActionType{Type: to.Ptr(azkeys.KeyRotationPolicyActionRotate)},
			Trigger: &azkeys.LifetimeActionTrigger{TimeAfterCreate: to.Ptr("P90D")},
		}},
	}, nil)
	if err != nil {
		return fmt.Errorf("UpdateKeyRotationPolicy (enable): %w", err)
	}
	return nil
}

func (b *Backend) DisableKeyRotation(ctx context.Context, keyID string) error {
	_, err := b.c.UpdateKeyRotationPolicy(ctx, keyID, azkeys.KeyRotationPolicy{
		LifetimeActions: []*azkeys.LifetimeAction{},
	}, nil)
	if err != nil {
		return fmt.Errorf("UpdateKeyRotationPolicy (disable): %w", err)
	}
	return nil
}

func (b *Backend) GetKeyRotationStatus(ctx context.Context, keyID string) (bool, error) {
	resp, err := b.c.GetKeyRotationPolicy(ctx, keyID, nil)
	if err != nil {
		return false, fmt.Errorf("GetKeyRotationPolicy: %w", err)
	}
	for _, la := range resp.LifetimeActions {
		if la.Action != nil && la.Action.Type != nil && *la.Action.Type == azkeys.KeyRotationPolicyActionRotate {
			return true, nil
		}
	}
	return false, nil
}

func azureKeyToDomain(id string, attr *azkeys.KeyAttributes) domain.Key {
	k := domain.Key{ID: id, Usage: domain.KeyUsageEncryptDecrypt, State: domain.KeyStateEnabled}
	if attr != nil {
		if attr.Enabled != nil && !*attr.Enabled {
			k.State = domain.KeyStateDisabled
		}
		if attr.Created != nil {
			k.CreatedAt = *attr.Created
		}
	}
	return k
}
