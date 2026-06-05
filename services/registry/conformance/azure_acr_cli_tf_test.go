// Conformance: Azure Container Registry ARM host surface exercised by
// the official `az acr` CLI and `hashicorp/azurerm` Terraform provider.
//
// These lanes need a real Entra-shaped token endpoint. They run through
// sockerless for auth/resource-group passthrough and skip cleanly unless
// SOCKERLESS_AZURE_TLS_PORT and SOCKERLESS_AZURE_TLS_CERT are set.
package conformance_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/registry/frontends/azure_acr"
	"github.com/e6qu/shimanism/services/registry/backends/inmem"
)

const (
	acrSubscriptionID = "00000000-0000-0000-0000-000000000000"
	acrTenantID       = "00000000-0000-0000-0000-000000000000"
	acrClientID       = "00000000-0000-0000-0000-000000000000"
	acrClientSecret   = "shim-test"
)

type acrAzureSession struct {
	dir     string
	shim    *httptest.Server
	sockURL string
	caPath  string
}

func newACRAzureSession(t *testing.T) *acrAzureSession {
	t.Helper()
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	certPath := os.Getenv("SOCKERLESS_AZURE_TLS_CERT")
	if certPath == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set")
	}
	sockCertPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read sockerless cert: %v", err)
	}
	systemCA := findSystemCABundleForACR()
	if systemCA == "" {
		t.Skip("no system CA bundle found - SSL_CERT_FILE workaround requires Linux")
	}
	systemBytes, err := os.ReadFile(systemCA)
	if err != nil {
		t.Fatalf("read system CA: %v", err)
	}

	sockURL := "https://localhost:" + port
	parsedSock, err := url.Parse(sockURL)
	if err != nil {
		t.Fatalf("parse sockerless URL: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(sockCertPEM) {
		t.Fatalf("AppendCertsFromPEM: no sockerless certs parsed")
	}
	proxy := httputil.NewSingleHostReverseProxy(parsedSock)
	proxy.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}} //nolint:gosec

	jwks := fetchSockerlessACRJWKS(t, port, acrTenantID, sockCertPEM)
	shim := httptest.NewTLSServer(azure_acr.HandlerWithConfig(inmem.New(), azure_acr.Config{
		Passthrough:      proxy,
		MetadataLoginURL: sockURL,
		BearerOptions: azurebearer.Options{
			Issuer: fmt.Sprintf("https://sts.windows.net/%s/", acrTenantID),
			JWKS:   jwks,
		},
	}))
	t.Cleanup(shim.Close)

	shimCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: shim.Certificate().Raw})
	dir := t.TempDir()
	combined := append(append([]byte{}, systemBytes...), '\n')
	combined = append(combined, sockCertPEM...)
	combined = append(combined, '\n')
	combined = append(combined, shimCertPEM...)
	caPath := filepath.Join(dir, "combined-ca.pem")
	if err := os.WriteFile(caPath, combined, 0o644); err != nil {
		t.Fatalf("write combined CA: %v", err)
	}
	return &acrAzureSession{dir: dir, shim: shim, sockURL: sockURL, caPath: caPath}
}

