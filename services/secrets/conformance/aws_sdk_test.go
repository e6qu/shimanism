// Phase 2 conformance: AWS Secrets Manager-shaped frontend
// exercised by the official `aws-sdk-go-v2/service/secretsmanager`
// SDK. The SDK is pointed at the shim via BaseEndpoint; signature
// validation is disabled in the shim at this phase so any static
// credentials let the SDK construct requests.
package conformance_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	sm_types "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
)

// newAWSSecretsManagerClient builds an aws-sdk-go-v2 Secrets Manager
// client pointed at the shim URL. Path-style addressing isn't a
// concept here (Secrets Manager dispatches everything through POST /),
// but we still pin a deterministic region and static credentials so
// the SDK doesn't try to read shell env.
func newAWSSecretsManagerClient(t *testing.T, endpoint string) *secretsmanager.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		// Use the verifier's trusted test credentials so SDK requests
		// sign with a key the shim's SigV4 middleware accepts. With
		// SHIMANISM_TEST_UNAUTHENTICATED=1 (harness init) the verifier
		// short-circuits and these creds are irrelevant; without it
		// the verifier checks the signature.
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	return secretsmanager.NewFromConfig(cfg, func(o *secretsmanager.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

func TestAWSSDK_SecretLifecycle(t *testing.T) {
	srv := harness.StartSecretsServerAWS(t, inmem.New())
	cli := newAWSSecretsManagerClient(t, srv.URL)
	ctx := context.Background()

	// CreateSecret with an initial value.
	create, err := cli.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String("api/token"),
		SecretString: aws.String("hello-shim"),
		Description:  aws.String("conformance fixture"),
		Tags: []sm_types.Tag{
			{Key: aws.String("env"), Value: aws.String("test")},
		},
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if aws.ToString(create.Name) != "api/token" {
		t.Errorf("CreateSecret Name = %q, want api/token", aws.ToString(create.Name))
	}
	if aws.ToString(create.VersionId) == "" {
		t.Errorf("CreateSecret VersionId is empty")
	}

	// DescribeSecret returns the metadata + AWSCURRENT version mapping.
	desc, err := cli.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String("api/token"),
	})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}
	if aws.ToString(desc.Description) != "conformance fixture" {
		t.Errorf("DescribeSecret Description = %q, want conformance fixture", aws.ToString(desc.Description))
	}
	if len(desc.Tags) != 1 || aws.ToString(desc.Tags[0].Key) != "env" || aws.ToString(desc.Tags[0].Value) != "test" {
		t.Errorf("DescribeSecret Tags = %+v, want [{env=test}]", desc.Tags)
	}
	if _, ok := desc.VersionIdsToStages[aws.ToString(create.VersionId)]; !ok {
		t.Errorf("DescribeSecret VersionIdsToStages missing %q (have %v)",
			aws.ToString(create.VersionId), desc.VersionIdsToStages)
	}

	// GetSecretValue by AWSCURRENT (default).
	get, err := cli.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String("api/token"),
	})
	if err != nil {
		t.Fatalf("GetSecretValue: %v", err)
	}
	if aws.ToString(get.SecretString) != "hello-shim" {
		t.Errorf("GetSecretValue SecretString = %q, want hello-shim", aws.ToString(get.SecretString))
	}

	// PutSecretValue creates a new version.
	put, err := cli.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String("api/token"),
		SecretString: aws.String("hello-shim-v2"),
	})
	if err != nil {
		t.Fatalf("PutSecretValue: %v", err)
	}
	if aws.ToString(put.VersionId) == aws.ToString(create.VersionId) {
		t.Errorf("PutSecretValue VersionId %q matches initial — should be new", aws.ToString(put.VersionId))
	}

	// GetSecretValue by AWSPREVIOUS returns the first version.
	prev, err := cli.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId:     aws.String("api/token"),
		VersionStage: aws.String("AWSPREVIOUS"),
	})
	if err != nil {
		t.Fatalf("GetSecretValue AWSPREVIOUS: %v", err)
	}
	if aws.ToString(prev.SecretString) != "hello-shim" {
		t.Errorf("AWSPREVIOUS SecretString = %q, want hello-shim", aws.ToString(prev.SecretString))
	}

	// GetSecretValue by explicit VersionId.
	byID, err := cli.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId:  aws.String("api/token"),
		VersionId: create.VersionId,
	})
	if err != nil {
		t.Fatalf("GetSecretValue VersionId: %v", err)
	}
	if aws.ToString(byID.SecretString) != "hello-shim" {
		t.Errorf("GetSecretValue by ID SecretString = %q, want hello-shim", aws.ToString(byID.SecretString))
	}

	// ListSecrets returns the secret.
	list, err := cli.ListSecrets(ctx, &secretsmanager.ListSecretsInput{})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(list.SecretList) != 1 || aws.ToString(list.SecretList[0].Name) != "api/token" {
		t.Errorf("ListSecrets = %+v, want [api/token]", list.SecretList)
	}

	// ListSecretVersionIds returns both versions in order.
	versions, err := cli.ListSecretVersionIds(ctx, &secretsmanager.ListSecretVersionIdsInput{
		SecretId: aws.String("api/token"),
	})
	if err != nil {
		t.Fatalf("ListSecretVersionIds: %v", err)
	}
	if len(versions.Versions) != 2 {
		t.Fatalf("ListSecretVersionIds count = %d, want 2", len(versions.Versions))
	}
	last := versions.Versions[len(versions.Versions)-1]
	if !containsStage(last.VersionStages, "AWSCURRENT") {
		t.Errorf("last version stages = %v, want AWSCURRENT", last.VersionStages)
	}

	// DeleteSecret with ForceDeleteWithoutRecovery so the next test
	// doesn't see it.
	if _, err := cli.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
		SecretId:                   aws.String("api/token"),
		ForceDeleteWithoutRecovery: aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	// GetSecretValue after delete returns ResourceNotFoundException.
	_, err = cli.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String("api/token"),
	})
	if err == nil {
		t.Fatalf("expected ResourceNotFoundException after delete, got nil")
	}
	var notFound *sm_types.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		t.Errorf("error after delete = %v, want *ResourceNotFoundException", err)
	}
}

