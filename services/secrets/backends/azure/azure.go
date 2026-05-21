// Package azure is the Azure Key Vault secrets-surface backend for
// shimanism's secrets service. It uses
// azure-sdk-for-go/sdk/security/keyvault/azsecrets (MIT).
//
// Version mapping. Azure Key Vault assigns GUID version IDs. The
// domain uses monotonic uint64. The shim derives monotonic → GUID
// by listing the secret's versions and sorting by CreatedOn. No
// translation table lives in the shim — per
// AGENTS.md § The shim is stateless.
//
// Value type. Azure Key Vault stores secret values as UTF-8 strings;
// it does not natively support binary. Binary domain values flow
// through as the UTF-8 representation of the bytes, which works
// for ASCII content but loses fidelity for arbitrary binary. Phase
// 2 documents binary support as a known limitation on the Azure
// path; we may wire base64 + a per-secret tag flag later.
package azure

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azapi "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/e6qu/shimanism/internal/secrets/domain"
)

// Backend implements domain.Secrets via Azure Key Vault.
type Backend struct {
	c *azsecrets.Client
}

// New wraps an already-configured Azure Key Vault secrets client.
func New(c *azsecrets.Client) *Backend { return &Backend{c: c} }

var _ domain.Secrets = (*Backend)(nil)

func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		switch re.ErrorCode {
		case "SecretNotFound":
			return domain.NoSuchSecret(name)
		case "Conflict", "ObjectIsBeingDeleted":
			return domain.SecretBeingDeleted(name)
		case "BadParameter", "InvalidArgument":
			return domain.InvalidArgument(re.RawResponse.Status)
		}
		if re.StatusCode == 404 {
			return domain.NoSuchSecret(name)
		}
		if re.StatusCode == 409 {
			return domain.SecretAlreadyExists(name)
		}
	}
	return err
}

func (b *Backend) CreateSecret(ctx context.Context, name string, opt domain.CreateSecretOptions) (domain.CreateSecretResult, error) {
	// Azure has no atomic "create if absent" with separate value
	// step — SetSecret writes a value and creates the secret if
	// missing. To enforce strict create semantics we GetSecret first;
	// if it exists, return SecretAlreadyExists. Race-window is real
	// on a multi-replica setup; we accept the cost (per the
	// no-state rule we can't lease anything in the shim).
	if _, err := b.c.GetSecret(ctx, name, "", nil); err == nil {
		return domain.CreateSecretResult{}, domain.SecretAlreadyExists(name)
	}

	if opt.InitialValue == nil {
		// Azure can't create a secret without a value. Return
		// InvalidArgument with the source-cloud-shaped vocabulary.
		return domain.CreateSecretResult{}, domain.InvalidArgument(
			"Azure Key Vault requires an initial value when creating a secret")
	}
	val := string(opt.InitialValue)
	tags := azureTags(opt.Tags, opt.Description)
	_, err := b.c.SetSecret(ctx, name, azsecrets.SetSecretParameters{
		Value: &val,
		Tags:  tags,
	}, nil)
	if err != nil {
		return domain.CreateSecretResult{}, translateErr(err, name)
	}
	return domain.CreateSecretResult{Version: 1}, nil
}

func azureTags(tags map[string]string, description string) map[string]*string {
	if len(tags) == 0 && description == "" {
		return nil
	}
	out := make(map[string]*string, len(tags)+1)
	for k, v := range tags {
		v := v
		out[k] = &v
	}
	if description != "" {
		v := description
		out["shim-description"] = &v
	}
	return out
}

func (b *Backend) GetSecretValue(ctx context.Context, name string, version uint64) (domain.SecretValue, error) {
	azureVersion := "" // "" means current
	resolved := version
	if version != 0 {
		// Map monotonic → GUID by listing.
		guid, _, err := b.lookupVersion(ctx, name, version)
		if err != nil {
			return domain.SecretValue{}, err
		}
		azureVersion = guid
	}
	out, err := b.c.GetSecret(ctx, name, azureVersion, nil)
	if err != nil {
		return domain.SecretValue{}, translateErr(err, name)
	}
	val := ""
	if out.Value != nil {
		val = *out.Value
	}
	if version == 0 {
		// Compute monotonic for the response by listing.
		versions, err := b.listVersions(ctx, name)
		if err != nil {
			return domain.SecretValue{}, err
		}
		resolved = uint64(len(versions))
	}
	created := time.Time{}
	if out.Attributes != nil && out.Attributes.Created != nil {
		created = *out.Attributes.Created
	}
	return domain.SecretValue{
		Name:      name,
		Version:   resolved,
		Value:     []byte(val),
		CreatedAt: created,
	}, nil
}

