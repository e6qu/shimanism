// Sockerless lane for the secrets service. See doc/SOCKERLESS_VALIDATION.md.
package conformance_test

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/e6qu/shimanism/internal/secrets/domain"
	awsbackend "github.com/e6qu/shimanism/services/secrets/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/secrets/backends/azure"
)

// TestSockerless_AWSSecretsManager_RoundTrip exercises the shim's
// AWS Secrets Manager backend's full lifecycle against a running
// sockerless AWS sim: CreateSecret + HeadSecret + GetSecretValue +
// ListSecrets + DeleteSecret.
//
// Set SOCKERLESS_AWS_SM_ENDPOINT (e.g. https://localhost:14566)
// to opt in.
func TestSockerless_AWSSecretsManager_RoundTrip(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_SM_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_SM_ENDPOINT not set")
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		os.Setenv("AWS_ACCESS_KEY_ID", "test")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		os.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	}
	if os.Getenv("AWS_REGION") == "" {
		os.Setenv("AWS_REGION", "us-east-1")
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	if os.Getenv("AWS_S3_CONFORMANCE_INSECURE_TLS") == "1" {
		cfg.HTTPClient = awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		})
	}
	client := awssm.NewFromConfig(cfg, func(o *awssm.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
	backend := awsbackend.New(client)
	ctx := context.Background()

	name := "shim-sockerless-" + randomHex(8)
	if _, err := backend.CreateSecret(ctx, name, domain.CreateSecretOptions{
		InitialValue: []byte("value-v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteSecret(ctx, name, true) })

	list, err := backend.ListSecrets(ctx, domain.ListSecretsOptions{Prefix: name})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	found := false
	for _, s := range list.Secrets {
		if s.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListSecrets did not contain %q", name)
	}

	desc, err := backend.HeadSecret(ctx, name)
	if err != nil {
		t.Fatalf("HeadSecret: %v", err)
	}
	if desc.Name != name {
		t.Errorf("HeadSecret.Name = %q, want %q", desc.Name, name)
	}
	if desc.CurrentVersion == 0 {
		t.Errorf("HeadSecret.CurrentVersion = 0, want >= 1 (created with InitialValue)")
	}

	got, err := backend.GetSecretValue(ctx, name, 0)
	if err != nil {
		t.Fatalf("GetSecretValue: %v", err)
	}
	if string(got.Value) != "value-v1" {
		t.Errorf("GetSecretValue.Value = %q, want %q", string(got.Value), "value-v1")
	}
}

func randomHex(n int) string {
	b := make([]byte, n/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// noOpCredential satisfies azcore.TokenCredential for sims that
// don't validate the token. Production code paths use real WIF /
// Entra credentials through the harness's verifier layer.
type noOpCredential struct{}

// farFuture is a fixed expiry well past any realistic test runtime.
// Avoids time.Now() so the test is deterministic and not subject to
// day / month / year boundary effects.
var farFuture = time.Date(2099, time.December, 31, 23, 59, 59, 0, time.UTC)

func (noOpCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "sockerless-test", ExpiresOn: farFuture}, nil
}

// TestSockerless_Azure_KeyVault_SecretRoundTrip exercises the
// shim's Azure Key Vault secrets backend: CreateSecret +
// GetSecretValue + DeleteSecret round-trip.
//
// Set SOCKERLESS_AZURE_KV_URL (e.g. `https://testvault.vault.azure.net`)
// to opt in. The sim must be running under TLS (SIM_TLS_CERT +
// SIM_TLS_KEY) so the Azure SDK accepts the https endpoint; set
// SOCKERLESS_AZURE_TLS_PORT to the sim's port.
func TestSockerless_Azure_KeyVault_SecretRoundTrip(t *testing.T) {
	vaultURL := os.Getenv("SOCKERLESS_AZURE_KV_URL")
	if vaultURL == "" {
		t.Skip("SOCKERLESS_AZURE_KV_URL not set")
	}
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	httpClient := &http.Client{Transport: localhostDialTransport(port)}

	c, err := azsecrets.NewClient(vaultURL, noOpCredential{}, &azsecrets.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: httpClient},
	})
	if err != nil {
		t.Fatalf("kv secrets client: %v", err)
	}
	backend := azurebackend.New(c)
	ctx := context.Background()

	name := "shim-sk-sec-" + randomHex(8)
	if _, err := backend.CreateSecret(ctx, name, domain.CreateSecretOptions{
		InitialValue: []byte("kv-v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteSecret(ctx, name, true) })

	got, err := backend.GetSecretValue(ctx, name, 0)
	if err != nil {
		t.Fatalf("GetSecretValue: %v", err)
	}
	if string(got.Value) != "kv-v1" {
		t.Errorf("GetSecretValue.Value = %q, want %q", string(got.Value), "kv-v1")
	}
}

// localhostDialTransport returns an *http.Transport that routes every
// outbound TCP connection to 127.0.0.1:port, regardless of the
// requested host. The HTTP request still carries the original Host
// header (the SDK derives it from the vault URL), which sockerless
// dispatches on. This avoids any DNS plumbing for *.localhost or
// *.vault.azure.net hostnames. InsecureSkipVerify is used because
// the sim's TLS cert is self-signed.
func localhostDialTransport(port string) *http.Transport {
	dialer := &net.Dialer{}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "127.0.0.1:"+port)
		},
		TLSClientConfig: &tls.Config{
			// ServerName empty → uses host from the URL → SNI matches
			// the sim's self-signed cert's CN=localhost.
			InsecureSkipVerify: true,
		},
	}
}
