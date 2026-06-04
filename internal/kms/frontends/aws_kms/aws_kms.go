// Package aws_kms is the AWS KMS frontend for shimanism's key-management
// service (Phase 19). It bridges the spec-driven awsJson1_1 generated
// stubs (services/kms/gen) onto the neutral domain.KMS interface.
//
// Protocol: awsJson1_1 (X-Amz-Target dispatch, JSON bodies, awsJson
// error envelope). Auth: SigV4 with service="kms".
package aws_kms

import (
	"context"
	"fmt"
	"net/http"

	"github.com/e6qu/shimanism/internal/awsjson"
	"github.com/e6qu/shimanism/internal/kms/domain"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
	gen "github.com/e6qu/shimanism/services/kms/gen"
)

// Adapter binds gen.KMSBackend to a domain.KMS.
type Adapter struct {
	k domain.KMS
}

// New returns the http.Handler dispatching through the generated
// awsJson1_1 router into the adapter bound to the given backend.
func New(k domain.KMS) http.Handler {
	verifier := sigv4verifier.New(sigv4verifier.StaticStore{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, sigv4verifier.Options{
		Service: "kms",
		Region:  "us-east-1",
	})
	mw := sigv4verifier.Middleware(verifier, awsjson.WriteError)
	return mw(gen.RegisterKMSRoutes(&Adapter{k: k}))
}

const acctID = "000000000000"

// mapDomainErr converts domain errors to awsjson.BackendError with the
// AWS KMS error codes + HTTP status codes.
func mapDomainErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case domain.IsNotFound(err):
		return &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "NotFoundException", Message: err.Error()}
	case domain.IsAlreadyExists(err):
		return &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "AlreadyExistsException", Message: err.Error()}
	case domain.IsKeyDisabled(err):
		return &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "DisabledException", Message: err.Error()}
	case domain.IsNotSupported(err):
		return &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "UnsupportedOperationException", Message: err.Error()}
	case domain.IsInvalidInput(err):
		return &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "InvalidCiphertextException", Message: err.Error()}
	default:
		return &awsjson.BackendError{HTTPStatus: http.StatusInternalServerError, Type: "KMSInternalException", Message: err.Error()}
	}
}

func keyArn(id string) string {
	return fmt.Sprintf("arn:aws:kms:us-east-1:%s:key/%s", acctID, id)
}

func tagListToMap(tags gen.TagList) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		if t.TagKey != "" {
			out[t.TagKey] = t.TagValue
		}
	}
	return out
}

func domainKeyToMetadata(k domain.Key) *gen.KeyMetadata {
	acc := acctID
	arn := keyArn(k.ID)
	desc := k.Description
	enabled := k.State == domain.KeyStateEnabled
	state := gen.KeyState(k.State)
	usage := gen.KeyUsageType(k.Usage)
	created := awsjson.EpochTime(k.CreatedAt)
	md := &gen.KeyMetadata{
		AWSAccountId: &acc,
		Arn:          &arn,
		KeyId:        k.ID,
		Description:  &desc,
		Enabled:      &enabled,
		KeyState:     &state,
		KeyUsage:     &usage,
		CreationDate: &created,
	}
	if k.KeySpec != "" {
		spec := gen.KeySpec(k.KeySpec)
		md.KeySpec = &spec
	}
	if !k.DeletionDate.IsZero() {
		dd := awsjson.EpochTime(k.DeletionDate)
		md.DeletionDate = &dd
	}
	return md
}

// ─── operations ──────────────────────────────────────────────────────

func (a *Adapter) CreateKey(ctx context.Context, in *gen.CreateKeyRequest) (*gen.CreateKeyResponse, error) {
	opts := domain.CreateKeyOptions{Tags: tagListToMap(in.Tags)}
	if in.Description != nil {
		opts.Description = *in.Description
	}
	if in.KeyUsage != nil {
		opts.Usage = domain.KeyUsage(*in.KeyUsage)
	}
	if in.KeySpec != nil {
		opts.KeySpec = string(*in.KeySpec)
	}
	key, err := a.k.CreateKey(ctx, opts)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.CreateKeyResponse{KeyMetadata: domainKeyToMetadata(key)}, nil
}

func (a *Adapter) DescribeKey(ctx context.Context, in *gen.DescribeKeyRequest) (*gen.DescribeKeyResponse, error) {
	key, err := a.k.DescribeKey(ctx, in.KeyId)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.DescribeKeyResponse{KeyMetadata: domainKeyToMetadata(key)}, nil
}

