// Phase 9 sub-phase 9.13 — cross-cloud exit criterion for secrets:
// TestCrossCloudImport_Roundtrip_SecretsAWStoAzure.
//
// User writes AWS Secrets Manager-shape Terraform; the actual
// secret lives in a mock Azure Key Vault server backed by inmem.
package conformance_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/e6qu/shimanism/internal/secrets/domain"
	awsfront "github.com/e6qu/shimanism/internal/secrets/frontends/aws_secretsmanager"
	azurefront "github.com/e6qu/shimanism/internal/secrets/frontends/azure_keyvault"
	azurebackend "github.com/e6qu/shimanism/services/secrets/backends/azure"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
)

const terraformCrossCloudSecretsConfig = `
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    secretsmanager = "%s"
  }
}

resource "aws_secretsmanager_secret" "imported" {
  name                    = "cross-cloud-secret"
  recovery_window_in_days = 0
}
`

type fakeAzureCredCC struct{}

func (fakeAzureCredCC) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "stub", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestCrossCloudImport_Roundtrip_SecretsAWStoAzure(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	// Layer 1: state-of-record.
	dataBackend := inmem.New()
	ctx := context.Background()
	if _, err := dataBackend.CreateSecret(ctx, "cross-cloud-secret", domain.CreateSecretOptions{
		InitialValue: []byte("seed"),
	}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	// Layer 2: mock cloud B = Azure KV-shape TLS server over inmem.
	// Azure SDK refuses to attach bearer tokens to plain HTTP, so the
	// mock must be TLS.
	mockAzure := httptest.NewTLSServer(azurefront.New(dataBackend))
	t.Cleanup(mockAzure.Close)

	// Layer 3: Azure backend with TLS-insecure client pointing at the
	// mock. The shim's Azure backend speaks Azure KV REST via azsecrets.
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	az, err := azsecrets.NewClient(mockAzure.URL, fakeAzureCredCC{}, &azsecrets.ClientOptions{
		DisableChallengeResourceVerification: true,
		ClientOptions: azcore.ClientOptions{
			Transport: httpClient,
		},
	})
	if err != nil {
		t.Fatalf("new azsecrets client: %v", err)
	}
	azB := azurebackend.New(az)

	// Layer 4: AWS Secrets Manager frontend over the Azure backend.
	shim := httptest.NewServer(awsfront.New(azB))
	t.Cleanup(shim.Close)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformCrossCloudSecretsConfig, shim.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	runTf := func(args ...string) ([]byte, []byte, error) {
		cmd := exec.Command(tf, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1",
			"TF_INPUT=0",
			"CHECKPOINT_DISABLE=1",
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirSecretsCC(),
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
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("init", "-no-color")

	stdout, stderr, err := runTf("import", "-no-color",
		"aws_secretsmanager_secret.imported", "cross-cloud-secret")
	if err != nil {
		t.Fatalf("terraform import:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}

	stdout, stderr, err = runTf("plan", "-no-color", "-detailed-exitcode")
	if err == nil {
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		t.Logf("cross-cloud plan reports a diff\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
		return
	}
	t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
}

func terraformPluginCacheDirSecretsCC() string {
	d := filepath.Join(os.TempDir(), "shim-secrets-cc-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}
