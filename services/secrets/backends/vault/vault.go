// Package vault is the HashiCorp Vault backend for shimanism's
// secrets service, acting as the K8s peer per PLAN.md Phase 2.
// Secrets land in Vault's KV v2 secrets engine under a configurable
// mount.
//
// Value encoding. The domain stores opaque []byte. Vault's KV v2
// secrets are JSON objects (map<string,string>). The shim writes
// {"value": "<string-of-bytes>"} when storing a domain value, and
// reads `value` back on retrieval. Multi-field Vault secrets
// written outside the shim are not in scope.
//
// Version mapping. Vault's KV v2 numbers versions monotonically
// starting at 1 — exact match for the domain's uint64 versions.
// No mapping table needed.
//
// State stays in Vault. The shim itself holds no state.
package vault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/e6qu/shimanism/internal/secrets/domain"
)

// Config holds Vault connection parameters.
type Config struct {
	// Mount is the KV v2 mount path under which secrets live.
	// Defaults to "secret" (Vault's default KV v2 mount).
	Mount string
}

// Backend implements domain.Secrets via Vault KV v2.
type Backend struct {
	c     *vaultapi.Client
	mount string
}

// New wraps an already-configured Vault API client.
func New(c *vaultapi.Client, cfg Config) *Backend {
	mount := cfg.Mount
	if mount == "" {
		mount = "secret"
	}
	return &Backend{c: c, mount: mount}
}

var _ domain.Secrets = (*Backend)(nil)

func (b *Backend) dataPath(name string) string {
	return path.Join(b.mount, "data", name)
}

func (b *Backend) metadataPath(name string) string {
	return path.Join(b.mount, "metadata", name)
}

// translateErr maps Vault API errors to domain errors.
func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	var rerr *vaultapi.ResponseError
	if errors.As(err, &rerr) {
		switch rerr.StatusCode {
		case 404:
			return domain.NoSuchSecret(name)
		case 400, 422:
			return domain.InvalidArgument(strings.Join(rerr.Errors, "; "))
		}
	}
	return err
}

// valueWrapper is what the shim stores in Vault for each version.
// One field named "value" holding the string form of the bytes
// (Vault KV v2 requires JSON-string field values; binary support
// would round-trip through base64 here too, deferred at this phase).
type valueWrapper struct {
	Value string `json:"value"`
}

func wrapValue(b []byte) map[string]interface{} {
	return map[string]interface{}{"value": string(b)}
}

func unwrapValue(data map[string]interface{}) ([]byte, bool) {
	v, ok := data["value"]
	if !ok {
		return nil, false
	}
	s, ok := v.(string)
	if !ok {
		return nil, false
	}
	return []byte(s), true
}

func (b *Backend) CreateSecret(ctx context.Context, name string, opt domain.CreateSecretOptions) (domain.CreateSecretResult, error) {
	// Vault KV v2's data path PUT both creates and updates. To enforce
	// "create only if absent" we check metadata first; if the secret
	// already exists, return SecretAlreadyExists.
	if _, err := b.c.Logical().ReadWithContext(ctx, b.metadataPath(name)); err == nil {
		return domain.CreateSecretResult{}, domain.SecretAlreadyExists(name)
	}

	// Apply metadata (description + tags) via the metadata endpoint.
	// Both create a placeholder if no version exists yet.
	meta := map[string]interface{}{}
	if opt.Description != "" {
		// Vault doesn't have a first-class description field; encode it
		// as a custom_metadata entry.
		meta["custom_metadata"] = mergeMeta(opt.Tags, opt.Description)
	} else if len(opt.Tags) > 0 {
		meta["custom_metadata"] = mergeMeta(opt.Tags, "")
	}
	if len(meta) > 0 {
		if _, err := b.c.Logical().WriteWithContext(ctx, b.metadataPath(name), meta); err != nil {
			return domain.CreateSecretResult{}, translateErr(err, name)
		}
	}

	if opt.InitialValue == nil {
		// KV v2 represents a "metadata-only" secret as one with
		// metadata but no versions. Some clients reject this; we honour
		// it but return version 0.
		return domain.CreateSecretResult{}, nil
	}

	if _, err := b.c.Logical().WriteWithContext(ctx, b.dataPath(name), map[string]interface{}{
		"data": wrapValue(opt.InitialValue),
		"options": map[string]interface{}{
			"cas": 0, // refuse to write if any version exists — true create
		},
	}); err != nil {
		return domain.CreateSecretResult{}, translateErr(err, name)
	}
	return domain.CreateSecretResult{Version: 1}, nil
}

