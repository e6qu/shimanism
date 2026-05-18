package aws_s3

import (
	"context"

	"github.com/e6qu/shimanism/internal/restxml"
	"github.com/e6qu/shimanism/internal/storage/domain"
	gen "github.com/e6qu/shimanism/services/storage/gen"
)

// The 18 bucket-config probes the Terraform AWS provider issues on
// every aws_s3_bucket resource Read. They never touch the domain
// interface — these are AWS-specific feature concepts that translate
// universally to "feature not configured" across every cloud's
// freshly-created bucket. The adapter checks bucket existence via
// the domain (HeadBucket) and then returns the canonical S3 error.

func (a *Adapter) headExists(ctx context.Context, bucket string) error {
	_, err := a.s.HeadBucket(ctx, bucket)
	return mapError(err)
}

func (a *Adapter) GetBucketLocation(ctx context.Context, in *gen.GetBucketLocationRequest) (*gen.GetBucketLocationOutput, error) {
	b, err := a.s.HeadBucket(ctx, in.Bucket)
	if err != nil {
		return nil, mapError(err)
	}
	loc := gen.BucketLocationConstraint(b.Region)
	return &gen.GetBucketLocationOutput{LocationConstraint: &loc}, nil
}

func (a *Adapter) GetBucketPolicy(ctx context.Context, in *gen.GetBucketPolicyRequest) (*gen.GetBucketPolicyOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return nil, restxml.NoSuchBucketPolicy(in.Bucket)
}

func (a *Adapter) GetBucketAcl(ctx context.Context, in *gen.GetBucketAclRequest) (*gen.GetBucketAclOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return &gen.GetBucketAclOutput{
		Owner: &gen.Owner{ID: ptr("shimanism"), DisplayName: ptr("shimanism")},
	}, nil
}

func (a *Adapter) GetBucketVersioning(ctx context.Context, in *gen.GetBucketVersioningRequest) (*gen.GetBucketVersioningOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return &gen.GetBucketVersioningOutput{}, nil
}

func (a *Adapter) GetBucketLogging(ctx context.Context, in *gen.GetBucketLoggingRequest) (*gen.GetBucketLoggingOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return &gen.GetBucketLoggingOutput{}, nil
}

func (a *Adapter) GetBucketCors(ctx context.Context, in *gen.GetBucketCorsRequest) (*gen.GetBucketCorsOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return nil, restxml.NoSuchCORSConfiguration(in.Bucket)
}

func (a *Adapter) GetBucketLifecycleConfiguration(ctx context.Context, in *gen.GetBucketLifecycleConfigurationRequest) (*gen.GetBucketLifecycleConfigurationOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return nil, restxml.NoSuchLifecycleConfiguration(in.Bucket)
}

func (a *Adapter) GetBucketReplication(ctx context.Context, in *gen.GetBucketReplicationRequest) (*gen.GetBucketReplicationOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return nil, restxml.ReplicationConfigurationNotFound(in.Bucket)
}

func (a *Adapter) GetBucketRequestPayment(ctx context.Context, in *gen.GetBucketRequestPaymentRequest) (*gen.GetBucketRequestPaymentOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	p := gen.PayerBucketOwner
	return &gen.GetBucketRequestPaymentOutput{Payer: &p}, nil
}

func (a *Adapter) GetBucketTagging(ctx context.Context, in *gen.GetBucketTaggingRequest) (*gen.GetBucketTaggingOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return nil, restxml.NoSuchTagSet(in.Bucket)
}

func (a *Adapter) GetBucketWebsite(ctx context.Context, in *gen.GetBucketWebsiteRequest) (*gen.GetBucketWebsiteOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return nil, restxml.NoSuchWebsiteConfiguration(in.Bucket)
}

func (a *Adapter) GetBucketEncryption(ctx context.Context, in *gen.GetBucketEncryptionRequest) (*gen.GetBucketEncryptionOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return nil, restxml.ServerSideEncryptionConfigurationNotFound(in.Bucket)
}

func (a *Adapter) GetBucketAccelerateConfiguration(ctx context.Context, in *gen.GetBucketAccelerateConfigurationRequest) (*gen.GetBucketAccelerateConfigurationOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return &gen.GetBucketAccelerateConfigurationOutput{}, nil
}

func (a *Adapter) GetObjectLockConfiguration(ctx context.Context, in *gen.GetObjectLockConfigurationRequest) (*gen.GetObjectLockConfigurationOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return nil, restxml.ObjectLockConfigurationNotFound(in.Bucket)
}

func (a *Adapter) GetBucketNotificationConfiguration(ctx context.Context, in *gen.GetBucketNotificationConfigurationRequest) (*gen.NotificationConfiguration, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return &gen.NotificationConfiguration{}, nil
}

func (a *Adapter) GetBucketOwnershipControls(ctx context.Context, in *gen.GetBucketOwnershipControlsRequest) (*gen.GetBucketOwnershipControlsOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return nil, restxml.OwnershipControlsNotFound(in.Bucket)
}

func (a *Adapter) GetBucketPolicyStatus(ctx context.Context, in *gen.GetBucketPolicyStatusRequest) (*gen.GetBucketPolicyStatusOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return nil, restxml.NoSuchBucketPolicy(in.Bucket)
}

func (a *Adapter) GetPublicAccessBlock(ctx context.Context, in *gen.GetPublicAccessBlockRequest) (*gen.GetPublicAccessBlockOutput, error) {
	if err := a.headExists(ctx, in.Bucket); err != nil {
		return nil, err
	}
	return nil, restxml.NoSuchPublicAccessBlockConfiguration(in.Bucket)
}

// Reference the domain package so the import doesn't go unused when
// adapter.go is the only caller of it that compilers can see.
var _ = domain.KindUnknown