func TestAzureCLI_ACR_RegistryHostLifecycle(t *testing.T) {
	azBin, err := exec.LookPath("az")
	if err != nil {
		t.Skipf("az CLI not installed: %v", err)
	}
	s := newACRAzureSession(t)
	azureConfigDir := filepath.Join(s.dir, ".azure")
	if err := os.MkdirAll(azureConfigDir, 0o755); err != nil {
		t.Fatalf("mkdir azure config: %v", err)
	}
	runAz := func(args ...string) ([]byte, []byte, error) {
		t.Helper()
		cmd := exec.Command(azBin, args...)
		cmd.Env = append(os.Environ(),
			"AZURE_CONFIG_DIR="+azureConfigDir,
			"SSL_CERT_FILE="+s.caPath,
			"REQUESTS_CA_BUNDLE="+s.caPath,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		return stdout.Bytes(), stderr.Bytes(), cmd.Run()
	}
	mustAz := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAz(args...)
		if err != nil {
			t.Fatalf("az %s\nstdout: %s\nstderr: %s\nerr: %v", strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	profile := "shim-acr-conformance"
	mustAz("cloud", "register",
		"--name", profile,
		"--endpoint-resource-manager", s.shim.URL,
		"--endpoint-active-directory", s.sockURL,
		"--endpoint-active-directory-resource-id", "https://management.azure.com/",
		"--endpoint-active-directory-graph-resource-id", s.sockURL,
		"--suffix-acr-login-server-endpoint", s.shim.Listener.Addr().String(),
		"--suffix-keyvault-dns", ".vault.localhost",
		"--suffix-storage-endpoint", "storage.localhost",
	)
	t.Cleanup(func() {
		_, _, _ = runAz("cloud", "set", "--name", "AzureCloud")
		_, _, _ = runAz("cloud", "unregister", "--name", profile)
	})
	mustAz("cloud", "set", "--name", profile)
	mustAz("login", "--service-principal", "--username", acrClientID, "--password", acrClientSecret, "--tenant", acrTenantID, "--allow-no-subscriptions")
	mustAz("account", "set", "--subscription", acrSubscriptionID)

	const (
		rg   = "shim-acr-cli-rg"
		name = "cliacr"
	)
	mustAz("group", "create", "--name", rg, "--location", "eastus")
	t.Cleanup(func() { _, _, _ = runAz("group", "delete", "--name", rg, "--yes", "--no-wait") })

	out := mustAz("acr", "create", "--resource-group", rg, "--name", name, "--sku", "Basic", "--location", "eastus", "-o", "json")
	var created struct {
		Name       string `json:"name"`
		Login      string `json:"loginServer"`
		Properties struct {
			LoginServer string `json:"loginServer"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("az acr create output: %v\n%s", err, out)
	}
	if created.Name != name {
		t.Fatalf("created name = %q, want %q", created.Name, name)
	}

	out = mustAz("acr", "show", "--resource-group", rg, "--name", name, "-o", "json")
	if !bytes.Contains(out, []byte(name)) {
		t.Fatalf("az acr show output missing %q:\n%s", name, out)
	}
	mustAz("acr", "delete", "--resource-group", rg, "--name", name, "--yes")
}

const terraformACRConfig = `
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
  subscription_id = "%[1]s"
  tenant_id       = "%[2]s"
  client_id       = "%[3]s"
  client_secret   = "%[4]s"
  metadata_host   = "%[5]s"
}

resource "azurerm_resource_group" "rg" {
  name     = "shim-acr-tf-rg"
  location = "eastus"
}

resource "azurerm_container_registry" "acr" {
  name                = "tfacr"
  resource_group_name = azurerm_resource_group.rg.name
  location            = azurerm_resource_group.rg.location
  sku                 = "Basic"
  admin_enabled       = false
}
`

func TestTerraform_AzureACR_RegistryHostLifecycle(t *testing.T) {
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	s := newACRAzureSession(t)
	dir := filepath.Join(s.dir, "tf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir tf dir: %v", err)
	}
	hcl := fmt.Sprintf(terraformACRConfig, acrSubscriptionID, acrTenantID, acrClientID, acrClientSecret, s.shim.Listener.Addr().String())
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	cacheDir := filepath.Join(dir, ".terraform-plugin-cache")
	_ = os.MkdirAll(cacheDir, 0o755)

	runTF := func(args ...string) ([]byte, []byte, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, tfBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1",
			"TF_INPUT=0",
			"CHECKPOINT_DISABLE=1",
			"TF_PLUGIN_CACHE_DIR="+cacheDir,
			"SSL_CERT_FILE="+s.caPath,
			"REQUESTS_CA_BUNDLE="+s.caPath,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		return stdout.Bytes(), stderr.Bytes(), cmd.Run()
	}
	if stdout, stderr, err := runTF("init", "-no-color"); err != nil {
		t.Fatalf("terraform init\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	stdout, stderr, err := runTF("apply", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform apply\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	if !strings.Contains(string(stdout), "azurerm_container_registry.acr: Creation complete") {
		t.Fatalf("terraform apply missing ACR creation:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	stdout, stderr, err = runTF("destroy", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform destroy\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
}

func findSystemCABundleForACR() string {
	for _, p := range []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/pki/tls/certs/ca-bundle.crt",
		"/etc/ssl/ca-bundle.pem",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func fetchSockerlessACRJWKS(t *testing.T, port, tenantID string, certPEM []byte) *azurebearer.JWKS {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatalf("AppendCertsFromPEM: no certs parsed")
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool}, //nolint:gosec
	}}
	u := fmt.Sprintf("https://localhost:%s/%s/discovery/v2.0/keys", port, tenantID)
	resp, err := client.Get(u)
	if err != nil {
		t.Fatalf("GET JWKS: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET JWKS status = %d body=%s", resp.StatusCode, body)
	}
	var jwks azurebearer.JWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		t.Fatalf("decode JWKS: %v", err)
	}
	return &jwks
}