// mergeMeta merges the user-tags with the description (encoded as a
// reserved `shim-description` key) into one custom_metadata map.
func mergeMeta(tags map[string]string, description string) map[string]string {
	out := make(map[string]string, len(tags)+1)
	for k, v := range tags {
		out[k] = v
	}
	if description != "" {
		out["shim-description"] = description
	}
	return out
}

func (b *Backend) GetSecretValue(ctx context.Context, name string, version uint64) (domain.SecretValue, error) {
	p := b.dataPath(name)
	var resp *vaultapi.Secret
	var err error
	if version == 0 {
		resp, err = b.c.Logical().ReadWithContext(ctx, p)
	} else {
		resp, err = b.c.Logical().ReadWithDataWithContext(ctx, p, map[string][]string{
			"version": {strconv.FormatUint(version, 10)},
		})
	}
	if err != nil {
		return domain.SecretValue{}, translateErr(err, name)
	}
	if resp == nil {
		return domain.SecretValue{}, domain.NoSuchSecret(name)
	}
	if resp.Data == nil {
		return domain.SecretValue{}, domain.NoSuchVersion(name, version)
	}
	// KV v2 read response: { "data": {...user fields}, "metadata": {...version info} }
	dataAny, ok := resp.Data["data"]
	if !ok || dataAny == nil {
		return domain.SecretValue{}, domain.NoSuchVersion(name, version)
	}
	data, ok := dataAny.(map[string]interface{})
	if !ok {
		return domain.SecretValue{}, fmt.Errorf("vault: unexpected data shape for %s", name)
	}
	bytes, ok := unwrapValue(data)
	if !ok {
		return domain.SecretValue{}, fmt.Errorf("vault: shim wrapper field 'value' missing on secret %s", name)
	}
	metaAny := resp.Data["metadata"]
	var resolvedVersion uint64
	var created time.Time
	if m, ok := metaAny.(map[string]interface{}); ok {
		if n, ok := m["version"].(json.Number); ok {
			if v, err := n.Int64(); err == nil {
				resolvedVersion = uint64(v)
			}
		}
		if c, ok := m["created_time"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, c); err == nil {
				created = t
			}
		}
	}
	if resolvedVersion == 0 {
		resolvedVersion = version
	}
	return domain.SecretValue{
		Name:      name,
		Version:   resolvedVersion,
		Value:     bytes,
		CreatedAt: created,
	}, nil
}

func (b *Backend) PutSecretValue(ctx context.Context, name string, value []byte) (domain.PutSecretValueResult, error) {
	resp, err := b.c.Logical().WriteWithContext(ctx, b.dataPath(name), map[string]interface{}{
		"data": wrapValue(value),
	})
	if err != nil {
		return domain.PutSecretValueResult{}, translateErr(err, name)
	}
	if resp == nil || resp.Data == nil {
		return domain.PutSecretValueResult{}, fmt.Errorf("vault: empty response writing secret %s", name)
	}
	// Write response: { "version": <int>, "created_time": "...", ... }
	if n, ok := resp.Data["version"].(json.Number); ok {
		if v, err := n.Int64(); err == nil {
			return domain.PutSecretValueResult{Version: uint64(v)}, nil
		}
	}
	if v, ok := resp.Data["version"].(int64); ok {
		return domain.PutSecretValueResult{Version: uint64(v)}, nil
	}
	if v, ok := resp.Data["version"].(float64); ok {
		return domain.PutSecretValueResult{Version: uint64(v)}, nil
	}
	return domain.PutSecretValueResult{}, fmt.Errorf("vault: write response missing 'version' field for %s", name)
}

func (b *Backend) DeleteSecret(ctx context.Context, name string, force bool) error {
	// Force delete: remove all version history + metadata via DELETE
	// on the metadata endpoint.
	if force {
		_, err := b.c.Logical().DeleteWithContext(ctx, b.metadataPath(name))
		return translateErr(err, name)
	}
	// Soft delete: KV v2's data-endpoint DELETE marks the current
	// version as deleted but leaves the history. Subsequent GETs
	// return 404 unless `version=` is passed to read history. This
	// matches the domain contract — DeleteSecret(force=false) renders
	// the secret unreadable through the normal path.
	_, err := b.c.Logical().DeleteWithContext(ctx, b.dataPath(name))
	return translateErr(err, name)
}

