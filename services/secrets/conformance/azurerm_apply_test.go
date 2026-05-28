// Phase 14.E.5: through-shim `azurerm_key_vault` Terraform Apply.
// Mirror of services/storage/conformance/azurerm_apply_test.go:
//
//	azurerm → mock-AAD (token) → shim KV ARM (Microsoft.KeyVault/vaults)
//	  → shim KV data plane (azure_keyvault) → backend.
//
// This is the second un-skipped azurerm Terraform test. The pattern
// generalizes: mock-AAD + ARM-frontend-with-Track* + data-plane URI
// propagation via the synthetic ARM resource.
package conformance_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/internal/secrets/domain"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
)

const terraformAzureKeyVaultApplyConfig = `
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
  metadata_host                   = "%s"
  subscription_id                 = "00000000-0000-0000-0000-000000000000"
  tenant_id                       = "00000000-0000-0000-0000-000000000000"
  client_id                       = "00000000-0000-0000-0000-000000000000"
  client_secret                   = "test-secret-do-not-use-in-prod"
  resource_provider_registrations = "none"
}

resource "azurerm_key_vault" "kv" {
  name                = "shimkv-apply"
  resource_group_name = "shim-rg"
  location            = "eastus"
  tenant_id           = "00000000-0000-0000-0000-000000000000"
  sku_name            = "standard"
}

resource "azurerm_key_vault_secret" "secret" {
  name         = "shim-applied-secret"
  value        = "shim-applied-value"
  key_vault_id = azurerm_key_vault.kv.id
}
`

// findSystemCABundleSecrets mirrors the storage-package helper of
// the same name. Returns "" on macOS (Security framework, no
// Unix-style trust bundle).
func findSystemCABundleSecrets() string {
	for _, p := range []string{
		"/etc/ssl/certs/ca-certificates.crt", // Debian/Ubuntu/Alpine
		"/etc/pki/tls/certs/ca-bundle.crt",   // RHEL/CentOS
		"/etc/ssl/ca-bundle.pem",             // OpenSUSE
		"/etc/pki/tls/cacert.pem",            // OpenELEC
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func TestCrossCloudApply_Roundtrip_KeyVaultAzureToAWS(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")
	systemCABundle := findSystemCABundleSecrets()
	if systemCABundle == "" {
		t.Skip("no system CA bundle found at a known Unix path — SSL_CERT_FILE workaround requires Linux")
	}

	ctx := context.Background()
	backend := inmem.New()

	kvShim := harness.StartSecretsServerAzure(t, backend)
	armShim := harness.StartSecretsServerAzureARM(t, kvShim.URL)
	aad := harness.StartMockAAD(t, armShim.URL)

	metadataHost := strings.TrimPrefix(aad.URL, "https://")

	hcl := fmt.Sprintf(terraformAzureKeyVaultApplyConfig, metadataHost)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	combinedCA := filepath.Join(dir, "combined-ca.pem")
	systemBytes, err := os.ReadFile(systemCABundle)
	if err != nil {
		t.Fatalf("read system CA bundle %s: %v", systemCABundle, err)
	}
	mockCABytes, err := os.ReadFile(aad.CertFile)
	if err != nil {
		t.Fatalf("read mock-AAD cert: %v", err)
	}
	// kvShim is HTTPS too; include its cert in the bundle so the
	// Terraform subprocess accepts its TLS handshake when azurerm
	// follows vaultUri to the data plane.
	kvCABytes, err := os.ReadFile(kvShim.CertFile)
	if err != nil {
		t.Fatalf("read kv-shim cert: %v", err)
	}
	combined := append(append(append(systemBytes, '\n'), mockCABytes...), kvCABytes...)
	if err := os.WriteFile(combinedCA, combined, 0o644); err != nil {
		t.Fatalf("write combined CA: %v", err)
	}

	runTf := func(args ...string) ([]byte, []byte, error) {
		cmd := exec.Command(tf, args...)
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
	mustRun("apply", "-no-color", "-auto-approve")
	t.Cleanup(func() {
		_, _, _ = runTf("destroy", "-no-color", "-auto-approve")
	})

	// Verify the secret landed in the backend.
	list, err := backend.ListSecrets(ctx, domain.ListSecretsOptions{})
	if err != nil {
		t.Fatalf("backend.ListSecrets: %v", err)
	}
	found := false
	for _, s := range list.Secrets {
		if s.Name == "shim-applied-secret" {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(list.Secrets))
		for _, s := range list.Secrets {
			names = append(names, s.Name)
		}
		t.Errorf("backend.ListSecrets did not contain `shim-applied-secret`; got %v", names)
	}
}
