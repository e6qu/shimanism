// Package aws is the AWS Secrets Manager passthrough backend. It
// uses aws-sdk-go-v2/service/secretsmanager to drive real AWS
// Secrets Manager. Useful for auth interception, observability
// injection, cross-region routing, or for using the same shim
// binary to talk to real Secrets Manager alongside other backends.
//
// Version handling. AWS Secrets Manager uses UUID `VersionId`
// values; the domain uses monotonic uint64. The mapping is derived
// per request by listing versions, sorting by CreatedDate, and
// indexing — same stateless approach the shim's other adapters
// use. No translation table lives in the shim.
package aws

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/smithy-go"

	"github.com/e6qu/shimanism/internal/secrets/domain"
)

// Backend implements domain.Secrets via real AWS Secrets Manager.
type Backend struct {
	c *awssm.Client
}

// New wraps an already-configured Secrets Manager client.
func New(client *awssm.Client) *Backend { return &Backend{c: client} }

var _ domain.Secrets = (*Backend)(nil)

// translateErr maps AWS SDK errors to domain errors using the
// canonical AWS error short names.
func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	var nf *smtypes.ResourceNotFoundException
	if errors.As(err, &nf) {
		return domain.NoSuchSecret(name)
	}
	var ex *smtypes.ResourceExistsException
	if errors.As(err, &ex) {
		return domain.SecretAlreadyExists(name)
	}
	var bad *smtypes.InvalidParameterException
	if errors.As(err, &bad) {
		return domain.InvalidArgument(awsapi.ToString(bad.Message))
	}
	var inv *smtypes.InvalidRequestException
	if errors.As(err, &inv) {
		// AWS uses InvalidRequestException for "secret is scheduled for
		// deletion". Match on the message to surface the right kind.
		msg := awsapi.ToString(inv.Message)
		if strings.Contains(msg, "scheduled for deletion") {
			return domain.SecretBeingDeleted(name)
		}
		return domain.InvalidArgument(msg)
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "ResourceNotFoundException":
			return domain.NoSuchSecret(name)
		case "ResourceExistsException":
			return domain.SecretAlreadyExists(name)
		case "InvalidParameterException":
			return domain.InvalidArgument(ae.ErrorMessage())
		}
	}
	return err
}

func (b *Backend) CreateSecret(ctx context.Context, name string, opt domain.CreateSecretOptions) (domain.CreateSecretResult, error) {
	in := &awssm.CreateSecretInput{
		Name: awsapi.String(name),
	}
	if opt.Description != "" {
		in.Description = awsapi.String(opt.Description)
	}
	if opt.InitialValue != nil {
		s := string(opt.InitialValue)
		in.SecretString = awsapi.String(s)
	}
	if len(opt.Tags) > 0 {
		in.Tags = make([]smtypes.Tag, 0, len(opt.Tags))
		// Deterministic order so concurrent runs don't churn AWS state.
		keys := make([]string, 0, len(opt.Tags))
		for k := range opt.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := opt.Tags[k]
			in.Tags = append(in.Tags, smtypes.Tag{
				Key:   awsapi.String(k),
				Value: awsapi.String(v),
			})
		}
	}
	_, err := b.c.CreateSecret(ctx, in)
	if err != nil {
		return domain.CreateSecretResult{}, translateErr(err, name)
	}
	if opt.InitialValue == nil {
		return domain.CreateSecretResult{}, nil
	}
	return domain.CreateSecretResult{Version: 1}, nil
}

func (b *Backend) GetSecretValue(ctx context.Context, name string, version uint64) (domain.SecretValue, error) {
	in := &awssm.GetSecretValueInput{SecretId: awsapi.String(name)}
	resolved := version
	if version != 0 {
		// Map monotonic → UUID via ListSecretVersionIds. Sort by
		// CreatedDate ascending; the (version-1)th entry is the
		// monotonic-N version.
		uuid, monotonic, err := b.lookupVersion(ctx, name, version)
		if err != nil {
			return domain.SecretValue{}, err
		}
		in.VersionId = awsapi.String(uuid)
		resolved = monotonic
	}
	out, err := b.c.GetSecretValue(ctx, in)
	if err != nil {
		return domain.SecretValue{}, translateErr(err, name)
	}
	val := []byte(awsapi.ToString(out.SecretString))
	if len(val) == 0 && len(out.SecretBinary) > 0 {
		val = out.SecretBinary
	}
	if resolved == 0 {
		// Caller asked for "latest"; compute the monotonic number by
		// listing. Costs one extra request per latest read; acceptable
		// at this phase.
		_, latestMon, err := b.lookupLatest(ctx, name)
		if err != nil {
			return domain.SecretValue{}, err
		}
		resolved = latestMon
	}
	created := time.Time{}
	if out.CreatedDate != nil {
		created = *out.CreatedDate
	}
	return domain.SecretValue{
		Name:      name,
		Version:   resolved,
		Value:     val,
		CreatedAt: created,
	}, nil
}

func (b *Backend) PutSecretValue(ctx context.Context, name string, value []byte) (domain.PutSecretValueResult, error) {
	s := string(value)
	out, err := b.c.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId:     awsapi.String(name),
		SecretString: awsapi.String(s),
	})
	if err != nil {
		return domain.PutSecretValueResult{}, translateErr(err, name)
	}
	// Look up the new version's monotonic number. The Put response
	// gives us the AWS UUID, but the monotonic number is "the length
	// of the now-current versions list after Put". List + count.
	_ = out
	versions, err := b.listVersions(ctx, name)
	if err != nil {
		return domain.PutSecretValueResult{}, err
	}
	return domain.PutSecretValueResult{Version: uint64(len(versions))}, nil
}

