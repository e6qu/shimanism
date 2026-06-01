// Sockerless lane for the NoSQL service.
//
// Tests here drive a real `terraform` against the shim, with the
// shim's Azure Cosmos Tables frontend wired so:
//
//   - ARM paths flow through the shim's passthrough into sockerless's
//     Azure ARM stub (which gained Cosmos + Storage Tables ARM
//     coverage in sockerless PR #357).
//   - `/metadata/endpoints` redirects azurerm to sockerless for Entra
//     token acquisition while keeping `resourceManager = shim`.
//   - The shim's bearer verifier validates sockerless-issued tokens
//     via the JWKS fetched live from sockerless.
//
// The pattern is the DNS analogue
// (`services/dns/conformance/sockerless_test.go::TestSockerless_AzureDNS_Through_Shim_Terraform_Apply`)
// adapted for `azurerm_cosmosdb_account` + `azurerm_cosmosdb_table`.
// Skips when SOCKERLESS_AZURE_TLS_PORT / SOCKERLESS_AZURE_TLS_CERT
// aren't set; the `sockerless through-shim e2e` CI lane sets them.
package conformance_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/harness"
	azurectfront "github.com/e6qu/shimanism/internal/nosql/frontends/azure_cosmos_tables"
	"github.com/e6qu/shimanism/services/nosql/backends/inmem"
)

// TestSockerless_AzureCosmosTables_Through_Shim_Terraform_Apply drives
// `terraform apply`/`destroy` of `azurerm_cosmosdb_account` +
// `azurerm_cosmosdb_table` through the shim's Cosmos Tables frontend
// configured with ARM passthrough → sockerless. Validates that the
// metadata + bearer + passthrough plumbing (BUG-50) closes the loop
// with sockerless's Tables ARM (closed via sockerless PR #357).
func TestSockerless_AzureCosmosTables_Through_Shim_Terraform_Apply(t *testing.T) {
	azureTLSPort := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if azureTLSPort == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	sockCertPath := os.Getenv("SOCKERLESS_AZURE_TLS_CERT")
	if sockCertPath == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set")
	}
	sockCertPEM, err := os.ReadFile(sockCertPath)
	if err != nil {
		t.Fatalf("read sockerless cert: %v", err)
	}
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	systemCABundle := findSystemCABundleForNoSQL()
	if systemCABundle == "" {
		t.Skip("no system CA bundle found — SSL_CERT_FILE workaround requires Linux")
	}

	const (
		subscriptionID = "00000000-0000-0000-0000-000000000000"
		tenantID       = "00000000-0000-0000-0000-000000000000"
		clientID       = "00000000-0000-0000-0000-000000000000"
		resourceGroup  = "shim-cosmostables-rg"
		accountName    = "shimcosmosacct"
		tableName      = "shimtable"
	)

	// 1. Reverse proxy: shim's passthrough → sockerless's Azure ARM
	//    over TLS, with RootCAs cert pinning (no InsecureSkipVerify).
	sockerlessARM, err := url.Parse("https://localhost:" + azureTLSPort)
	if err != nil {
		t.Fatalf("parse sockerless URL: %v", err)
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(sockCertPEM) {
		t.Fatalf("append sockerless cert to pool")
	}
	proxy := httputil.NewSingleHostReverseProxy(sockerlessARM)
	proxy.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: rootCAs}}

	// 2. Sockerless JWKS so the shim's bearer verifier accepts the
	//    tokens sockerless's Entra ID stub issues to azurerm.
	jwks := fetchSockerlessNoSQLJWKS(t, azureTLSPort, tenantID, sockCertPEM)

	// 3. Shim with passthrough → sockerless ARM and the cloud-metadata
	//    endpoint pointing auth + service URLs at sockerless. The
	//    Cosmos Tables data plane stays local against an inmem
	//    backend (the test exercises ARM only — `azurerm_cosmosdb_table`
	//    is an ARM resource).
	shim := harness.StartNoSQLServerAzureWithConfig(t, inmem.New(), azurectfront.Config{
		Passthrough:      proxy,
		MetadataLoginURL: sockerlessARM.String(),
		BearerOptions: azurebearer.Options{
			Issuer: fmt.Sprintf("https://sts.windows.net/%s/", tenantID),
			// Audience deliberately empty — sockerless mints tokens
			// with `aud = <shim_url>` (since /metadata/endpoints
			// declares the shim as resourceManager) and the shim's
			// URL is dynamic at httptest setup. Signature, Issuer,
			// Exp/Nbf checks still apply.
			JWKS: jwks,
		},
	})

	// 4. Combined CA bundle = system + sockerless cert + shim cert so
	//    Terraform's HTTPS handshakes succeed on both legs.
	dir := t.TempDir()
	systemBytes, err := os.ReadFile(systemCABundle)
	if err != nil {
		t.Fatalf("read system CA: %v", err)
	}
	combined := append(append([]byte{}, systemBytes...), '\n')
	combined = append(combined, sockCertPEM...)
	combined = append(combined, '\n')
	combined = append(combined, shim.CertPEM...)
	combinedPath := filepath.Join(dir, "combined-ca.pem")
	if err := os.WriteFile(combinedPath, combined, 0o644); err != nil {
		t.Fatalf("write combined CA: %v", err)
	}

	// 5. Terraform config. Single `metadata_host` drives both Cosmos
	//    DB account ARM and Tables ARM through the shim.
	hcl := fmt.Sprintf(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.0" }
  }
}

