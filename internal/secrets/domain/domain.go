// Package domain holds shimanism's neutral secrets interface and
// types. The interface is the lingua franca between three frontend
// protocols (AWS Secrets Manager, GCP Secret Manager, Azure Key
// Vault) and four backends (AWS Secrets Manager, GCP Secret Manager,
// Azure Key Vault, Vault as K8s peer); each frontend translates its
// wire types into this domain, each backend translates this domain
// into its cloud's native API.
//
// The shim is stateless ([AGENTS.md § The shim is stateless](../../../AGENTS.md#the-shim-is-stateless)):
// no mapping table lives in the shim. Cross-cloud version mappings —
// notably translating the AWS `VersionId` UUID or Azure GUID to the
// monotonic uint64 surface this interface exposes — are derived per
// request by listing versions and sorting by creation timestamp.
//
// See services/secrets/OPERATIONS.md for the intersection-set
// rationale and per-cloud mapping.
package domain

import (
	"context"
	"time"
)

// Secret describes a stored secret's metadata (without the value).
type Secret struct {
	Name        string
	Description string
	Tags        map[string]string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// Enabled flag — only the AWS + Azure frontends meaningfully toggle
	// this; GCP + Vault adapters always report true and reject
	// disabled writes with InvalidArgument.
	Enabled bool
	// CurrentVersion is the monotonic version number of the latest
	// value. Increments by 1 per PutSecretValue. Zero means no
	// version exists yet (a freshly-created secret with no initial
	// value, where permitted by the backend).
	CurrentVersion uint64
}

// SecretValue is one specific version of a secret. Value is the raw
// bytes; multi-field secrets (Vault) round-trip as canonical JSON
// in this field, encoded/decoded by the Vault adapter on either
// end. Binary secrets through Azure (which has no native binary
// type) are base64-encoded by the Azure adapter; the encoding flag
// rides on the secret's `shim-binary=true` tag in Azure itself, not
// in the shim.
type SecretValue struct {
	Name      string
	Version   uint64
	Value     []byte
	CreatedAt time.Time
}

// Version is the metadata for one version of a secret (no value).
type Version struct {
	Number    uint64
	CreatedAt time.Time
}

// CreateSecretOptions controls CreateSecret. If InitialValue is nil
// the secret is created with no version (some backends — Vault — do
// not support this; their adapter returns InvalidArgument).
type CreateSecretOptions struct {
	Description  string
	Tags         map[string]string
	InitialValue []byte
}

// CreateSecretResult.
type CreateSecretResult struct {
	// Version is 1 when an initial value was provided, 0 otherwise.
	Version uint64
}

// PutSecretValueResult is the new-version response.
type PutSecretValueResult struct {
	Version uint64
}

// ListSecretsOptions controls ListSecrets pagination + filtering.
type ListSecretsOptions struct {
	Prefix     string
	MaxResults int
	NextToken  string
}

// ListSecretsResult is the ListSecrets response.
type ListSecretsResult struct {
	Secrets   []Secret
	NextToken string
}

// Secrets is the interface every secrets backend implements.
// Implementations must be safe for concurrent use across goroutines.
type Secrets interface {
	// CreateSecret creates a new named secret. Returns
	// SecretAlreadyExists if a secret with that name exists.
	CreateSecret(ctx context.Context, name string, opt CreateSecretOptions) (CreateSecretResult, error)

	// GetSecretValue returns one version of a secret's value. version=0
	// means "the latest version". Returns NoSuchSecret if the secret
	// doesn't exist, NoSuchVersion if the named version doesn't.
	GetSecretValue(ctx context.Context, name string, version uint64) (SecretValue, error)

	// PutSecretValue appends a new value version to a secret. Returns
	// the new monotonic version number. Returns NoSuchSecret if the
	// secret doesn't exist.
	PutSecretValue(ctx context.Context, name string, value []byte) (PutSecretValueResult, error)

	// DeleteSecret removes a secret. With force=false the backend's
	// soft-delete path is used (AWS recovery window, Azure soft-delete,
	// Vault versioned delete); GCP has no soft-delete and treats both
	// force values as hard delete. With force=true every backend
	// performs a hard delete. Returns NoSuchSecret if the secret
	// doesn't exist.
	DeleteSecret(ctx context.Context, name string, force bool) error

	// HeadSecret returns a secret's metadata without its value.
	// Returns NoSuchSecret if the secret doesn't exist.
	HeadSecret(ctx context.Context, name string) (Secret, error)

	// ListSecrets lists secrets, optionally filtered by name prefix.
	ListSecrets(ctx context.Context, opt ListSecretsOptions) (ListSecretsResult, error)

	// ListVersions lists every version of one secret in ascending
	// version-number order. Returns NoSuchSecret if the secret
	// doesn't exist.
	ListVersions(ctx context.Context, name string) ([]Version, error)
}