func (b *Backend) DeleteSecret(ctx context.Context, name string, force bool) error {
	in := &awssm.DeleteSecretInput{SecretId: awsapi.String(name)}
	if force {
		in.ForceDeleteWithoutRecovery = awsapi.Bool(true)
	}
	_, err := b.c.DeleteSecret(ctx, in)
	return translateErr(err, name)
}

func (b *Backend) HeadSecret(ctx context.Context, name string) (domain.Secret, error) {
	out, err := b.c.DescribeSecret(ctx, &awssm.DescribeSecretInput{
		SecretId: awsapi.String(name),
	})
	if err != nil {
		return domain.Secret{}, translateErr(err, name)
	}
	s := domain.Secret{
		Name:        awsapi.ToString(out.Name),
		Description: awsapi.ToString(out.Description),
		Enabled:     true,
	}
	if out.CreatedDate != nil {
		s.CreatedAt = *out.CreatedDate
	}
	if out.LastChangedDate != nil {
		s.UpdatedAt = *out.LastChangedDate
	}
	if len(out.Tags) > 0 {
		s.Tags = make(map[string]string, len(out.Tags))
		for _, t := range out.Tags {
			s.Tags[awsapi.ToString(t.Key)] = awsapi.ToString(t.Value)
		}
	}
	// CurrentVersion = length of versions list.
	versions, err := b.listVersions(ctx, name)
	if err != nil {
		return domain.Secret{}, err
	}
	s.CurrentVersion = uint64(len(versions))
	return s, nil
}

func (b *Backend) ListSecrets(ctx context.Context, opt domain.ListSecretsOptions) (domain.ListSecretsResult, error) {
	in := &awssm.ListSecretsInput{}
	if opt.MaxResults > 0 {
		mr := int32(opt.MaxResults)
		in.MaxResults = &mr
	}
	if opt.NextToken != "" {
		in.NextToken = awsapi.String(opt.NextToken)
	}
	out, err := b.c.ListSecrets(ctx, in)
	if err != nil {
		return domain.ListSecretsResult{}, translateErr(err, "")
	}
	res := domain.ListSecretsResult{NextToken: awsapi.ToString(out.NextToken)}
	for _, s := range out.SecretList {
		name := awsapi.ToString(s.Name)
		if opt.Prefix != "" && !strings.HasPrefix(name, opt.Prefix) {
			continue
		}
		entry := domain.Secret{
			Name:        name,
			Description: awsapi.ToString(s.Description),
			Enabled:     true,
		}
		if s.CreatedDate != nil {
			entry.CreatedAt = *s.CreatedDate
		}
		if s.LastChangedDate != nil {
			entry.UpdatedAt = *s.LastChangedDate
		}
		if len(s.Tags) > 0 {
			entry.Tags = make(map[string]string, len(s.Tags))
			for _, t := range s.Tags {
				entry.Tags[awsapi.ToString(t.Key)] = awsapi.ToString(t.Value)
			}
		}
		res.Secrets = append(res.Secrets, entry)
	}
	return res, nil
}

func (b *Backend) ListVersions(ctx context.Context, name string) ([]domain.Version, error) {
	awsVersions, err := b.listVersions(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Version, 0, len(awsVersions))
	for i, v := range awsVersions {
		entry := domain.Version{
			Number: uint64(i + 1),
		}
		if v.CreatedDate != nil {
			entry.CreatedAt = *v.CreatedDate
		}
		out = append(out, entry)
	}
	return out, nil
}

// listVersions returns the secret's versions in ascending
// creation-date order (monotonic order). One AWS API call.
func (b *Backend) listVersions(ctx context.Context, name string) ([]smtypes.SecretVersionsListEntry, error) {
	out, err := b.c.ListSecretVersionIds(ctx, &awssm.ListSecretVersionIdsInput{
		SecretId:          awsapi.String(name),
		IncludeDeprecated: awsapi.Bool(true),
	})
	if err != nil {
		return nil, translateErr(err, name)
	}
	vs := out.Versions
	sort.SliceStable(vs, func(i, j int) bool {
		ti := time.Time{}
		tj := time.Time{}
		if vs[i].CreatedDate != nil {
			ti = *vs[i].CreatedDate
		}
		if vs[j].CreatedDate != nil {
			tj = *vs[j].CreatedDate
		}
		return ti.Before(tj)
	})
	return vs, nil
}

// lookupVersion returns the AWS UUID for a given monotonic version
// number, plus the monotonic number itself (which is just `version`
// echoed back unless version was 0 → "latest").
func (b *Backend) lookupVersion(ctx context.Context, name string, version uint64) (uuid string, monotonic uint64, err error) {
	versions, err := b.listVersions(ctx, name)
	if err != nil {
		return "", 0, err
	}
	if version > uint64(len(versions)) {
		return "", 0, domain.NoSuchVersion(name, version)
	}
	v := versions[version-1]
	return awsapi.ToString(v.VersionId), version, nil
}

// lookupLatest returns the AWS UUID + monotonic number of the
// AWSCURRENT version.
func (b *Backend) lookupLatest(ctx context.Context, name string) (uuid string, monotonic uint64, err error) {
	versions, err := b.listVersions(ctx, name)
	if err != nil {
		return "", 0, err
	}
	if len(versions) == 0 {
		return "", 0, domain.NoSuchVersion(name, 0)
	}
	last := versions[len(versions)-1]
	return awsapi.ToString(last.VersionId), uint64(len(versions)), nil
}
