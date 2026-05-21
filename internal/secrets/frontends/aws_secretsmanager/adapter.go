// Package aws_secretsmanager is the AWS Secrets Manager frontend for
// shimanism's secrets service. Phase 11.3 migrated it from a
// hand-written awsJson1_1 wire layer to a spec-driven path: the wire
// decode + dispatch + encode is generated from the vendored Smithy
// spec into services/secrets/gen/aws_secretsmanager.gen.go. This
// package is now the **adapter** — it implements the generated
// gen.SecretsManagerBackend interface by translating each per-op
// request type into the neutral domain.Secrets layer.
//
// The generated handlers handle:
//
//   - X-Amz-Target dispatch (POST /, routed by X-Amz-Target header)
//   - JSON request decode with DisallowUnknownFields
//   - required-field validation → ValidationException
//   - JSON response encode (with awsjson.EpochTime for timestamps)
//   - JSON error envelope (`__type` field + `X-Amzn-Errortype` header)
//
// This file owns:
//
//   - per-op translation logic (gen.<Op>Request → domain → gen.<Op>Response)
//   - the ARN <-> name normaliser (`normaliseSecretID`)
//   - the version-handle codec (`versionIDFor`, `versionFromID`)
//   - the domain.Error → awsjson.BackendError mapper
//
// The harness wires `gen.RegisterSecretsManagerRoutes(adapter)` and
// gets back the *awsjson.Router; no hand-written ServeHTTP path
// remains.
package aws_secretsmanager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/awsjson"
	"github.com/e6qu/shimanism/internal/secrets/domain"
	gen "github.com/e6qu/shimanism/services/secrets/gen"
)

// Adapter binds the generated SecretsManagerBackend interface to a
// concrete domain.Secrets implementation. Construct via New; pass to
// gen.RegisterSecretsManagerRoutes(adapter) to obtain the http.Handler.
type Adapter struct {
	s domain.Secrets
}

// New returns the http.Handler dispatching through the generated
// awsJson1_1 router into the adapter bound to the given backend.
// Replaces the prior Server.ServeHTTP entry point; the package's
// public API stays the same single function.
func New(s domain.Secrets) http.Handler {
	return gen.RegisterSecretsManagerRoutes(&Adapter{s: s})
}

// ---------------------------------------------------------------------
// Helpers — version codec, ARN forge, tag conversion, error mapping.
// ---------------------------------------------------------------------

// versionIDFor renders the domain's monotonic uint64 as the AWS-shape
// UUID with the monotonic part in the bottom 48 bits. Real AWS uses
// genuinely random UUIDs; shim-issued versions round-trip through
// versionFromID losslessly.
func versionIDFor(n uint64) string {
	return fmt.Sprintf("00000000-0000-0000-0000-%012x", n)
}

// versionFromID extracts the monotonic number from a shim-issued
// version UUID. Empty ID maps to 0 ("latest"). Returns ok=false for
// unparseable input.
func versionFromID(id string) (uint64, bool) {
	if id == "" {
		return 0, true
	}
	if i := strings.LastIndexByte(id, '-'); i >= 0 {
		id = id[i+1:]
	}
	n, err := strconv.ParseUint(id, 16, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// fakeARN constructs a shim-issued ARN. The shim has no real account
// or region; SDK clients accept any string starting with
// `arn:aws:secretsmanager:`.
func fakeARN(name string) string {
	return "arn:aws:secretsmanager:shim:000000000000:secret:" + name
}

// normaliseSecretID accepts either a bare name or an ARN and returns
// the secret name. Real AWS ARNs append a 6-char random suffix; the
// shim's ARNs don't. The function strips the suffix only when the
// region segment looks like a real AWS region (i.e. not "shim").
func normaliseSecretID(id string) string {
	const arnPrefix = "arn:aws:secretsmanager:"
	if !strings.HasPrefix(id, arnPrefix) {
		return id
	}
	rest := strings.TrimPrefix(id, arnPrefix)
	parts := strings.SplitN(rest, ":", 4)
	if len(parts) < 4 || parts[2] != "secret" {
		return id
	}
	region := parts[0]
	name := parts[3]
	if region == "shim" {
		return name
	}
	if i := strings.LastIndexByte(name, '-'); i >= 0 && len(name)-i == 7 {
		ok := true
		for _, c := range name[i+1:] {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
				ok = false
				break
			}
		}
		if ok {
			name = name[:i]
		}
	}
	return name
}

// tagsToMap converts the generated TagListType into a domain
// map[string]string.
func tagsToMap(tags gen.TagListType) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		if t.Key == nil {
			continue
		}
		v := ""
		if t.Value != nil {
			v = *t.Value
		}
		out[*t.Key] = v
	}
	return out
}

