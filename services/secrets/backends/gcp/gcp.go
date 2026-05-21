// Package gcp is the Google Cloud Secret Manager backend for
// shimanism's secrets service. It uses
// cloud.google.com/go/secretmanager (Apache 2.0).
//
// Version mapping. GCP Secret Manager numbers versions monotonically
// starting at 1 — exact match for the domain's uint64 versions. No
// mapping table.
//
// State stays in GCP. The shim itself holds no state.
package gcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	smapi "cloud.google.com/go/secretmanager/apiv1"
	smpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/e6qu/shimanism/internal/secrets/domain"
)

// Config holds GCP-specific knobs.
type Config struct {
	// ProjectID is required: GCP secrets are addressed as
	// projects/<id>/secrets/<name>.
	ProjectID string
}

// Backend implements domain.Secrets via GCP Secret Manager.
type Backend struct {
	c       *smapi.Client
	project string
}

// New wraps an already-configured client.
func New(c *smapi.Client, cfg Config) *Backend {
	return &Backend{c: c, project: cfg.ProjectID}
}

var _ domain.Secrets = (*Backend)(nil)

func (b *Backend) parent() string {
	return "projects/" + b.project
}

func (b *Backend) secretResource(name string) string {
	return b.parent() + "/secrets/" + name
}

func (b *Backend) versionResource(name string, version uint64) string {
	if version == 0 {
		return b.secretResource(name) + "/versions/latest"
	}
	return fmt.Sprintf("%s/versions/%d", b.secretResource(name), version)
}

func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	if s, ok := status.FromError(err); ok {
		switch s.Code() {
		case codes.NotFound:
			return domain.NoSuchSecret(name)
		case codes.AlreadyExists:
			return domain.SecretAlreadyExists(name)
		case codes.InvalidArgument:
			return domain.InvalidArgument(s.Message())
		case codes.FailedPrecondition:
			// GCP returns FAILED_PRECONDITION when a secret is being
			// deleted (rare — GCP is hard-delete only — but kept
			// defensive).
			return domain.SecretBeingDeleted(name)
		}
	}
	return err
}

func (b *Backend) CreateSecret(ctx context.Context, name string, opt domain.CreateSecretOptions) (domain.CreateSecretResult, error) {
	if b.project == "" {
		return domain.CreateSecretResult{}, domain.InvalidArgument("gcp project ID is required")
	}
	secret := &smpb.Secret{
		Replication: &smpb.Replication{
			Replication: &smpb.Replication_Automatic_{
				Automatic: &smpb.Replication_Automatic{},
			},
		},
		Labels: gcpLabels(opt.Tags),
	}
	if opt.Description != "" {
		// GCP doesn't have a first-class description field; encode it
		// as a reserved label.
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels["shim-description"] = opt.Description
	}
	_, err := b.c.CreateSecret(ctx, &smpb.CreateSecretRequest{
		Parent:   b.parent(),
		SecretId: name,
		Secret:   secret,
	})
	if err != nil {
		return domain.CreateSecretResult{}, translateErr(err, name)
	}
	if opt.InitialValue == nil {
		return domain.CreateSecretResult{}, nil
	}
	if _, err := b.c.AddSecretVersion(ctx, &smpb.AddSecretVersionRequest{
		Parent:  b.secretResource(name),
		Payload: &smpb.SecretPayload{Data: append([]byte(nil), opt.InitialValue...)},
	}); err != nil {
		return domain.CreateSecretResult{}, translateErr(err, name)
	}
	return domain.CreateSecretResult{Version: 1}, nil
}

// gcpLabels converts the domain tag map into GCP's label map. GCP
// labels have constraints (lowercase, 63-char max, [a-z0-9_-] only);
// we pass through and let the API reject invalid values rather than
// silently transforming them.
func gcpLabels(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}
	return out
}

func (b *Backend) GetSecretValue(ctx context.Context, name string, version uint64) (domain.SecretValue, error) {
	out, err := b.c.AccessSecretVersion(ctx, &smpb.AccessSecretVersionRequest{
		Name: b.versionResource(name, version),
	})
	if err != nil {
		return domain.SecretValue{}, translateErr(err, name)
	}
	resolved := version
	if resolved == 0 {
		// Parse the "name" the server returned to recover the numeric
		// version it picked when we asked for `latest`.
		if i := strings.LastIndexByte(out.GetName(), '/'); i >= 0 {
			if n, err := strconv.ParseUint(out.GetName()[i+1:], 10, 64); err == nil {
				resolved = n
			}
		}
	}
	val := []byte{}
	if p := out.GetPayload(); p != nil {
		val = append(val, p.GetData()...)
	}
	return domain.SecretValue{
		Name:    name,
		Version: resolved,
		Value:   val,
	}, nil
}

func (b *Backend) PutSecretValue(ctx context.Context, name string, value []byte) (domain.PutSecretValueResult, error) {
	out, err := b.c.AddSecretVersion(ctx, &smpb.AddSecretVersionRequest{
		Parent:  b.secretResource(name),
		Payload: &smpb.SecretPayload{Data: append([]byte(nil), value...)},
	})
	if err != nil {
		return domain.PutSecretValueResult{}, translateErr(err, name)
	}
	if i := strings.LastIndexByte(out.GetName(), '/'); i >= 0 {
		if n, err := strconv.ParseUint(out.GetName()[i+1:], 10, 64); err == nil {
			return domain.PutSecretValueResult{Version: n}, nil
		}
	}
	return domain.PutSecretValueResult{}, fmt.Errorf("gcp: could not parse version from %q", out.GetName())
}

