// Secrets subcommand wiring. Mirrors the storage subcommand shape:
// pick a frontend (wire protocol) and a backend (destination),
// listen on -addr, serve forever.

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	azsecretsapi "github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	smcfg "github.com/aws/aws-sdk-go-v2/config"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smapi "cloud.google.com/go/secretmanager/apiv1"
	vaultapi "github.com/hashicorp/vault/api"

	"github.com/e6qu/shimanism/internal/secrets/domain"
	awssmfront "github.com/e6qu/shimanism/internal/secrets/frontends/aws_secretsmanager"
	azurekvfront "github.com/e6qu/shimanism/internal/secrets/frontends/azure_keyvault"
	gcpsmfront "github.com/e6qu/shimanism/internal/secrets/frontends/gcp_secretmanager"
	awssmbackend "github.com/e6qu/shimanism/services/secrets/backends/aws"
	azuresmbackend "github.com/e6qu/shimanism/services/secrets/backends/azure"
	gcpsmbackend "github.com/e6qu/shimanism/services/secrets/backends/gcp"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
	vaultbackend "github.com/e6qu/shimanism/services/secrets/backends/vault"
)

func runSecrets(args []string) error {
	fs := flag.NewFlagSet("secrets", flag.ContinueOnError)
	addr := fs.String("addr", ":9100", "address to listen on")
	frontendName := fs.String("frontend", "aws_secretsmanager",
		"frontend wire protocol: aws_secretsmanager, gcp_secretmanager, azure_keyvault")
	backendName := fs.String("backend", "inmem",
		"backend: inmem, vault, aws, gcp, azure")

	// Backend connection knobs.
	vaultAddr := fs.String("vault-addr", envOr("VAULT_ADDR", ""), "Vault server address")
	vaultMount := fs.String("vault-mount", envOr("VAULT_KV_MOUNT", "secret"), "Vault KV v2 mount path")
	awsEndpoint := fs.String("aws-endpoint", envOr("AWS_SECRETSMANAGER_ENDPOINT", ""),
		"AWS Secrets Manager endpoint override (empty = default)")
	gcpProject := fs.String("gcp-project", envOr("GCP_PROJECT_ID", ""),
		"GCP project ID (for the gcp backend)")
	azureVaultURL := fs.String("azure-vault-url", envOr("AZURE_KEYVAULT_URL", ""),
		"Azure Key Vault URL (e.g. https://<name>.vault.azure.net)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	backend, err := buildSecretsBackend(*backendName, secretsBackendConfig{
		vaultAddr:     *vaultAddr,
		vaultMount:    *vaultMount,
		awsEndpoint:   *awsEndpoint,
		gcpProject:    *gcpProject,
		azureVaultURL: *azureVaultURL,
	})
	if err != nil {
		return err
	}

	handler, err := buildSecretsFrontend(*frontendName, backend)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "shim secrets: frontend=%s backend=%s addr=%s\n",
		*frontendName, *backendName, *addr)
	return http.ListenAndServe(*addr, handler)
}

type secretsBackendConfig struct {
	vaultAddr     string
	vaultMount    string
	awsEndpoint   string
	gcpProject    string
	azureVaultURL string
}

func buildSecretsBackend(name string, cfg secretsBackendConfig) (domain.Secrets, error) {
	switch name {
	case "inmem":
		return inmem.New(), nil
	case "vault":
		if cfg.vaultAddr == "" {
			return nil, fmt.Errorf("vault backend requires -vault-addr (or VAULT_ADDR)")
		}
		vc, err := vaultapi.NewClient(vaultapi.DefaultConfig())
		if err != nil {
			return nil, fmt.Errorf("new vault client: %w", err)
		}
		// vault.DefaultConfig respects VAULT_ADDR; for the flag form
		// also accept setting via the explicit -vault-addr value.
		if err := vc.SetAddress(cfg.vaultAddr); err != nil {
			return nil, fmt.Errorf("set vault address: %w", err)
		}
		return vaultbackend.New(vc, vaultbackend.Config{Mount: cfg.vaultMount}), nil
	case "aws":
		awsCfg, err := smcfg.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		opts := []func(*awssm.Options){}
		if cfg.awsEndpoint != "" {
			opts = append(opts, func(o *awssm.Options) {
				o.BaseEndpoint = awsapi.String(cfg.awsEndpoint)
			})
		}
		return awssmbackend.New(awssm.NewFromConfig(awsCfg, opts...)), nil
	case "gcp":
		if cfg.gcpProject == "" {
			return nil, fmt.Errorf("gcp backend requires -gcp-project (or GCP_PROJECT_ID)")
		}
		c, err := smapi.NewClient(context.Background())
		if err != nil {
			return nil, fmt.Errorf("connect to GCP Secret Manager: %w", err)
		}
		return gcpsmbackend.New(c, gcpsmbackend.Config{ProjectID: cfg.gcpProject}), nil
	case "azure":
		if cfg.azureVaultURL == "" {
			return nil, fmt.Errorf("azure backend requires -azure-vault-url (or AZURE_KEYVAULT_URL)")
		}
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("azure credential chain: %w", err)
		}
		c, err := azsecretsapi.NewClient(cfg.azureVaultURL, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("connect to Azure Key Vault: %w", err)
		}
		return azuresmbackend.New(c), nil
	default:
		return nil, fmt.Errorf("unknown backend %q (valid: inmem, vault, aws, gcp, azure)", name)
	}
}

func buildSecretsFrontend(name string, backend domain.Secrets) (http.Handler, error) {
	switch name {
	case "aws_secretsmanager":
		return awssmfront.New(backend), nil
	case "gcp_secretmanager":
		return gcpsmfront.New(backend), nil
	case "azure_keyvault":
		return azurekvfront.New(backend), nil
	default:
		return nil, fmt.Errorf("unknown frontend %q (valid: aws_secretsmanager, gcp_secretmanager, azure_keyvault)", name)
	}
}
