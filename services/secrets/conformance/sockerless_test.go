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

	gcpsm "cloud.google.com/go/secretmanager/apiv1"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/secrets/domain"
	awsbackend "github.com/e6qu/shimanism/services/secrets/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/secrets/backends/azure"
	gcpbackend "github.com/e6qu/shimanism/services/secrets/backends/gcp"
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

// TestSockerless_GCPSecretManager_RoundTrip exercises the shim's
// GCP Secret Manager backend against the sockerless GCP sim using
// the official REST transport: CreateSecret + PutSecretValue +
// HeadSecret + GetSecretValue(latest and explicit version) +
// ListSecrets + ListVersions + UpdateSecret + DeleteSecret.
//
// Set SOCKERLESS_GCP_ENDPOINT (host:port, e.g. localhost:14567) to
// opt in.
func TestSockerless_GCPSecretManager_RoundTrip(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	ctx := context.Background()
	c, err := gcpsm.NewRESTClient(ctx,
		option.WithEndpoint("http://"+endpoint+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("gcp secretmanager client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	project := os.Getenv("SOCKERLESS_GCP_PROJECT")
	if project == "" {
		project = "shim-sockerless"
	}
	backend := gcpbackend.New(c, gcpbackend.Config{ProjectID: project})

	name := "shim-sk-gcp-sec-" + randomHex(8)
	if res, err := backend.CreateSecret(ctx, name, domain.CreateSecretOptions{
		Description:  "gcp sockerless secret",
		Tags:         map[string]string{"env": "test"},
		InitialValue: []byte("gcp-v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	} else if res.Version != 1 {
		t.Fatalf("CreateSecret.Version = %d, want 1", res.Version)
	}
	t.Cleanup(func() { _ = backend.DeleteSecret(ctx, name, true) })

	if res, err := backend.PutSecretValue(ctx, name, []byte("gcp-v2")); err != nil {
		t.Fatalf("PutSecretValue: %v", err)
	} else if res.Version != 2 {
		t.Fatalf("PutSecretValue.Version = %d, want 2", res.Version)
	}

	head, err := backend.HeadSecret(ctx, name)
	if err != nil {
		t.Fatalf("HeadSecret: %v", err)
	}
	if head.Name != name {
		t.Errorf("HeadSecret.Name = %q, want %q", head.Name, name)
	}
	if head.Description != "gcp sockerless secret" {
		t.Errorf("HeadSecret.Description = %q, want %q", head.Description, "gcp sockerless secret")
	}
	if got := head.Tags["env"]; got != "test" {
		t.Errorf("HeadSecret.Tags[env] = %q, want test", got)
	}
	if head.CurrentVersion != 2 {
		t.Errorf("HeadSecret.CurrentVersion = %d, want 2", head.CurrentVersion)
	}

	updatedDescription := "gcp sockerless secret updated"
	if err := backend.UpdateSecret(ctx, name, domain.UpdateSecretOptions{
		Description: &updatedDescription,
		Tags:        map[string]string{"team": "shim"},
	}); err != nil {
		t.Fatalf("UpdateSecret: %v", err)
	}
	updatedHead, err := backend.HeadSecret(ctx, name)
	if err != nil {
		t.Fatalf("HeadSecret after update: %v", err)
	}
	if updatedHead.Description != updatedDescription {
		t.Errorf("updated HeadSecret.Description = %q, want %q", updatedHead.Description, updatedDescription)
	}
	if got := updatedHead.Tags["team"]; got != "shim" {
		t.Errorf("updated HeadSecret.Tags[team] = %q, want shim", got)
	}

	latest, err := backend.GetSecretValue(ctx, name, 0)
	if err != nil {
		t.Fatalf("GetSecretValue latest: %v", err)
	}
	if latest.Version != 2 || string(latest.Value) != "gcp-v2" {
		t.Errorf("latest = version %d value %q, want version 2 value gcp-v2", latest.Version, string(latest.Value))
	}

	first, err := backend.GetSecretValue(ctx, name, 1)
	if err != nil {
		t.Fatalf("GetSecretValue v1: %v", err)
	}
	if first.Version != 1 || string(first.Value) != "gcp-v1" {
		t.Errorf("v1 = version %d value %q, want version 1 value gcp-v1", first.Version, string(first.Value))
	}

	versions, err := backend.ListVersions(ctx, name)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("ListVersions count = %d, want 2", len(versions))
	}
	if versions[0].Number != 1 || versions[1].Number != 2 {
		t.Errorf("ListVersions = %+v, want [1 2]", versions)
	}

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