func (b *Backend) UpdateSecret(ctx context.Context, name string, opt domain.UpdateSecretOptions) error {
	if opt.Enabled != nil && !*opt.Enabled {
		// GCP Secret Manager has no per-secret enabled flag (versions
		// have enabled/disabled; the secret itself is always live).
		return domain.InvalidArgument("GCP Secret Manager does not support a per-secret enabled flag (use version-level enable/disable)")
	}
	if opt.Description == nil && opt.Tags == nil {
		return nil
	}
	// Build the patched Secret + FieldMask. Description rides on the
	// reserved "shim-description" label (same encoding as CreateSecret).
	secret := &smpb.Secret{Name: b.secretResource(name)}
	var paths []string
	if opt.Tags != nil || opt.Description != nil {
		labels := gcpLabels(opt.Tags)
		if labels == nil {
			labels = map[string]string{}
		}
		if opt.Description != nil {
			labels["shim-description"] = *opt.Description
		}
		secret.Labels = labels
		paths = append(paths, "labels")
	}
	_, err := b.c.UpdateSecret(ctx, &smpb.UpdateSecretRequest{
		Secret:     secret,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
	})
	return translateErr(err, name)
}

func (b *Backend) DeleteSecret(ctx context.Context, name string, force bool) error {
	// GCP Secret Manager is hard-delete only — `force` has no effect.
	err := b.c.DeleteSecret(ctx, &smpb.DeleteSecretRequest{
		Name: b.secretResource(name),
	})
	_ = force
	return translateErr(err, name)
}

func (b *Backend) HeadSecret(ctx context.Context, name string) (domain.Secret, error) {
	out, err := b.c.GetSecret(ctx, &smpb.GetSecretRequest{
		Name: b.secretResource(name),
	})
	if err != nil {
		return domain.Secret{}, translateErr(err, name)
	}
	s := domain.Secret{
		Name:    name,
		Enabled: true,
	}
	if out.GetCreateTime() != nil {
		s.CreatedAt = out.GetCreateTime().AsTime()
		s.UpdatedAt = s.CreatedAt
	}
	if labels := out.GetLabels(); len(labels) > 0 {
		s.Tags = map[string]string{}
		for k, v := range labels {
			if k == "shim-description" {
				s.Description = v
				continue
			}
			s.Tags[k] = v
		}
		if len(s.Tags) == 0 {
			s.Tags = nil
		}
	}
	// Resolve CurrentVersion via AccessSecretVersion(latest); cheaper
	// alternative would be ListVersions but that's one round trip
	// either way.
	if val, err := b.c.AccessSecretVersion(ctx, &smpb.AccessSecretVersionRequest{
		Name: b.versionResource(name, 0),
	}); err == nil {
		if i := strings.LastIndexByte(val.GetName(), '/'); i >= 0 {
			if n, err := strconv.ParseUint(val.GetName()[i+1:], 10, 64); err == nil {
				s.CurrentVersion = n
			}
		}
	}
	return s, nil
}

func (b *Backend) ListSecrets(ctx context.Context, opt domain.ListSecretsOptions) (domain.ListSecretsResult, error) {
	if b.project == "" {
		return domain.ListSecretsResult{}, domain.InvalidArgument("gcp project ID is required")
	}
	in := &smpb.ListSecretsRequest{
		Parent:    b.parent(),
		PageToken: opt.NextToken,
	}
	if opt.MaxResults > 0 {
		in.PageSize = int32(opt.MaxResults) //nolint:gosec
	}
	it := b.c.ListSecrets(ctx, in)
	res := domain.ListSecretsResult{}
	for {
		s, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return domain.ListSecretsResult{}, translateErr(err, "")
		}
		// Extract the short name from projects/<p>/secrets/<short>.
		short := s.GetName()
		if i := strings.LastIndexByte(short, '/'); i >= 0 {
			short = short[i+1:]
		}
		if opt.Prefix != "" && !strings.HasPrefix(short, opt.Prefix) {
			continue
		}
		entry := domain.Secret{
			Name:    short,
			Enabled: true,
		}
		if s.GetCreateTime() != nil {
			entry.CreatedAt = s.GetCreateTime().AsTime()
			entry.UpdatedAt = entry.CreatedAt
		}
		if labels := s.GetLabels(); len(labels) > 0 {
			entry.Tags = map[string]string{}
			for k, v := range labels {
				if k == "shim-description" {
					entry.Description = v
					continue
				}
				entry.Tags[k] = v
			}
			if len(entry.Tags) == 0 {
				entry.Tags = nil
			}
		}
		res.Secrets = append(res.Secrets, entry)
	}
	// Page token threading via the iterator is library-internal —
	// the user-side pagination contract for shimanism is handled by
	// the per-page iterator and our (single-page) caller.
	return res, nil
}

func (b *Backend) ListVersions(ctx context.Context, name string) ([]domain.Version, error) {
	it := b.c.ListSecretVersions(ctx, &smpb.ListSecretVersionsRequest{
		Parent: b.secretResource(name),
	})
	var versions []domain.Version
	for {
		v, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, translateErr(err, name)
		}
		short := v.GetName()
		if i := strings.LastIndexByte(short, '/'); i >= 0 {
			short = short[i+1:]
		}
		n, err := strconv.ParseUint(short, 10, 64)
		if err != nil {
			continue
		}
		entry := domain.Version{Number: n}
		if v.GetCreateTime() != nil {
			entry.CreatedAt = v.GetCreateTime().AsTime()
		}
		versions = append(versions, entry)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Number < versions[j].Number })
	return versions, nil
}
