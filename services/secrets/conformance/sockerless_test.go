// Sockerless lane for the secrets service. See docs/sockerless-validation.md.
package conformance_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	"golang.org/x/oauth2"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/internal/secrets/domain"
	awsbackend "github.com/e6qu/shimanism/services/secrets/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/secrets/backends/azure"
	gcpbackend "github.com/e6qu/shimanism/services/secrets/backends/gcp"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
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

// TestSockerless_AWSSecretsFrontendToGCPBackend_RoundTrip drives the
// full through-shim E2E path for secrets:
// aws-sdk-go-v2 Secrets Manager client → AWS-shaped shim frontend →
// GCP Secret Manager backend → sockerless GCP simulator.
func TestSockerless_AWSSecretsFrontendToGCPBackend_RoundTrip(t *testing.T) {
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
	srv := harness.StartSecretsServerAWS(t, backend)
	cli := newAWSSecretsManagerClient(t, srv.URL)

	name := "shim-sk-xsec-" + randomHex(8)
	create, err := cli.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         awsapi.String(name),
		SecretString: awsapi.String("through-shim-v1"),
		Description:  awsapi.String("through-shim sockerless secret"),
	})
	if err != nil {
		t.Fatalf("CreateSecret through shim: %v", err)
	}
	if awsapi.ToString(create.Name) != name {
		t.Errorf("CreateSecret.Name = %q, want %q", awsapi.ToString(create.Name), name)
	}
	t.Cleanup(func() {
		_, _ = cli.DeleteSecret(ctx, &awssm.DeleteSecretInput{
			SecretId:                   awsapi.String(name),
			ForceDeleteWithoutRecovery: awsapi.Bool(true),
		})
	})

	if _, err := cli.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId:     awsapi.String(name),
		SecretString: awsapi.String("through-shim-v2"),
	}); err != nil {
		t.Fatalf("PutSecretValue through shim: %v", err)
	}

	got, err := cli.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId: awsapi.String(name),
	})
	if err != nil {
		t.Fatalf("GetSecretValue through shim: %v", err)
	}
	if awsapi.ToString(got.SecretString) != "through-shim-v2" {
		t.Errorf("SecretString = %q, want through-shim-v2", awsapi.ToString(got.SecretString))
	}

	desc, err := cli.DescribeSecret(ctx, &awssm.DescribeSecretInput{
		SecretId: awsapi.String(name),
	})
	if err != nil {
		t.Fatalf("DescribeSecret through shim: %v", err)
	}
	if awsapi.ToString(desc.Name) != name {
		t.Errorf("DescribeSecret.Name = %q, want %q", awsapi.ToString(desc.Name), name)
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

// TestSockerless_E2E_AzureKV_Through_Shim_ApplyTF drives the honest
// cross-cloud Apply path for Key Vault: `azurerm` Terraform provider
// → sockerless's real Microsoft.KeyVault ARM (which emits the shim's
// data-plane URL in `properties.vaultUri`) → azurerm follows the URL
// for secret PUT with a Microsoft Entra Bearer token sockerless's
// mock token endpoint mints (RS256, sockerless#262) for the
// `https://vault.azure.net/.default` scope (sockerless#272/#274) →
// shim's azure_keyvault frontend verifies the JWT against the JWKS
// the test pre-fetches from sockerless's discovery endpoint → shim
// translates SetSecret onto the in-memory secrets backend.
//
// No shim-side fakes — the same compositional pattern PR #58 landed
// for storage. Linux-only because azurerm reads the system CA via
// SSL_CERT_FILE which Go honors on Unix but not on macOS.
func TestSockerless_E2E_AzureKV_Through_Shim_ApplyTF(t *testing.T) {
	azurePort := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if azurePort == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	shimPortStr := os.Getenv("SHIM_AZUREKV_PORT")
	if shimPortStr == "" {
		t.Skip("SHIM_AZUREKV_PORT not set (the run script defaults to 14582)")
	}
	shimPort, err := strconv.Atoi(shimPortStr)
	if err != nil {
		t.Fatalf("SHIM_AZUREKV_PORT not numeric: %v", err)
	}
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	systemCABundle := findSystemCABundleForKV()
	if systemCABundle == "" {
		t.Skip("no system CA bundle found at known Unix paths — SSL_CERT_FILE workaround requires Linux")
	}
	sockCertPath := os.Getenv("SOCKERLESS_AZURE_TLS_CERT")
	sockKeyPath := os.Getenv("SOCKERLESS_AZURE_TLS_KEY")
	if sockCertPath == "" || sockKeyPath == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT / SOCKERLESS_AZURE_TLS_KEY not set (the run script exports these)")
	}
	sockCertPEM, err := os.ReadFile(sockCertPath)
	if err != nil {
		t.Fatalf("read sockerless cert: %v", err)
	}
	sockKeyPEM, err := os.ReadFile(sockKeyPath)
	if err != nil {
		t.Fatalf("read sockerless key: %v", err)
	}

	const (
		subscriptionID = "00000000-0000-0000-0000-000000000000"
		tenantID       = "00000000-0000-0000-0000-000000000000"
		clientID       = "00000000-0000-0000-0000-000000000000"
		resourceGroup  = "shim-kv-rg"
		vaultName      = "shimvault"
		secretName     = "applied-secret"
		secretValue    = "hunter2-through-shim"
		objectID       = "22222222-2222-2222-2222-222222222222"
	)

	jwks := fetchSockerlessJWKS(t, azurePort, tenantID, sockCertPEM)

	backend := inmem.New()
	shim := harness.StartSecretsServerAzureKVAtPort(
		t,
		backend,
		shimPort,
		sockCertPEM,
		sockKeyPEM,
		jwks,
		"https://vault.azure.net",
		fmt.Sprintf("https://sts.windows.net/%s/", tenantID),
	)
	_ = shim // URL embedded in sockerless ARM emission via env var

	dir := t.TempDir()
	hcl := fmt.Sprintf(
		terraformAzureKVApplyConfig,
		"localhost:"+azurePort, // %[1]s metadata_host
		subscriptionID,         // %[2]s
		tenantID,               // %[3]s
		clientID,               // %[4]s
		resourceGroup,          // %[5]s
		vaultName,              // %[6]s
		objectID,               // %[7]s
		secretName,             // %[8]s
		secretValue,            // %[9]s
	)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	combinedCA := filepath.Join(dir, "combined-ca.pem")
	systemBytes, err := os.ReadFile(systemCABundle)
	if err != nil {
		t.Fatalf("read system CA bundle %s: %v", systemCABundle, err)
	}
	if err := os.WriteFile(combinedCA, append(append(systemBytes, '\n'), sockCertPEM...), 0o644); err != nil {
		t.Fatalf("write combined CA: %v", err)
	}

	runTf := func(args ...string) ([]byte, []byte, error) {
		cmd := exec.Command(tfBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1",
			"TF_INPUT=0",
			"CHECKPOINT_DISABLE=1",
			"SSL_CERT_FILE="+combinedCA,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}
	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runTf(args...)
		if err != nil {
			t.Fatalf("terraform %s\nstdout:\n%s\nstderr:\n%s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("init", "-no-color")
	applyOut := mustRun("apply", "-no-color", "-auto-approve")
	t.Logf("terraform apply stdout:\n%s", applyOut)
	t.Cleanup(func() { _, _, _ = runTf("destroy", "-no-color", "-auto-approve") })

	list, err := backend.ListSecrets(context.Background(), domain.ListSecretsOptions{})
	if err != nil {
		t.Fatalf("backend.ListSecrets: %v", err)
	}
	var found *domain.Secret
	for i, s := range list.Secrets {
		if s.Name == secretName {
			found = &list.Secrets[i]
			break
		}
	}
	if found == nil {
		names := make([]string, 0, len(list.Secrets))
		for _, s := range list.Secrets {
			names = append(names, s.Name)
		}
		t.Fatalf("backend.ListSecrets did not contain %q; got %v", secretName, names)
	}

	val, err := backend.GetSecretValue(context.Background(), secretName, 0)
	if err != nil {
		t.Fatalf("backend.GetSecretValue: %v", err)
	}
	if string(val.Value) != secretValue {
		t.Errorf("backend.GetSecretValue.Value = %q, want %q", string(val.Value), secretValue)
	}
}

// fetchSockerlessJWKS GETs the JWKS sockerless publishes at
// `/{tenant}/discovery/v2.0/keys` using an http.Client that trusts
// the simulator's self-signed TLS cert. Returns the parsed key set
// the shim's azurebearer verifier consumes via Options.JWKS. Doing
// the fetch out-of-band keeps the shim's in-process verifier free of
// any custom TLS-trust plumbing — the JWKS is just static input.
func fetchSockerlessJWKS(t *testing.T, azurePort, tenantID string, certPEM []byte) *azurebearer.JWKS {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("AppendCertsFromPEM: no certs parsed")
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
	url := fmt.Sprintf("https://localhost:%s/%s/discovery/v2.0/keys", azurePort, tenantID)
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read JWKS body: %v", err)
	}
	var jwks azurebearer.JWKS
	if err := json.Unmarshal(body, &jwks); err != nil {
		t.Fatalf("parse JWKS JSON: %v\nbody: %s", err, body)
	}
	if len(jwks.Keys) == 0 {
		t.Fatalf("JWKS at %s is empty", url)
	}
	return &jwks
}

// findSystemCABundleForKV mirrors the helper in the storage sockerless
// test — duplicated because each conformance package compiles
// independently. Returns "" on macOS.
func findSystemCABundleForKV() string {
	for _, p := range []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/pki/tls/certs/ca-bundle.crt",
		"/etc/ssl/ca-bundle.pem",
		"/etc/pki/tls/cacert.pem",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

const terraformAzureKVApplyConfig = `
terraform {
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
  }
}

provider "azurerm" {
  features {}
  metadata_host                   = "%[1]s"
  subscription_id                 = "%[2]s"
  tenant_id                       = "%[3]s"
  client_id                       = "%[4]s"
  client_secret                   = "test-secret-do-not-use-in-prod"
  resource_provider_registrations = "none"
}

resource "azurerm_resource_group" "rg" {
  name     = "%[5]s"
  location = "eastus"
}

resource "azurerm_key_vault" "kv" {
  name                       = "%[6]s"
  resource_group_name        = azurerm_resource_group.rg.name
  location                   = azurerm_resource_group.rg.location
  tenant_id                  = "%[3]s"
  sku_name                   = "standard"
  purge_protection_enabled   = false
  soft_delete_retention_days = 7
}

resource "azurerm_key_vault_access_policy" "policy" {
  key_vault_id = azurerm_key_vault.kv.id
  tenant_id    = "%[3]s"
  object_id    = "%[7]s"

  secret_permissions = ["Get", "List", "Set", "Delete"]
}

resource "azurerm_key_vault_secret" "sec" {
  name         = "%[8]s"
  value        = "%[9]s"
  key_vault_id = azurerm_key_vault.kv.id

  depends_on = [azurerm_key_vault_access_policy.policy]
}
`

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

// gcpHS256Bearer mints a test-mode HS256 JWT that the shim's
// gcpbearer middleware accepts. Same helper as the cache reverse
// lane; duplicated here because each conformance package is its own
// test compilation unit.
func gcpHS256Bearer(t *testing.T, audience string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT","kid":"shim-test"}`))
	payloadJSON := []byte(`{"aud":"` + audience + `","exp":4102444800,"iat":1}`)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, []byte("test-key-do-not-use-in-prod"))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

type gcpStaticTokenSource struct{ token string }

func (s gcpStaticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.token, TokenType: "Bearer"}, nil
}

// TestSockerless_GCPSecretManagerFrontendToAWSBackend_RoundTrip is
// the reverse-direction through-shim cell for secrets: GCP Secret
// Manager SDK drives the shim's GCP frontend, which routes through
// the shim's AWS Secrets Manager backend, which targets sockerless's
// AWS sim. Complement of the existing AWS→GCP cell.
func TestSockerless_GCPSecretManagerFrontendToAWSBackend_RoundTrip(t *testing.T) {
	awsEndpoint := os.Getenv("SOCKERLESS_AWS_SM_ENDPOINT")
	if awsEndpoint == "" {
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
	smClient := awssm.NewFromConfig(cfg, func(o *awssm.Options) {
		o.BaseEndpoint = awsapi.String(awsEndpoint)
	})
	backend := awsbackend.New(smClient)
	srv := harness.StartSecretsServerGCP(t, backend)

	ctx := context.Background()
	c, err := gcpsm.NewRESTClient(ctx,
		option.WithEndpoint(srv.URL+"/"),
		option.WithTokenSource(gcpStaticTokenSource{token: gcpHS256Bearer(t, "https://secretmanager.googleapis.com/")}),
	)
	if err != nil {
		t.Fatalf("gcp sm client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	project := "shim-sockerless"
	name := "shim-sk-rev-sec-" + randomHex(8)
	gcpBackend := gcpbackend.New(c, gcpbackend.Config{ProjectID: project})
	if _, err := gcpBackend.CreateSecret(ctx, name, domain.CreateSecretOptions{
		InitialValue: []byte("rev-source-payload"),
		Tags:         map[string]string{"env": "test"},
	}); err != nil {
		t.Fatalf("CreateSecret through shim: %v", err)
	}
	t.Cleanup(func() { _ = gcpBackend.DeleteSecret(ctx, name, true) })

	v, err := gcpBackend.GetSecretValue(ctx, name, 0)
	if err != nil {
		t.Fatalf("GetSecretValue through shim: %v", err)
	}
	if string(v.Value) != "rev-source-payload" {
		t.Errorf("GetSecretValue = %q, want %q", string(v.Value), "rev-source-payload")
	}
}
