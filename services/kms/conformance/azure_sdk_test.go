// Conformance: Azure Key Vault keys data-plane frontend exercised by the
// official azure-sdk-for-go/.../azkeys SDK. Covers Phase 19.B: CreateKey,
// GetKey, Encrypt, Decrypt. Key Vault standard keys are asymmetric; the
// shim's encrypt/decrypt treats the ciphertext as opaque bytes, so the
// round-trip is backend-agnostic.
package conformance_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/kms/backends/inmem"
)

type kvKeysCred struct{}

func (kvKeysCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	jwt := azurebearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://vault.azure.net",
		15*time.Minute,
	)
	return azcore.AccessToken{Token: jwt, ExpiresOn: time.Now().Add(15 * time.Minute)}, nil
}

func newAzureKeysClient(t *testing.T, endpoint string) *azkeys.Client {
	t.Helper()
	httpClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}}
	c, err := azkeys.NewClient(endpoint, kvKeysCred{}, &azkeys.ClientOptions{
		DisableChallengeResourceVerification: true,
		ClientOptions:                        azcore.ClientOptions{Transport: httpClient},
	})
	if err != nil {
		t.Fatalf("new Azure Key Vault keys client: %v", err)
	}
	return c
}

func TestAzureSDK_KMS_KeyAndCrypto(t *testing.T) {
	srv := harness.StartKMSServerAzure(t, inmem.New())
	cli := newAzureKeysClient(t, srv.URL)
	ctx := context.Background()

	// CreateKey (RSA — standard Key Vault key type).
	created, err := cli.CreateKey(ctx, "shim-key", azkeys.CreateKeyParameters{
		Kty: to.Ptr(azkeys.KeyTypeRSA),
	}, nil)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if created.Key == nil || created.Key.KID == nil {
		t.Fatal("CreateKey returned no key id")
	}

	// GetKey.
	got, err := cli.GetKey(ctx, "shim-key", "", nil)
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if got.Key == nil || got.Key.KID == nil {
		t.Fatal("GetKey returned no key id")
	}

	// Encrypt / Decrypt round-trip.
	plaintext := []byte("azure-kms-secret")
	enc, err := cli.Encrypt(ctx, "shim-key", "", azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP),
		Value:     plaintext,
	}, nil)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(enc.Result) == 0 {
		t.Fatal("Encrypt returned empty result")
	}
	if bytes.Equal(enc.Result, plaintext) {
		t.Error("ciphertext equals plaintext")
	}

	dec, err := cli.Decrypt(ctx, "shim-key", "", azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP),
		Value:     enc.Result,
	}, nil)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(dec.Result, plaintext) {
		t.Errorf("Decrypt round-trip = %q, want %q", dec.Result, plaintext)
	}
}
