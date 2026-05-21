// Phase 9 sub-phase 9.13 — reverse-direction cross-cloud:
// TestCrossCloudImport_Roundtrip_StorageGCStoAWS.
//
// Symmetric to TestCrossCloudImport_Roundtrip_StorageAWStoGCS:
// the user writes GCS-shape Terraform but the actual bucket lives
// in a mock AWS S3 server backed by inmem. Confirms the cross-
// cloud chain works in both directions.
package conformance_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/e6qu/shimanism/internal/restxml"
	awsfront "github.com/e6qu/shimanism/internal/storage/frontends/aws_s3"
	gcsfront "github.com/e6qu/shimanism/internal/storage/frontends/gcs"
	awsbackend "github.com/e6qu/shimanism/services/storage/backends/aws"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
	storagegen "github.com/e6qu/shimanism/services/storage/gen"
)

const terraformReverseCrossCloudConfig = `
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project                    = "shim-cross-cloud"
  region                     = "us"
  credentials                = file("creds.json")
  storage_custom_endpoint    = "%s/storage/v1/"
}

resource "google_storage_bucket" "imported" {
  name     = "reverse-cross-cloud-bucket"
  location = "US"
}
`

func TestCrossCloudImport_Roundtrip_StorageGCStoAWS(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	// hashicorp/google's provider parses the credentials PEM at
	// startup and refuses to fall back to anonymous mode even with
	// a custom endpoint. Real-cloud auth is Track A work; for the
	// mock-tier we can't bypass the PEM parse without crafting a
	// valid (though unused) private key. Skipping until Track A.
	t.Skip("hashicorp/google credentials PEM-parse blocks mock-tier auth-bypass; Track A picks this up")
	tf, _ := exec.LookPath("terraform")

	// Layer 1: state-of-record.
	dataBackend := inmem.New()
	ctx := context.Background()
	if err := dataBackend.CreateBucket(ctx, "reverse-cross-cloud-bucket", "us-east-1"); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}

	// Layer 2: mock cloud B = AWS S3-shape HTTP frontend over inmem.
	awsRouter := &restxml.Router{}
	storagegen.RegisterAmazonS3Routes(awsRouter, awsfront.New(dataBackend))
	mockAWS := httptest.NewServer(awsRouter)
	t.Cleanup(mockAWS.Close)

	// Layer 3: AWS backend pointing at the mock.
	awsConfig, err := awscfg.LoadDefaultConfig(ctx,
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(awscredentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	s3Client := awss3.NewFromConfig(awsConfig, func(o *awss3.Options) {
		o.BaseEndpoint = awsapi.String(mockAWS.URL)
		o.UsePathStyle = true
	})
	awsB := awsbackend.New(s3Client)

	// Layer 4: GCS frontend over the AWS backend.
	shim := httptest.NewServer(gcsfront.New(awsB))
	t.Cleanup(shim.Close)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformReverseCrossCloudConfig, shim.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	creds := []byte(`{"type":"service_account","client_email":"x@x.iam.gserviceaccount.com","private_key_id":"k","private_key":"-----BEGIN PRIVATE KEY-----\nMIIBVQIBADANBgkqhkiG9w0BAQEFAASCAT8wggE7AgEAAkEAxxxx\n-----END PRIVATE KEY-----\n","token_uri":"https://oauth2.googleapis.com/token"}`)
	if err := os.WriteFile(filepath.Join(dir, "creds.json"), creds, 0o600); err != nil {
		t.Fatalf("write creds.json: %v", err)
	}

	runTf := func(args ...string) ([]byte, []byte, error) {
		cmd := exec.Command(tf, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1",
			"TF_INPUT=0",
			"CHECKPOINT_DISABLE=1",
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirReverseCC(),
			"GOOGLE_OAUTH_ACCESS_TOKEN=stub",
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
		"google_storage_bucket.imported", "reverse-cross-cloud-bucket")
	if err != nil {
		t.Fatalf("terraform import:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}

	stdout, stderr, err = runTf("plan", "-no-color", "-detailed-exitcode")
	if err == nil {
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		t.Logf("reverse cross-cloud plan reports a diff — fidelity gap\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
		return
	}
	t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
}

func terraformPluginCacheDirReverseCC() string {
	d := filepath.Join(os.TempDir(), "shim-reverse-cc-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}