func (b *Backend) HeadSecret(ctx context.Context, name string) (domain.Secret, error) {
	resp, err := b.c.Logical().ReadWithContext(ctx, b.metadataPath(name))
	if err != nil {
		return domain.Secret{}, translateErr(err, name)
	}
	if resp == nil || resp.Data == nil {
		return domain.Secret{}, domain.NoSuchSecret(name)
	}
	s := domain.Secret{Name: name, Enabled: true}
	if c, ok := resp.Data["created_time"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, c); err == nil {
			s.CreatedAt = t
		}
	}
	if c, ok := resp.Data["updated_time"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, c); err == nil {
			s.UpdatedAt = t
		}
	}
	if cm, ok := resp.Data["custom_metadata"].(map[string]interface{}); ok {
		s.Tags = map[string]string{}
		for k, v := range cm {
			if str, ok := v.(string); ok {
				if k == "shim-description" {
					s.Description = str
					continue
				}
				s.Tags[k] = str
			}
		}
		if len(s.Tags) == 0 {
			s.Tags = nil
		}
	}
	if cv, ok := resp.Data["current_version"].(json.Number); ok {
		if v, err := cv.Int64(); err == nil {
			s.CurrentVersion = uint64(v)
		}
	}
	if cv, ok := resp.Data["current_version"].(float64); ok {
		s.CurrentVersion = uint64(cv)
	}
	return s, nil
}

func (b *Backend) ListSecrets(ctx context.Context, opt domain.ListSecretsOptions) (domain.ListSecretsResult, error) {
	// Vault's KV v2 list endpoint is `LIST <mount>/metadata/<prefix>`.
	// The library uses the special Logical().ListWithContext call.
	resp, err := b.c.Logical().ListWithContext(ctx, path.Join(b.mount, "metadata"))
	if err != nil {
		return domain.ListSecretsResult{}, translateErr(err, "")
	}
	if resp == nil || resp.Data == nil {
		return domain.ListSecretsResult{}, nil
	}
	keysAny, ok := resp.Data["keys"]
	if !ok {
		return domain.ListSecretsResult{}, nil
	}
	keysList, ok := keysAny.([]interface{})
	if !ok {
		return domain.ListSecretsResult{}, fmt.Errorf("vault: list response 'keys' has unexpected shape")
	}
	names := make([]string, 0, len(keysList))
	for _, k := range keysList {
		if s, ok := k.(string); ok {
			if opt.Prefix == "" || strings.HasPrefix(s, opt.Prefix) {
				names = append(names, strings.TrimSuffix(s, "/"))
			}
		}
	}
	sort.Strings(names)
	res := domain.ListSecretsResult{Secrets: make([]domain.Secret, 0, len(names))}
	for _, name := range names {
		// Reading metadata for every entry is expensive but stateless.
		// At this phase, lists are small. Pagination + tag filtering
		// are domain features the larger backends will exercise; the
		// Vault peer keeps it simple.
		s, err := b.HeadSecret(ctx, name)
		if err != nil {
			// If the secret was deleted between list and head, skip it.
			var de *domain.Error
			if errors.As(err, &de) && de.Kind == domain.KindNoSuchSecret {
				continue
			}
			return domain.ListSecretsResult{}, err
		}
		res.Secrets = append(res.Secrets, s)
	}
	if opt.MaxResults > 0 && len(res.Secrets) > opt.MaxResults {
		res.Secrets = res.Secrets[:opt.MaxResults]
	}
	return res, nil
}

func (b *Backend) ListVersions(ctx context.Context, name string) ([]domain.Version, error) {
	resp, err := b.c.Logical().ReadWithContext(ctx, b.metadataPath(name))
	if err != nil {
		return nil, translateErr(err, name)
	}
	if resp == nil || resp.Data == nil {
		return nil, domain.NoSuchSecret(name)
	}
	versionsAny, ok := resp.Data["versions"].(map[string]interface{})
	if !ok {
		return nil, nil
	}
	// versions is { "1": {...}, "2": {...} }; collect + sort numerically.
	type entry struct {
		n uint64
		t time.Time
	}
	entries := make([]entry, 0, len(versionsAny))
	for k, v := range versionsAny {
		n, err := strconv.ParseUint(k, 10, 64)
		if err != nil {
			continue
		}
		e := entry{n: n}
		if m, ok := v.(map[string]interface{}); ok {
			if c, ok := m["created_time"].(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, c); err == nil {
					e.t = t
				}
			}
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].n < entries[j].n })
	out := make([]domain.Version, len(entries))
	for i, e := range entries {
		out[i] = domain.Version{Number: e.n, CreatedAt: e.t}
	}
	return out, nil
}

// silence unused-import warning for json.Decoder when the package
// compiles without exercising the encoding paths.
var _ = json.NewDecoder
var _ = valueWrapper{}
