// Conformance: Azure Application Gateway lifecycle exercised by the
// official `hashicorp/azurerm` Terraform provider.
//
// Full end-to-end Terraform apply requires:
//   - terraform binary on PATH
//   - SOCKERLESS_AZURE_TLS_PORT set (sockerless Entra stub reachable)
//   - SOCKERLESS_AZURE_TLS_CERT set (PEM path for sockerless's TLS cert)
//   - A system CA bundle present (Linux-only via SSL_CERT_FILE)
//
// Without sockerless the test is skipped with a clear diagnostic.
package conformance_test

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/harness"
	azurelbfront "github.com/e6qu/shimanism/internal/loadbalancer/frontends/azure_lb"
	"github.com/e6qu/shimanism/services/loadbalancer/backends/inmem"
)

func lbShimHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host
}

// TestTerraform_AzureAppGateway_Lifecycle creates an azurerm_application_gateway
// resource via the hashicorp/azurerm provider pointed at the shim's azure_lb
// frontend. Microsoft.Network/applicationGateways go to the shim; resource
// groups, VNet, and subnets pass through to sockerless.
func TestTerraform_AzureAppGateway_Lifecycle(t *testing.T) {
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	azureTLSPort := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if azureTLSPort == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set — Azure Terraform tests require sockerless Entra stub; skipping")
	}
	sockCertPath := os.Getenv("SOCKERLESS_AZURE_TLS_CERT")
	if sockCertPath == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set")
	}
	sockCertPEM, err := os.ReadFile(sockCertPath)
	if err != nil {
		t.Fatalf("read sockerless cert: %v", err)
	}
	systemCA := findSystemCABundleForLB()
	if systemCA == "" {
		t.Skip("no system CA bundle found — SSL_CERT_FILE workaround requires Linux")
	}

	const (
		subscriptionID = "00000000-0000-0000-0000-000000000001"
		tenantID       = "00000000-0000-0000-0000-000000000000"
		clientID       = "00000000-0000-0000-0000-000000000000"
	)

	sockerlessARM, err := url.Parse("https://localhost:" + azureTLSPort)
	if err != nil {
		t.Fatalf("parse sockerless URL: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(sockCertPEM) {
		t.Fatalf("AppendCertsFromPEM: no certs parsed")
	}
	proxy := httputil.NewSingleHostReverseProxy(sockerlessARM)
	proxy.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}} //nolint:gosec

	jwks := fetchSockerlessLBJWKS(t, azureTLSPort, tenantID, sockCertPEM)

	shim := harness.StartLoadBalancerServerAzureWithConfig(t, inmem.New(), azurelbfront.Config{
		Passthrough:      proxy,
		MetadataLoginURL: sockerlessARM.String(),
		BearerOptions: azurebearer.Options{
			Issuer: fmt.Sprintf("https://sts.windows.net/%s/", tenantID),
			JWKS:   jwks,
		},
	})

	dir := t.TempDir()
	systemBytes, err := os.ReadFile(systemCA)
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

	cacheDir := filepath.Join(dir, ".terraform-plugin-cache")
	_ = os.MkdirAll(cacheDir, 0o755)

	runTf := func(args ...string) {
		t.Helper()
		cmd := exec.Command(tfBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1",
			"TF_PLUGIN_CACHE_DIR="+cacheDir,
			"SSL_CERT_FILE="+combinedPath,
		)
		var outBuf, errBuf strings.Builder
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), outBuf.String(), errBuf.String(), err)
		}
	}

	metaHost := lbShimHost(shim.URL)
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

resource "azurerm_resource_group" "shim" {
  name     = "shim-lb-tf-rg"
  location = "East US"
}

resource "azurerm_virtual_network" "shim" {
  name                = "shim-lb-vnet"
  location            = azurerm_resource_group.shim.location
  resource_group_name = azurerm_resource_group.shim.name
  address_space       = ["10.0.0.0/16"]
}

resource "azurerm_subnet" "shim" {
  name                 = "shim-lb-subnet"
  resource_group_name  = azurerm_resource_group.shim.name
  virtual_network_name = azurerm_virtual_network.shim.name
  address_prefixes     = ["10.0.1.0/24"]
}

resource "azurerm_application_gateway" "shim" {
  name                = "shim-appgw-tf"
  resource_group_name = azurerm_resource_group.shim.name
  location            = azurerm_resource_group.shim.location

  sku {
    name     = "Standard_v2"
    tier     = "Standard_v2"
    capacity = 2
  }

  gateway_ip_configuration {
    name      = "shimgwip"
    subnet_id = azurerm_subnet.shim.id
  }

  frontend_port {
    name = "shimfep"
    port = 80
  }

  frontend_ip_configuration {
    name                          = "shimfeip"
    subnet_id                     = azurerm_subnet.shim.id
    private_ip_address_allocation = "Dynamic"
  }

  backend_address_pool { name = "shim-pool" }

  backend_http_settings {
    name                  = "shim-settings"
    cookie_based_affinity = "Disabled"
    path                  = "/"
    port                  = 80
    protocol              = "Http"
    request_timeout       = 60
  }

  http_listener {
    name                           = "shim-listener"
    frontend_ip_configuration_name = "shimfeip"
    frontend_port_name             = "shimfep"
    protocol                       = "Http"
  }

  request_routing_rule {
    name                       = "shim-rule"
    rule_type                  = "Basic"
    http_listener_name         = "shim-listener"
    backend_address_pool_name  = "shim-pool"
    backend_http_settings_name = "shim-settings"
    priority                   = 100
  }
}
`, metaHost, subscriptionID, tenantID, clientID)

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	runTf("init", "-no-color")
	runTf("apply", "-auto-approve", "-no-color")
	runTf("destroy", "-auto-approve", "-no-color")
}
