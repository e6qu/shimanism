// Phase 2 conformance: Azure Key Vault secrets-surface frontend
// exercised by the official
// `azure-sdk-for-go/sdk/security/keyvault/azsecrets` SDK. The
// client is pointed at the shim with NewClientWithNoCredential;
// the shim accepts unsigned requests at this phase.
package conformance_test

import (
	"context"
	"crypto/tls"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
)

// fakeTokenCredential satisfies azcore.TokenCredential without
// actually issuing a token. The shim doesn't validate bearer
// tokens at this phase, so any non-empty string works.
type fakeTokenCredential struct{}

func (fakeTokenCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "shim-conformance-fake-token",
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

func newAzureSecretsClient(t *testing.T, endpoint string) *azsecrets.Client {
	t.Helper()
	// httptest.NewTLSServer uses a self-signed cert; accept it.
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	c, err := azsecrets.NewClient(endpoint, fakeTokenCredential{}, &azsecrets.ClientOptions{
		DisableChallengeResourceVerification: true,
		ClientOptions: azcore.ClientOptions{
			Transport: httpClient,
		},
	})
	if err != nil {
		t.Fatalf("new Azure Key Vault secrets client: %v", err)
	}
	return c
}

func TestAzureSDK_SecretLifecycle(t *testing.T) {
	srv := harness.StartSecretsServerAzure(t, inmem.New())
	cli := newAzureSecretsClient(t, srv.URL)
	ctx := context.Background()

	val := "hello-shim"
	if _, err := cli.SetSecret(ctx, "api-token", azsecrets.SetSecretParameters{
		Value: &val,
		Tags: map[string]*string{
			"env": strPtr("test"),
		},
	}, nil); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	// Get latest version.
	got, err := cli.GetSecret(ctx, "api-token", "", nil)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if got.Value == nil {
		t.Errorf("GetSecret value = nil, want hello-shim")
	} else if *got.Value != "hello-shim" {
		t.Errorf("GetSecret value = %q, want hello-shim", *got.Value)
	}
	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Errorf("GetSecret tags env = %v, want test", got.Tags["env"])
	}

	// SetSecret again — same name → new version.
	val2 := "hello-shim-v2"
	if _, err := cli.SetSecret(ctx, "api-token", azsecrets.SetSecretParameters{
		Value: &val2,
	}, nil); err != nil {
		t.Fatalf("SetSecret v2: %v", err)
	}

	// List versions returns 2 entries.
	pager := cli.NewListSecretPropertiesVersionsPager("api-token", nil)
	count := 0
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListSecretPropertiesVersions: %v", err)
		}
		count += len(page.Value)
	}
	if count != 2 {
		t.Errorf("version count = %d, want 2", count)
	}

	// Delete soft-deletes (Azure default).
	if _, err := cli.DeleteSecret(ctx, "api-token", nil); err != nil {
		t.Errorf("DeleteSecret: %v", err)
	}
}

func strPtr(s string) *string { return &s }