provider "azurerm" {
  features {}
  metadata_host                   = %q
  subscription_id                 = %q
  tenant_id                       = %q
  client_id                       = %q
  client_secret                   = "shim-test"
  use_oidc                        = false
  use_cli                         = false
  resource_provider_registrations = "none"
}

resource "azurerm_resource_group" "tf" {
  name     = %q
  location = "eastus"
}

resource "azurerm_cosmosdb_account" "tf" {
  name                = %q
  location            = azurerm_resource_group.tf.location
  resource_group_name = azurerm_resource_group.tf.name
  offer_type          = "Standard"
  kind                = "GlobalDocumentDB"

  capabilities {
    name = "EnableTable"
  }

  consistency_policy {
    consistency_level = "Session"
  }

  geo_location {
    location          = azurerm_resource_group.tf.location
    failover_priority = 0
  }
}

resource "azurerm_cosmosdb_table" "tf" {
  name                = %q
  resource_group_name = azurerm_resource_group.tf.name
  account_name        = azurerm_cosmosdb_account.tf.name
  throughput          = 400
}
`, shimHostNoSQL(shim.URL), subscriptionID, tenantID, clientID, resourceGroup, accountName, tableName)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	runTf := func(args ...string) {
		t.Helper()
		cmd := exec.Command(tfBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1",
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirForNoSQLWorkdir(dir),
			"SSL_CERT_FILE="+combinedPath,
			"ARM_CLIENT_SECRET=shim-test",
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout.String(), stderr.String(), err)
		}
	}
	runTf("init", "-no-color")
	runTf("apply", "-auto-approve", "-no-color")
	runTf("destroy", "-auto-approve", "-no-color")
}

// shimHostNoSQL extracts the `host:port` authority from a URL like
// `https://127.0.0.1:NN`. azurerm's `metadata_host` expects this
// shape (no scheme); it prepends https:// itself.
func shimHostNoSQL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host
}

// findSystemCABundleForNoSQL locates the OS CA bundle Terraform's
// child processes need (via SSL_CERT_FILE) to validate the combined
// chain. Linux-only — macOS doesn't expose a single PEM file. The
// DNS package has the same helper under findSystemCABundleForDNS;
// we duplicate here so the file is self-contained.
func findSystemCABundleForNoSQL() string {
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

// fetchSockerlessNoSQLJWKS pulls sockerless's Entra ID stub JWKS so
// the shim's Azure bearer verifier validates tokens sockerless
// issues. Mirror of `fetchSockerlessDNSJWKS` in the DNS lane.
func fetchSockerlessNoSQLJWKS(t *testing.T, azurePort, tenantID string, certPEM []byte) *azurebearer.JWKS {
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
	u := fmt.Sprintf("https://localhost:%s/%s/discovery/v2.0/keys", azurePort, tenantID)
	resp, err := client.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read JWKS body: %v", err)
	}
	var jwks azurebearer.JWKS
	if err := json.Unmarshal(body, &jwks); err != nil {
		t.Fatalf("parse JWKS: %v\nbody: %s", err, body)
	}
	if len(jwks.Keys) == 0 {
		t.Fatalf("JWKS at %s is empty", u)
	}
	return &jwks
}
