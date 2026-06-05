// Package inmem is an in-process secrets backend used by the
// conformance harness as the always-on baseline. It is not a
// production backend; the shim binary running `secrets -backend=inmem`
// is intended only for tests and local exploration.
//
// State lives in the backend, not the shim — but this backend's
// "state" is just a Go map. Multi-replica deployments must point at
// one of the cloud / Vault backends instead.
package inmem

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/secrets/domain"
)

// Backend implements domain.Secrets entirely in memory.
type Backend struct {
	mu      sync.Mutex
	secrets map[string]*secretState
}

type secretState struct {
	name        string
	description string
	tags        map[string]string
	createdAt   time.Time
	updatedAt   time.Time
	enabled     bool
	deleting    bool
	versions    []versionState
}

type versionState struct {
	number    uint64
	value     []byte
	createdAt time.Time
}

// New constructs a fresh empty backend.
func New() *Backend {
	return &Backend{secrets: map[string]*secretState{}}
}

var _ domain.Secrets = (*Backend)(nil)

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func (b *Backend) CreateSecret(ctx context.Context, name string, opt domain.CreateSecretOptions) (domain.CreateSecretResult, error) {
	if name == "" {
		return domain.CreateSecretResult{}, domain.InvalidArgument("secret name is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing, ok := b.secrets[name]; ok {
		if existing.deleting {
			return domain.CreateSecretResult{}, domain.SecretBeingDeleted(name)
		}
		return domain.CreateSecretResult{}, domain.SecretAlreadyExists(name)
	}
	now := time.Now().UTC()
	st := &secretState{
		name:        name,
		description: opt.Description,
		tags:        copyTags(opt.Tags),
		createdAt:   now,
		updatedAt:   now,
		enabled:     true,
	}
	res := domain.CreateSecretResult{}
	if opt.InitialValue != nil {
		st.versions = append(st.versions, versionState{
			number:    1,
			value:     copyBytes(opt.InitialValue),
			createdAt: now,
		})
		res.Version = 1
	}
	b.secrets[name] = st
	return res, nil
}

func (b *Backend) GetSecretValue(ctx context.Context, name string, version uint64) (domain.SecretValue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.secrets[name]
	if !ok {
		return domain.SecretValue{}, domain.NoSuchSecret(name)
	}
	if st.deleting {
		return domain.SecretValue{}, domain.SecretBeingDeleted(name)
	}
	if len(st.versions) == 0 {
		return domain.SecretValue{}, domain.NoSuchVersion(name, version)
	}
	if version == 0 {
		v := st.versions[len(st.versions)-1]
		return domain.SecretValue{
			Name:      name,
			Version:   v.number,
			Value:     copyBytes(v.value),
			CreatedAt: v.createdAt,
		}, nil
	}
	for _, v := range st.versions {
		if v.number == version {
			return domain.SecretValue{
				Name:      name,
				Version:   v.number,
				Value:     copyBytes(v.value),
				CreatedAt: v.createdAt,
			}, nil
		}
	}
	return domain.SecretValue{}, domain.NoSuchVersion(name, version)
}

func (b *Backend) PutSecretValue(ctx context.Context, name string, value []byte) (domain.PutSecretValueResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.secrets[name]
	if !ok {
		return domain.PutSecretValueResult{}, domain.NoSuchSecret(name)
	}
	if st.deleting {
		return domain.PutSecretValueResult{}, domain.SecretBeingDeleted(name)
	}
	now := time.Now().UTC()
	var nextNumber uint64 = 1
	if n := len(st.versions); n > 0 {
		nextNumber = st.versions[n-1].number + 1
	}
	st.versions = append(st.versions, versionState{
		number:    nextNumber,
		value:     copyBytes(value),
		createdAt: now,
	})
	st.updatedAt = now
	return domain.PutSecretValueResult{Version: nextNumber}, nil
}

func (b *Backend) UpdateSecret(ctx context.Context, name string, opt domain.UpdateSecretOptions) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.secrets[name]
	if !ok {
		return domain.NoSuchSecret(name)
	}
	if opt.Description != nil {
		st.description = *opt.Description
	}
	if opt.Tags != nil {
		st.tags = copyTags(opt.Tags)
	}
	if opt.Enabled != nil {
		st.enabled = *opt.Enabled
	}
	st.updatedAt = time.Now().UTC()
	return nil
}

func (b *Backend) DeleteSecret(ctx context.Context, name string, force bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.secrets[name]
	if !ok {
		return domain.NoSuchSecret(name)
	}
	if force {
		delete(b.secrets, name)
		return nil
	}
	// Soft delete: mark and leave in place. A real backend would
	// implement a recovery window; the in-mem backend marks deleting
	// and rejects subsequent operations to model the same shape.
	st.deleting = true
	return nil
}

func (b *Backend) HeadSecret(ctx context.Context, name string) (domain.Secret, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.secrets[name]
	if !ok {
		return domain.Secret{}, domain.NoSuchSecret(name)
	}
	if st.deleting {
		return domain.Secret{}, domain.SecretBeingDeleted(name)
	}
	return secretFromState(st), nil
}

func secretFromState(st *secretState) domain.Secret {
	var cv uint64
	if n := len(st.versions); n > 0 {
		cv = st.versions[n-1].number
	}
	return domain.Secret{
		Name:           st.name,
		Description:    st.description,
		Tags:           copyTags(st.tags),
		CreatedAt:      st.createdAt,
		UpdatedAt:      st.updatedAt,
		Enabled:        st.enabled,
		CurrentVersion: cv,
	}
}

func (b *Backend) ListSecrets(ctx context.Context, opt domain.ListSecretsOptions) (domain.ListSecretsResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	names := make([]string, 0, len(b.secrets))
	for name, st := range b.secrets {
		if st.deleting {
			continue
		}
		if opt.Prefix != "" && !strings.HasPrefix(name, opt.Prefix) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	res := domain.ListSecretsResult{Secrets: make([]domain.Secret, 0, len(names))}
	for _, name := range names {
		res.Secrets = append(res.Secrets, secretFromState(b.secrets[name]))
	}
	// MaxResults / NextToken: simple in-mem implementation truncates
	// without a continuation token. Pagination is a domain concept
	// the larger backends will exercise; conformance tests don't
	// rely on it for the in-mem path.
	if opt.MaxResults > 0 && len(res.Secrets) > opt.MaxResults {
		res.Secrets = res.Secrets[:opt.MaxResults]
	}
	return res, nil
}

func (b *Backend) ListVersions(ctx context.Context, name string) ([]domain.Version, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.secrets[name]
	if !ok {
		return nil, domain.NoSuchSecret(name)
	}
	if st.deleting {
		return nil, domain.SecretBeingDeleted(name)
	}
	out := make([]domain.Version, 0, len(st.versions))
	for _, v := range st.versions {
		out = append(out, domain.Version{Number: v.number, CreatedAt: v.createdAt})
	}
	return out, nil
}