func (b *Backend) PutSecretValue(ctx context.Context, name string, value []byte) (domain.PutSecretValueResult, error) {
	val := string(value)
	if _, err := b.c.SetSecret(ctx, name, azsecrets.SetSecretParameters{
		Value: &val,
	}, nil); err != nil {
		return domain.PutSecretValueResult{}, translateErr(err, name)
	}
	// Recover the new monotonic version by listing.
	versions, err := b.listVersions(ctx, name)
	if err != nil {
		return domain.PutSecretValueResult{}, err
	}
	return domain.PutSecretValueResult{Version: uint64(len(versions))}, nil
}

func (b *Backend) UpdateSecret(ctx context.Context, name string, opt domain.UpdateSecretOptions) error {
	// Azure Key Vault: UpdateSecretProperties patches the latest
	// version's metadata. Description has no direct Key Vault field —
	// the closest analogue is the contentType / tag store. shimanism's
	// cross-cloud convention stores description in a tag with key
	// "shim-description"; the Azure adapter round-trips that.
	params := azsecrets.UpdateSecretPropertiesParameters{}
	if opt.Tags != nil || opt.Description != nil {
		tags := map[string]*string{}
		if opt.Tags != nil {
			for k, v := range opt.Tags {
				v := v
				tags[k] = &v
			}
		}
		if opt.Description != nil {
			d := *opt.Description
			tags["shim-description"] = &d
		}
		params.Tags = tags
	}
	if opt.Enabled != nil {
		e := *opt.Enabled
		params.SecretAttributes = &azsecrets.SecretAttributes{Enabled: &e}
	}
	_, err := b.c.UpdateSecretProperties(ctx, name, "", params, nil)
	return translateErr(err, name)
}

func (b *Backend) DeleteSecret(ctx context.Context, name string, force bool) error {
	// Soft delete (default): DeleteSecret initiates the recovery
	// window. Force: follow with PurgeDeletedSecret.
	_, err := b.c.DeleteSecret(ctx, name, nil)
	if err != nil {
		return translateErr(err, name)
	}
	if force {
		// Poll briefly for the secret to land in the "deleted" state
		// before purging — Azure rejects purge of a not-yet-deleted
		// secret.
		deadline := time.Now().Add(15 * time.Second)
		var lastErr error
		for time.Now().Before(deadline) {
			_, perr := b.c.PurgeDeletedSecret(ctx, name, nil)
			if perr == nil {
				return nil
			}
			lastErr = perr
			time.Sleep(500 * time.Millisecond)
		}
		return translateErr(lastErr, name)
	}
	return nil
}

func (b *Backend) HeadSecret(ctx context.Context, name string) (domain.Secret, error) {
	out, err := b.c.GetSecret(ctx, name, "", nil)
	if err != nil {
		return domain.Secret{}, translateErr(err, name)
	}
	s := domain.Secret{
		Name:    name,
		Enabled: true,
	}
	if out.Attributes != nil {
		if out.Attributes.Created != nil {
			s.CreatedAt = *out.Attributes.Created
		}
		if out.Attributes.Updated != nil {
			s.UpdatedAt = *out.Attributes.Updated
		}
		if out.Attributes.Enabled != nil {
			s.Enabled = *out.Attributes.Enabled
		}
	}
	if len(out.Tags) > 0 {
		s.Tags = map[string]string{}
		for k, v := range out.Tags {
			if v == nil {
				continue
			}
			if k == "shim-description" {
				s.Description = *v
				continue
			}
			s.Tags[k] = *v
		}
		if len(s.Tags) == 0 {
			s.Tags = nil
		}
	}
	versions, err := b.listVersions(ctx, name)
	if err != nil {
		return domain.Secret{}, err
	}
	s.CurrentVersion = uint64(len(versions))
	return s, nil
}