// tagsFromMap renders a domain tag map as the generated TagListType.
// Output keys are sorted so conformance tests can assert on equality.
func tagsFromMap(m map[string]string) gen.TagListType {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	out := make(gen.TagListType, 0, len(keys))
	for _, k := range keys {
		kCopy, vCopy := k, m[k]
		out = append(out, gen.Tag{Key: &kCopy, Value: &vCopy})
	}
	return out
}

// sortStrings keeps the dependency footprint small — a tiny bubble
// sort is fine for the tag counts we see in practice.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// pickValueBytes returns the value bytes from a request, preferring
// SecretString. SecretBinary is accepted but treated as raw bytes
// (the SDK base64-encodes binary secrets on the wire; the JSON
// decoder converts back to []byte).
func pickValueBytes(secretString *string, secretBinary []byte) ([]byte, bool) {
	if secretString != nil {
		return []byte(*secretString), true
	}
	if len(secretBinary) > 0 {
		out := make([]byte, len(secretBinary))
		copy(out, secretBinary)
		return out, true
	}
	return nil, false
}

// strPtr / strDeref are tiny conversion helpers — generated types use
// *string for optional fields; we either build them or read them with
// safe defaults.
func strPtr(s string) *string { return &s }
func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// mapDomainErr translates a domain.Error into an awsjson.BackendError
// the generated handlers' awsjson.WriteBackendError understands.
// Mirrors the per-error HTTP codes documented in the Smithy spec.
func mapDomainErr(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		return &awsjson.BackendError{
			HTTPStatus: http.StatusInternalServerError,
			Type:       "InternalServiceError",
			Message:    err.Error(),
		}
	}
	switch de.Kind {
	case domain.KindNoSuchSecret, domain.KindNoSuchVersion:
		return &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "ResourceNotFoundException",
			Message:    de.Error(),
		}
	case domain.KindSecretAlreadyExists:
		return &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "ResourceExistsException",
			Message:    de.Error(),
		}
	case domain.KindSecretBeingDeleted:
		return &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidRequestException",
			Message:    de.Error(),
		}
	case domain.KindInvalidArgument:
		return &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterException",
			Message:    de.Error(),
		}
	default:
		return &awsjson.BackendError{
			HTTPStatus: http.StatusInternalServerError,
			Type:       "InternalServiceError",
			Message:    de.Error(),
		}
	}
}

// ---------------------------------------------------------------------
// Per-operation methods — satisfy gen.SecretsManagerBackend.
// ---------------------------------------------------------------------

func (a *Adapter) CreateSecret(ctx context.Context, in *gen.CreateSecretRequest) (*gen.CreateSecretResponse, error) {
	if in.Name == "" {
		return nil, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterException",
			Message:    "Name is required",
		}
	}
	opt := domain.CreateSecretOptions{
		Tags: tagsToMap(in.Tags),
	}
	if in.Description != nil {
		opt.Description = *in.Description
	}
	if v, ok := pickValueBytes(in.SecretString, in.SecretBinary); ok {
		opt.InitialValue = v
	}
	res, err := a.s.CreateSecret(ctx, in.Name, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	resp := &gen.CreateSecretResponse{
		ARN:  strPtr(fakeARN(in.Name)),
		Name: strPtr(in.Name),
	}
	if res.Version > 0 {
		resp.VersionId = strPtr(versionIDFor(res.Version))
	}
	return resp, nil
}