func TestAWSSDK_DuplicateCreateRejected(t *testing.T) {
	srv := harness.StartSecretsServerAWS(t, inmem.New())
	cli := newAWSSecretsManagerClient(t, srv.URL)
	ctx := context.Background()

	if _, err := cli.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String("dup"),
		SecretString: aws.String("first"),
	}); err != nil {
		t.Fatalf("first CreateSecret: %v", err)
	}
	_, err := cli.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String("dup"),
		SecretString: aws.String("second"),
	})
	if err == nil {
		t.Fatalf("expected ResourceExistsException on duplicate Create, got nil")
	}
	var exists *sm_types.ResourceExistsException
	if !errors.As(err, &exists) {
		t.Errorf("duplicate Create error = %v, want *ResourceExistsException", err)
	}
}

func TestAWSSDK_GetByARN(t *testing.T) {
	srv := harness.StartSecretsServerAWS(t, inmem.New())
	cli := newAWSSecretsManagerClient(t, srv.URL)
	ctx := context.Background()

	if _, err := cli.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String("arn-test"),
		SecretString: aws.String("via-arn"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	// Shim-issued ARN. Real AWS ARNs include a random 6-char suffix
	// the normaliser strips; verify the shim accepts both.
	for _, id := range []string{
		"arn:aws:secretsmanager:shim:000000000000:secret:arn-test",
		"arn:aws:secretsmanager:us-east-1:123456789012:secret:arn-test-aBcDeF",
	} {
		got, err := cli.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
			SecretId: aws.String(id),
		})
		if err != nil {
			t.Errorf("GetSecretValue(%s): %v", id, err)
			continue
		}
		if aws.ToString(got.SecretString) != "via-arn" {
			t.Errorf("GetSecretValue(%s) SecretString = %q, want via-arn", id, aws.ToString(got.SecretString))
		}
	}
}

// containsStage is a tiny helper — keep the test file self-contained.
func containsStage(stages []string, want string) bool {
	for _, s := range stages {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}