func (b *Backend) ListSecrets(ctx context.Context, opt domain.ListSecretsOptions) (domain.ListSecretsResult, error) {
	pager := b.c.NewListSecretPropertiesPager(nil)
	res := domain.ListSecretsResult{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return domain.ListSecretsResult{}, translateErr(err, "")
		}
		for _, sp := range page.Value {
			if sp == nil || sp.ID == nil {
				continue
			}
			name := secretNameFromID(string(*sp.ID))
			if opt.Prefix != "" && !strings.HasPrefix(name, opt.Prefix) {
				continue
			}
			entry := domain.Secret{Name: name, Enabled: true}
			if sp.Attributes != nil {
				if sp.Attributes.Created != nil {
					entry.CreatedAt = *sp.Attributes.Created
				}
				if sp.Attributes.Updated != nil {
					entry.UpdatedAt = *sp.Attributes.Updated
				}
				if sp.Attributes.Enabled != nil {
					entry.Enabled = *sp.Attributes.Enabled
				}
			}
			if len(sp.Tags) > 0 {
				entry.Tags = map[string]string{}
				for k, v := range sp.Tags {
					if v == nil {
						continue
					}
					if k == "shim-description" {
						entry.Description = *v
						continue
					}
					entry.Tags[k] = *v
				}
				if len(entry.Tags) == 0 {
					entry.Tags = nil
				}
			}
			res.Secrets = append(res.Secrets, entry)
			if opt.MaxResults > 0 && len(res.Secrets) >= opt.MaxResults {
				return res, nil
			}
		}
	}
	return res, nil
}

func (b *Backend) ListVersions(ctx context.Context, name string) ([]domain.Version, error) {
	versions, err := b.listVersions(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Version, len(versions))
	for i, v := range versions {
		out[i] = domain.Version{Number: uint64(i + 1), CreatedAt: v.created}
	}
	return out, nil
}

// azureVersionInfo is the per-version record needed for monotonic
// numbering: GUID + creation timestamp.
type azureVersionInfo struct {
	guid    string
	created time.Time
}

// listVersions returns the secret's versions in ascending
// CreatedOn order — matching the monotonic ordering the domain
// expects.
func (b *Backend) listVersions(ctx context.Context, name string) ([]azureVersionInfo, error) {
	pager := b.c.NewListSecretPropertiesVersionsPager(name, nil)
	var versions []azureVersionInfo
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, translateErr(err, name)
		}
		for _, sp := range page.Value {
			if sp == nil || sp.ID == nil {
				continue
			}
			guid := versionFromID(string(*sp.ID))
			info := azureVersionInfo{guid: guid}
			if sp.Attributes != nil && sp.Attributes.Created != nil {
				info.created = *sp.Attributes.Created
			}
			versions = append(versions, info)
		}
	}
	sort.SliceStable(versions, func(i, j int) bool {
		return versions[i].created.Before(versions[j].created)
	})
	return versions, nil
}

func (b *Backend) lookupVersion(ctx context.Context, name string, version uint64) (guid string, monotonic uint64, err error) {
	versions, err := b.listVersions(ctx, name)
	if err != nil {
		return "", 0, err
	}
	if version > uint64(len(versions)) {
		return "", 0, domain.NoSuchVersion(name, version)
	}
	return versions[version-1].guid, version, nil
}

// secretNameFromID extracts the secret name from an Azure Key Vault
// secret ID URL (https://<vault>.vault.azure.net/secrets/<name>[/<version>]).
func secretNameFromID(id string) string {
	const prefix = "/secrets/"
	i := strings.Index(id, prefix)
	if i < 0 {
		return id
	}
	rest := id[i+len(prefix):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		return rest[:j]
	}
	return rest
}

// versionFromID extracts the per-version GUID from an Azure secret
// version URL (https://<vault>.vault.azure.net/secrets/<name>/<guid>).
func versionFromID(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		return id[i+1:]
	}
	return id
}

// keep azure runtime imported so future use is straightforward.
var _ = azapi.NewPager[struct{}]
