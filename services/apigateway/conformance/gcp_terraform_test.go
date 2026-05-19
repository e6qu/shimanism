// Phase 8 conformance: GCP API Gateway-shaped frontend exercised
// by `hashicorp/google`. Uses the provider's per-service endpoint
// override `apigateway_custom_endpoint`.
package conformance_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

const terraformGCPAPIGWConfig = `
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project                      = "shim-project"
  region                       = "us-central1"
  apigateway_custom_endpoint   = "%s/v1/"
  credentials                  = file("creds.json")
}

resource "google_api_gateway_api" "tf" {
  provider = google
  api_id   = "tf-api"
}
`

func TestTerraform_GCPAPIGateway_ResourceLifecycle(t *testing.T) {
	t.Parallel()
	bin := requireTerraformAPIGW(t)
	srv := harness.StartAPIGatewayServerGCP(t, inmem.New())

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformGCPAPIGWConfig, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	// Minimal service-account JSON the provider accepts during plan.
	creds := []byte(`{"type":"service_account","client_email":"x@x.iam.gserviceaccount.com","private_key_id":"k","private_key":"-----BEGIN PRIVATE KEY-----\nMIIBVQIBADANBgkqhkiG9w0BAQEFAASCAT8wggE7AgEAAkEAxxxx\n-----END PRIVATE KEY-----\n","token_uri":"https://oauth2.googleapis.com/token"}`)
	if err := os.WriteFile(filepath.Join(dir, "creds.json"), creds, 0o600); err != nil {
		t.Fatalf("write creds.json: %v", err)
	}
	// Plan is the smoke test here — apply requires the provider's
	// Api → ApiConfig → Gateway chain plus OAuth signing the test
	// harness intentionally doesn't simulate.
	for _, step := range [][]string{
		{"init", "-no-color"},
		{"plan", "-no-color"},
	} {
		stdout, stderr, err := runTerraformAPIGW(t, dir, bin, step...)
		if err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(step, " "), stdout, stderr, err)
		}
	}
}