func (a *Adapter) ListKeys(ctx context.Context, _ *gen.ListKeysRequest) (*gen.ListKeysResponse, error) {
	res, err := a.k.ListKeys(ctx)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.ListKeysResponse{}
	for _, k := range res.Keys {
		k := k
		arn := keyArn(k.ID)
		id := k.ID
		out.Keys = append(out.Keys, gen.KeyListEntry{KeyId: &id, KeyArn: &arn})
	}
	return out, nil
}

func (a *Adapter) Encrypt(ctx context.Context, in *gen.EncryptRequest) (*gen.EncryptResponse, error) {
	res, err := a.k.Encrypt(ctx, in.KeyId, in.Plaintext)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	arn := keyArn(res.KeyID)
	return &gen.EncryptResponse{KeyId: &arn, CiphertextBlob: res.Ciphertext}, nil
}

func (a *Adapter) Decrypt(ctx context.Context, in *gen.DecryptRequest) (*gen.DecryptResponse, error) {
	res, err := a.k.Decrypt(ctx, in.CiphertextBlob)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	arn := keyArn(res.KeyID)
	return &gen.DecryptResponse{KeyId: &arn, Plaintext: res.Plaintext}, nil
}

func (a *Adapter) ScheduleKeyDeletion(ctx context.Context, in *gen.ScheduleKeyDeletionRequest) (*gen.ScheduleKeyDeletionResponse, error) {
	days := 30
	if in.PendingWindowInDays != nil {
		days = int(*in.PendingWindowInDays)
	}
	key, err := a.k.ScheduleKeyDeletion(ctx, in.KeyId, days)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	arn := keyArn(key.ID)
	state := gen.KeyState(key.State)
	resp := &gen.ScheduleKeyDeletionResponse{KeyId: &arn, KeyState: &state}
	if !key.DeletionDate.IsZero() {
		dd := awsjson.EpochTime(key.DeletionDate)
		resp.DeletionDate = &dd
	}
	return resp, nil
}

func (a *Adapter) CancelKeyDeletion(ctx context.Context, in *gen.CancelKeyDeletionRequest) (*gen.CancelKeyDeletionResponse, error) {
	key, err := a.k.CancelKeyDeletion(ctx, in.KeyId)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	arn := keyArn(key.ID)
	return &gen.CancelKeyDeletionResponse{KeyId: &arn}, nil
}

func (a *Adapter) EnableKeyRotation(ctx context.Context, in *gen.EnableKeyRotationRequest) (struct{}, error) {
	if err := a.k.EnableKeyRotation(ctx, in.KeyId); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) DisableKeyRotation(ctx context.Context, in *gen.DisableKeyRotationRequest) (struct{}, error) {
	if err := a.k.DisableKeyRotation(ctx, in.KeyId); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) GetKeyRotationStatus(ctx context.Context, in *gen.GetKeyRotationStatusRequest) (*gen.GetKeyRotationStatusResponse, error) {
	on, err := a.k.GetKeyRotationStatus(ctx, in.KeyId)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.GetKeyRotationStatusResponse{KeyRotationEnabled: &on}, nil
}

// GetKeyPolicy returns the AWS default key policy. The shim's intersection
// does not manage key policies/grants (out of intersection — see
// INTERSECTION.md), so every key reports the well-known default policy
// granting the account root full access. The hashicorp/aws provider calls
// this unconditionally during aws_kms_key read.
func (a *Adapter) GetKeyPolicy(ctx context.Context, in *gen.GetKeyPolicyRequest) (*gen.GetKeyPolicyResponse, error) {
	if _, err := a.k.DescribeKey(ctx, in.KeyId); err != nil {
		return nil, mapDomainErr(err)
	}
	name := "default"
	if in.PolicyName != nil && *in.PolicyName != "" {
		name = *in.PolicyName
	}
	policy := fmt.Sprintf(`{"Version":"2012-10-17","Id":"key-default-1","Statement":[{"Sid":"Enable IAM User Permissions","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::%s:root"},"Action":"kms:*","Resource":"*"}]}`, acctID)
	return &gen.GetKeyPolicyResponse{Policy: &policy, PolicyName: &name}, nil
}

// ListResourceTags returns the key's tags from the backend.
func (a *Adapter) ListResourceTags(ctx context.Context, in *gen.ListResourceTagsRequest) (*gen.ListResourceTagsResponse, error) {
	key, err := a.k.DescribeKey(ctx, in.KeyId)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.ListResourceTagsResponse{Truncated: boolPtr(false)}
	for k, v := range key.Tags {
		k, v := k, v
		out.Tags = append(out.Tags, gen.Tag{TagKey: k, TagValue: v})
	}
	return out, nil
}

func boolPtr(b bool) *bool { return &b }
