// Package conformance hosts the backend factories used by the
// parameterised conformance tests in this directory. Tests live in
// `package conformance_test` (external tests) and import the
// exported factory set from here.
//
// Each factory is named the same as the backend it produces; a
// per-PR conformance lane picks one factory at a time, controlled
// via env vars so CI can light up each backend in its own job
// without modifying the test source.
package conformance

import (
	"context"
	"os"
	"testing"

	smapi "cloud.google.com/go/secretmanager/apiv1"
	azcore "github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	vaultapi "github.com/hashicorp/vault/api"

	"github.com/e6qu/shimanism/internal/secrets/domain"
	awsbackend "github.com/e6qu/shimanism/services/secrets/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/secrets/backends/azure"
	gcpbackend "github.com/e6qu/shimanism/services/secrets/backends/gcp"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
	vaultbackend "github.com/e6qu/shimanism/services/secrets/backends/vault"
)

// BackendFactory returns a Secrets backend ready for use. Each
// factory may call t.Skip if its required infrastructure
// (Docker container, env var, real cloud account) isn't available.
type BackendFactory struct {
	Name string
	Fn   func(t *testing.T) domain.Secrets
}

// ActiveBackends returns the set of backend factories to drive the
// conformance suite against. Lists every backend; each factory
// internally decides whether to skip.
func ActiveBackends() []BackendFactory {
	return []BackendFactory{
		{Name: "inmem", Fn: NewInmem},
		{Name: "vault", Fn: NewVault},
		{Name: "aws", Fn: NewAWS},
		{Name: "gcp", Fn: NewGCP},
		{Name: "azure", Fn: NewAzure},
	}
}

// NewInmem is always available — no external dependencies.
func NewInmem(t *testing.T) domain.Secrets {
	t.Helper()
	return inmem.New()
}

// NewVault connects to a Vault server using VAULT_ADDR + VAULT_TOKEN
// env vars. Skipped if VAULT_ADDR is not set. CI starts a Vault dev
// container as a step + sets the env vars.
func NewVault(t *testing.T) domain.Secrets {
	t.Helper()
	if os.Getenv("VAULT_ADDR") == "" {
		t.Skip("VAULT_ADDR not set (Vault backend conformance disabled)")
	}
	c, err := vaultapi.NewClient(vaultapi.DefaultConfig())
	if err != nil {
		t.Fatalf("new vault client: %v", err)
	}
	mount := os.Getenv("VAULT_KV_MOUNT")
	if mount == "" {
		mount = "secret"
	}
	return vaultbackend.New(c, vaultbackend.Config{Mount: mount})
}

// NewAWS connects to real AWS Secrets Manager when
// AWS_SECRETSMANAGER_CONFORMANCE=1 is set. CI installs no emulator
// for AWS Secrets Manager; the AWS lane lights up only with real
// cloud credentials (Track A).
func NewAWS(t *testing.T) domain.Secrets {
	t.Helper()
	if os.Getenv("AWS_SECRETSMANAGER_CONFORMANCE") != "1" {
		t.Skip("AWS_SECRETSMANAGER_CONFORMANCE!=1 (AWS Secrets Manager backend conformance disabled)")
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	opts := []func(*awssm.Options){}
	if endpoint := os.Getenv("AWS_SECRETSMANAGER_ENDPOINT"); endpoint != "" {
		opts = append(opts, func(o *awssm.Options) {
			o.BaseEndpoint = awsapi.String(endpoint)
		})
	}
	return awsbackend.New(awssm.NewFromConfig(cfg, opts...))
}

// NewGCP connects to real GCP Secret Manager when GCP_SECRETMANAGER_
// CONFORMANCE=1 is set. CI installs no first-party GCP Secret
// Manager emulator; this lane awaits Track A.
func NewGCP(t *testing.T) domain.Secrets {
	t.Helper()
	if os.Getenv("GCP_SECRETMANAGER_CONFORMANCE") != "1" {
		t.Skip("GCP_SECRETMANAGER_CONFORMANCE!=1 (GCP Secret Manager backend conformance disabled)")
	}
	project := os.Getenv("GCP_PROJECT_ID")
	if project == "" {
		t.Skip("GCP_PROJECT_ID not set")
	}
	c, err := smapi.NewClient(context.Background())
	if err != nil {
		t.Fatalf("new GCP secretmanager client: %v", err)
	}
	return gcpbackend.New(c, gcpbackend.Config{ProjectID: project})
}

// NewAzure connects to real Azure Key Vault when AZURE_KEYVAULT_URL
// is set. CI installs no first-party Azure Key Vault emulator; this
// lane awaits Track A.
func NewAzure(t *testing.T) domain.Secrets {
	t.Helper()
	if os.Getenv("AZURE_KEYVAULT_URL") == "" {
		t.Skip("AZURE_KEYVAULT_URL not set (Azure Key Vault backend conformance disabled)")
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("new Azure credential: %v", err)
	}
	c, err := azsecrets.NewClient(os.Getenv("AZURE_KEYVAULT_URL"), cred, nil)
	if err != nil {
		t.Fatalf("new azsecrets client: %v", err)
	}
	return azurebackend.New(c)
}

// silence unused-import warning when not all backends are wired.
var _ = azcore.ClientOptions{}