func (a *Adapter) GetSecretValue(ctx context.Context, in *gen.GetSecretValueRequest) (*gen.GetSecretValueResponse, error) {
	if in.SecretId == "" {
		return nil, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterException",
			Message:    "SecretId is required",
		}
	}
	name := normaliseSecretID(in.SecretId)
	var version uint64
	if in.VersionId != nil && *in.VersionId != "" {
		v, ok := versionFromID(*in.VersionId)
		if !ok {
			return nil, &awsjson.BackendError{
				HTTPStatus: http.StatusBadRequest,
				Type:       "InvalidParameterException",
				Message:    "VersionId is not a shim-issued identifier: " + *in.VersionId,
			}
		}
		version = v
	} else if in.VersionStage != nil && *in.VersionStage != "" {
		switch *in.VersionStage {
		case "AWSCURRENT":
			version = 0
		case "AWSPREVIOUS":
			versions, err := a.s.ListVersions(ctx, name)
			if err != nil {
				return nil, mapDomainErr(err)
			}
			if len(versions) < 2 {
				return nil, &awsjson.BackendError{
					HTTPStatus: http.StatusBadRequest,
					Type:       "ResourceNotFoundException",
					Message:    "no AWSPREVIOUS version for secret " + name,
				}
			}
			version = versions[len(versions)-2].Number
		default:
			return nil, &awsjson.BackendError{
				HTTPStatus: http.StatusBadRequest,
				Type:       "InvalidParameterException",
				Message:    "VersionStage " + *in.VersionStage + " is not supported by this shim (intersection: AWSCURRENT, AWSPREVIOUS)",
			}
		}
	}
	val, err := a.s.GetSecretValue(ctx, name, version)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	s := string(val.Value)
	return &gen.GetSecretValueResponse{
		ARN:           strPtr(fakeARN(name)),
		Name:          strPtr(name),
		VersionId:     strPtr(versionIDFor(val.Version)),
		SecretString:  &s,
		VersionStages: gen.SecretVersionStagesType{"AWSCURRENT"},
		CreatedDate:   awsjson.EpochTimePtr(val.CreatedAt),
	}, nil
}

func (a *Adapter) PutSecretValue(ctx context.Context, in *gen.PutSecretValueRequest) (*gen.PutSecretValueResponse, error) {
	if in.SecretId == "" {
		return nil, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterException",
			Message:    "SecretId is required",
		}
	}
	name := normaliseSecretID(in.SecretId)
	value, ok := pickValueBytes(in.SecretString, in.SecretBinary)
	if !ok {
		return nil, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterException",
			Message:    "must provide SecretString or SecretBinary",
		}
	}
	res, err := a.s.PutSecretValue(ctx, name, value)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.PutSecretValueResponse{
		ARN:           strPtr(fakeARN(name)),
		Name:          strPtr(name),
		VersionId:     strPtr(versionIDFor(res.Version)),
		VersionStages: gen.SecretVersionStagesType{"AWSCURRENT"},
	}, nil
}

func (a *Adapter) DeleteSecret(ctx context.Context, in *gen.DeleteSecretRequest) (*gen.DeleteSecretResponse, error) {
	if in.SecretId == "" {
		return nil, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterException",
			Message:    "SecretId is required",
		}
	}
	name := normaliseSecretID(in.SecretId)
	force := false
	if in.ForceDeleteWithoutRecovery != nil {
		force = *in.ForceDeleteWithoutRecovery
	}
	if err := a.s.DeleteSecret(ctx, name, force); err != nil {
		return nil, mapDomainErr(err)
	}
	resp := &gen.DeleteSecretResponse{
		ARN:  strPtr(fakeARN(name)),
		Name: strPtr(name),
	}
	if !force {
		resp.DeletionDate = awsjson.EpochTimePtr(time.Now().UTC().Add(7 * 24 * time.Hour))
	}
	return resp, nil
}

func (a *Adapter) DescribeSecret(ctx context.Context, in *gen.DescribeSecretRequest) (*gen.DescribeSecretResponse, error) {
	if in.SecretId == "" {
		return nil, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterException",
			Message:    "SecretId is required",
		}
	}
	name := normaliseSecretID(in.SecretId)
	s, err := a.s.HeadSecret(ctx, name)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	resp := &gen.DescribeSecretResponse{
		ARN:             strPtr(fakeARN(name)),
		Name:            strPtr(name),
		Description:     strPtr(s.Description),
		Tags:            tagsFromMap(s.Tags),
		CreatedDate:     awsjson.EpochTimePtr(s.CreatedAt),
		LastChangedDate: awsjson.EpochTimePtr(s.UpdatedAt),
	}
	if s.CurrentVersion > 0 {
		resp.VersionIdsToStages = gen.SecretVersionsToStagesMapType{
			versionIDFor(s.CurrentVersion): gen.SecretVersionStagesType{"AWSCURRENT"},
		}
	}
	return resp, nil
}

