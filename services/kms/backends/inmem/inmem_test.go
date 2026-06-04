package inmem_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/e6qu/shimanism/internal/kms/domain"
	"github.com/e6qu/shimanism/services/kms/backends/inmem"
)

func TestKey_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()

	key, err := b.CreateKey(ctx, domain.CreateKeyOptions{Description: "test"})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if key.ID == "" || key.State != domain.KeyStateEnabled {
		t.Fatalf("unexpected key: %+v", key)
	}

	got, err := b.DescribeKey(ctx, key.ID)
	if err != nil || got.Description != "test" {
		t.Fatalf("DescribeKey: %v %+v", err, got)
	}

	res, err := b.ListKeys(ctx)
	if err != nil || len(res.Keys) != 1 {
		t.Fatalf("ListKeys: %v count=%d", err, len(res.Keys))
	}

	// Schedule + cancel deletion.
	sched, err := b.ScheduleKeyDeletion(ctx, key.ID, 7)
	if err != nil || sched.State != domain.KeyStatePendingDeletion || sched.DeletionDate.IsZero() {
		t.Fatalf("ScheduleKeyDeletion: %v %+v", err, sched)
	}
	cancelled, err := b.CancelKeyDeletion(ctx, key.ID)
	if err != nil || cancelled.State != domain.KeyStateDisabled {
		t.Fatalf("CancelKeyDeletion: %v %+v", err, cancelled)
	}

	if _, err := b.DescribeKey(ctx, "nonexistent"); !domain.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestKey_EncryptDecryptRoundTrip(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()
	key, _ := b.CreateKey(ctx, domain.CreateKeyOptions{})

	plaintext := []byte("the quick brown fox")
	enc, err := b.Encrypt(ctx, key.ID, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(enc.Ciphertext, plaintext) {
		t.Fatal("ciphertext equals plaintext")
	}

	dec, err := b.Decrypt(ctx, "", enc.Ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if dec.KeyID != key.ID {
		t.Errorf("Decrypt KeyID = %q, want %q", dec.KeyID, key.ID)
	}
	if !bytes.Equal(dec.Plaintext, plaintext) {
		t.Errorf("round-trip mismatch: %q != %q", dec.Plaintext, plaintext)
	}

	// Two encryptions of the same plaintext differ (random nonce).
	enc2, _ := b.Encrypt(ctx, key.ID, plaintext)
	if bytes.Equal(enc.Ciphertext, enc2.Ciphertext) {
		t.Error("two encryptions produced identical ciphertext (nonce reuse)")
	}

	// Tampered ciphertext fails authentication.
	bad := append([]byte{}, enc.Ciphertext...)
	bad[len(bad)-1] ^= 0xff
	if _, err := b.Decrypt(ctx, "", bad); err == nil {
		t.Error("tampered ciphertext decrypted without error")
	}
}

func TestKey_Rotation(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()
	key, _ := b.CreateKey(ctx, domain.CreateKeyOptions{})

	on, _ := b.GetKeyRotationStatus(ctx, key.ID)
	if on {
		t.Error("rotation should default off")
	}
	if err := b.EnableKeyRotation(ctx, key.ID); err != nil {
		t.Fatalf("EnableKeyRotation: %v", err)
	}
	if on, _ := b.GetKeyRotationStatus(ctx, key.ID); !on {
		t.Error("rotation should be on after enable")
	}
}

func TestKey_SignVerifyUnsupported(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()
	if _, err := b.CreateKey(ctx, domain.CreateKeyOptions{Usage: domain.KeyUsageSignVerify}); !domain.IsNotSupported(err) {
		t.Fatalf("expected ErrNotSupported for SIGN_VERIFY, got %v", err)
	}
}
