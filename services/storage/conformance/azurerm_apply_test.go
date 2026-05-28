// Phase 14.E.4: through-shim `azurerm` Terraform Apply for
// storage. Exercises the full stack:
//
//	azurerm provider → mock-AAD (token exchange) → shim's ARM frontend
//	  (Microsoft.Storage/storageAccounts + BlobContainers) → shared
//	  backend → mock blob frontend → backend (via the data plane).
//
// This is the first un-skipped azurerm Terraform test in the repo.
// The previously-skipped TestTerraform_AzureBlob_ResourceLifecycle
// stays skipped (it dates from before the ARM frontend existed; the
// new cell uses the post-14.E.3 PrimaryEndpoints.Blob mechanism +
// the new mock-AAD).
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
	storagedomain "github.com/e6qu/shimanism/internal/storage/domain"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
)

// findSystemCABundle returns the path of the OS's installed CA
// bundle. Matches the search list Go's crypto/x509 uses on Unix
// (root_linux.go:certFiles); returns "" if none of them exist (e.g.
// macOS, which loads roots from the Security framework instead).
func findSystemCABundle() string {
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

const terraformAzureStorageApplyConfig = `
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
  skip_provider_registration      = true
  resource_provider_registrations = "none"
  storage_use_azuread             = false
}

resource "azurerm_storage_account" "sa" {
  name                     = "shimsa%s"
  resource_group_name      = "shim-rg"
  location                 = "eastus"
  account_tier             = "Standard"
  account_replication_type = "LRS"
}

resource "azurerm_storage_container" "sc" {
  name                  = "applied-container"
  storage_account_id    = azurerm_storage_account.sa.id
  container_access_type = "private"
}
`

func TestCrossCloudApply_Roundtrip_StorageAzureToAWS(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")
	// Go's TLS stack honors SSL_CERT_FILE on Linux to extend the
	// system trust store. macOS uses the Security framework
	// directly and ignores the env var — running the test there
	// would require installing the mock-AAD cert into the
	// Keychain. Skip when no system CA bundle is on a known Unix
	// path; CI runs on Linux.
	systemCABundle := findSystemCABundle()
	if systemCABundle == "" {
		t.Skip("no system CA bundle found at any of the standard Unix paths — SSL_CERT_FILE workaround requires Linux")
	}

	ctx := context.Background()
	backend := inmem.New()

	blobShim := harness.StartStorageServerAzureBlob(t, backend)
	armShim := harness.StartStorageServerAzureARM(t, backend, blobShim.URL)
	aad := harness.StartMockAAD(t, armShim.URL)

	metadataHost := strings.TrimPrefix(aad.URL, "https://")

	suffix := strings.ToLower(strings.ReplaceAll(t.Name(), "/", ""))
	if len(suffix) > 16 {
		suffix = suffix[len(suffix)-16:]
	}
	hcl := fmt.Sprintf(terraformAzureStorageApplyConfig, metadataHost, suffix)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	// Build a combined CA bundle: the system roots + our mock-AAD
	// cert. SSL_CERT_FILE replaces (not augments) Go's default
	// trust pool, so we have to include both: the system roots are
	// what `terraform init` needs to reach registry.terraform.io;
	// the test cert is what `terraform apply` needs to trust the
	// mock-AAD authority.
	combinedCA := filepath.Join(dir, "combined-ca.pem")
	systemBytes, err := os.ReadFile(systemCABundle)
	if err != nil {
		t.Fatalf("read system CA bundle %s: %v", systemCABundle, err)
	}
	mockCABytes, err := os.ReadFile(aad.CertFile)
	if err != nil {
		t.Fatalf("read mock-AAD cert: %v", err)
	}
	if err := os.WriteFile(combinedCA, append(append(systemBytes, '\n'), mockCABytes...), 0o644); err != nil {
		t.Fatalf("write combined CA: %v", err)
	}

	runTf := func(args ...string) ([]byte, []byte, error) {
		cmd := exec.Command(tf, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1",
			"TF_INPUT=0",
			"CHECKPOINT_DISABLE=1",
			// Combined CA bundle: system roots + mock-AAD self-
			// signed cert. Go's TLS stack reads SSL_CERT_FILE on
			// Linux at runtime; the value replaces the default
			// trust pool, so it must include both.
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

	// Cleanup: terraform destroy. The shim's account ARM is a no-op so
	// destroy should land without backend interaction; the container
	// destroy hits backend.DeleteBucket.
	t.Cleanup(func() {
		_, _, _ = runTf("destroy", "-no-color", "-auto-approve")
	})

	// Verify the container actually landed in the backend.
	list, err := backend.ListBuckets(ctx, storagedomain.ListBucketsOptions{})
	if err != nil {
		t.Fatalf("backend.ListBuckets: %v", err)
	}
	found := false
	for _, b := range list.Buckets {
		if b.Name == "applied-container" {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(list.Buckets))
		for _, b := range list.Buckets {
			names = append(names, b.Name)
		}
		t.Errorf("backend.ListBuckets did not contain `applied-container`; got %v", names)
	}
}