func (a *Adapter) ListSecrets(ctx context.Context, in *gen.ListSecretsRequest) (*gen.ListSecretsResponse, error) {
	opt := domain.ListSecretsOptions{}
	if in.NextToken != nil {
		opt.NextToken = *in.NextToken
	}
	if in.MaxResults != nil {
		opt.MaxResults = int(*in.MaxResults)
	}
	res, err := a.s.ListSecrets(ctx, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	resp := &gen.ListSecretsResponse{}
	if res.NextToken != "" {
		resp.NextToken = strPtr(res.NextToken)
	}
	for _, s := range res.Secrets {
		resp.SecretList = append(resp.SecretList, gen.SecretListEntry{
			ARN:             strPtr(fakeARN(s.Name)),
			Name:            strPtr(s.Name),
			Description:     strPtr(s.Description),
			Tags:            tagsFromMap(s.Tags),
			CreatedDate:     awsjson.EpochTimePtr(s.CreatedAt),
			LastChangedDate: awsjson.EpochTimePtr(s.UpdatedAt),
		})
	}
	return resp, nil
}

func (a *Adapter) ListSecretVersionIds(ctx context.Context, in *gen.ListSecretVersionIdsRequest) (*gen.ListSecretVersionIdsResponse, error) {
	if in.SecretId == "" {
		return nil, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterException",
			Message:    "SecretId is required",
		}
	}
	name := normaliseSecretID(in.SecretId)
	versions, err := a.s.ListVersions(ctx, name)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	resp := &gen.ListSecretVersionIdsResponse{
		ARN:  strPtr(fakeARN(name)),
		Name: strPtr(name),
	}
	for i, v := range versions {
		var stages gen.SecretVersionStagesType
		if i == len(versions)-1 {
			stages = gen.SecretVersionStagesType{"AWSCURRENT"}
		} else if i == len(versions)-2 {
			stages = gen.SecretVersionStagesType{"AWSPREVIOUS"}
		}
		resp.Versions = append(resp.Versions, gen.SecretVersionsListEntry{
			VersionId:     strPtr(versionIDFor(v.Number)),
			VersionStages: stages,
			CreatedDate:   awsjson.EpochTimePtr(v.CreatedAt),
		})
	}
	return resp, nil
}

func (a *Adapter) UpdateSecret(ctx context.Context, in *gen.UpdateSecretRequest) (*gen.UpdateSecretResponse, error) {
	if in.SecretId == "" {
		return nil, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterException",
			Message:    "SecretId is required",
		}
	}
	name := normaliseSecretID(in.SecretId)
	opt := domain.UpdateSecretOptions{Description: in.Description}
	if err := a.s.UpdateSecret(ctx, name, opt); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.UpdateSecretResponse{
		ARN:  strPtr(fakeARN(name)),
		Name: strPtr(name),
	}, nil
}

func (a *Adapter) TagResource(ctx context.Context, in *gen.TagResourceRequest) (struct{}, error) {
	if in.SecretId == "" {
		return struct{}{}, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterException",
			Message:    "SecretId is required",
		}
	}
	name := normaliseSecretID(in.SecretId)
	existing, err := a.s.HeadSecret(ctx, name)
	if err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	merged := map[string]string{}
	for k, v := range existing.Tags {
		merged[k] = v
	}
	for _, t := range in.Tags {
		if t.Key == nil {
			continue
		}
		merged[*t.Key] = strDeref(t.Value)
	}
	if err := a.s.UpdateSecret(ctx, name, domain.UpdateSecretOptions{Tags: merged}); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) UntagResource(ctx context.Context, in *gen.UntagResourceRequest) (struct{}, error) {
	if in.SecretId == "" {
		return struct{}{}, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterException",
			Message:    "SecretId is required",
		}
	}
	name := normaliseSecretID(in.SecretId)
	existing, err := a.s.HeadSecret(ctx, name)
	if err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	remaining := map[string]string{}
	for k, v := range existing.Tags {
		remaining[k] = v
	}
	for _, k := range in.TagKeys {
		delete(remaining, k)
	}
	if err := a.s.UpdateSecret(ctx, name, domain.UpdateSecretOptions{Tags: remaining}); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) GetResourcePolicy(ctx context.Context, in *gen.GetResourcePolicyRequest) (*gen.GetResourcePolicyResponse, error) {
	if in.SecretId == "" {
		return nil, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterException",
			Message:    "SecretId is required",
		}
	}
	name := normaliseSecretID(in.SecretId)
	if _, err := a.s.HeadSecret(ctx, name); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.GetResourcePolicyResponse{
		ARN:  strPtr(fakeARN(name)),
		Name: strPtr(name),
	}, nil
}
