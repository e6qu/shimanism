// Package aws is the AWS Lambda passthrough backend.
package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/e6qu/shimanism/internal/functions/domain"
)

type Backend struct {
	c *lambda.Client
	// Role is the IAM execution role the backend supplies if the
	// caller doesn't. Required by AWS Lambda.
	role string
}

type Config struct {
	ExecutionRoleARN string
}

func New(c *lambda.Client, cfg Config) *Backend {
	return &Backend{c: c, role: cfg.ExecutionRoleARN}
}

var _ domain.Functions = (*Backend)(nil)

func domainStatus(s lambdatypes.State) domain.Status {
	switch s {
	case lambdatypes.StatePending:
		return domain.StatusCreating
	case lambdatypes.StateActive:
		return domain.StatusAvailable
	case lambdatypes.StateInactive:
		return domain.StatusDeleting
	case lambdatypes.StateFailed:
		return domain.StatusUnknown
	default:
		return domain.StatusCreating
	}
}

// toDomain translates a Lambda FunctionConfiguration. The
// container image isn't part of FunctionConfiguration — only
// GetFunctionOutput carries a separate Code block, so the caller
// supplies it when known (e.g. echoing the Create input).
func (b *Backend) toDomain(out *lambdatypes.FunctionConfiguration, image string) domain.Function {
	fn := domain.Function{
		Name:           awsapi.ToString(out.FunctionName),
		Image:          image,
		Status:         domainStatus(out.State),
		MemoryBytes:    int64(awsapi.ToInt32(out.MemorySize)) * 1024 * 1024,
		TimeoutSeconds: int(awsapi.ToInt32(out.Timeout)),
	}
	if out.Environment != nil && out.Environment.Variables != nil {
		fn.Environment = out.Environment.Variables
	}
	if out.LastModified != nil {
		if t, err := time.Parse(time.RFC3339, *out.LastModified); err == nil {
			fn.CreatedAt = t
		}
	}
	if fn.Status == domain.StatusAvailable && out.FunctionArn != nil {
		// Lambda doesn't natively expose a public function URL.
		// Emit the ARN as a placeholder. Phase 7's HTTP-invoke exit
		// criterion runs only against Knative; AWS-backed invocation
		// tests would need a `CreateFunctionUrlConfig` op (out of
		// intersection at this phase).
		fn.Endpoint = domain.Endpoint{URL: "aws-lambda://" + awsapi.ToString(out.FunctionArn)}
	}
	return fn
}

func (b *Backend) CreateFunction(ctx context.Context, name string, opt domain.CreateFunctionOptions) (domain.Function, error) {
	// Prefer caller-supplied role (10.3 close of BUG-13); fall back to
	// the backend-configured default for callers that don't set one.
	role := opt.Role
	if role == "" {
		role = b.role
	}
	in := &lambda.CreateFunctionInput{
		FunctionName: awsapi.String(name),
		PackageType:  lambdatypes.PackageTypeImage,
		Code: &lambdatypes.FunctionCode{
			ImageUri: awsapi.String(opt.Image),
		},
		Role:       awsapi.String(role),
		MemorySize: awsapi.Int32(int32(opt.MemoryBytes / (1024 * 1024))),
		Timeout:    awsapi.Int32(int32(opt.TimeoutSeconds)),
		Publish:    opt.Publish,
	}
	if len(opt.Environment) > 0 {
		in.Environment = &lambdatypes.Environment{Variables: opt.Environment}
	}
	out, err := b.c.CreateFunction(ctx, in)
	if err != nil {
		return domain.Function{}, translateErr(err, name)
	}
	return b.toDomain(&lambdatypes.FunctionConfiguration{
		FunctionName: out.FunctionName,
		FunctionArn:  out.FunctionArn,
		State:        out.State,
		MemorySize:   out.MemorySize,
		Timeout:      out.Timeout,
		Environment:  out.Environment,
		LastModified: out.LastModified,
	}, opt.Image), nil
}

func (b *Backend) DeleteFunction(ctx context.Context, name string) error {
	if _, err := b.c.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
		FunctionName: awsapi.String(name),
	}); err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) DescribeFunction(ctx context.Context, name string) (domain.Function, error) {
	out, err := b.c.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: awsapi.String(name),
	})
	if err != nil {
		return domain.Function{}, translateErr(err, name)
	}
	image := ""
	if out.Code != nil {
		image = awsapi.ToString(out.Code.ImageUri)
	}
	return b.toDomain(out.Configuration, image), nil
}

func (b *Backend) ListFunctions(ctx context.Context, opt domain.ListFunctionsOptions) (domain.ListFunctionsResult, error) {
	out, err := b.c.ListFunctions(ctx, &lambda.ListFunctionsInput{})
	if err != nil {
		return domain.ListFunctionsResult{}, translateErr(err, "")
	}
	res := domain.ListFunctionsResult{}
	for i := range out.Functions {
		name := awsapi.ToString(out.Functions[i].FunctionName)
		if opt.Prefix != "" && !strings.HasPrefix(name, opt.Prefix) {
			continue
		}
		res.Functions = append(res.Functions, b.toDomain(&out.Functions[i], ""))
		if opt.MaxResults > 0 && len(res.Functions) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) UpdateFunction(ctx context.Context, name string, opt domain.UpdateFunctionOptions) error {
	if opt.Image != "" {
		if _, err := b.c.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
			FunctionName: awsapi.String(name),
			ImageUri:     awsapi.String(opt.Image),
		}); err != nil {
			return translateErr(err, name)
		}
	}
	if opt.Environment != nil || opt.MemoryBytes > 0 || opt.TimeoutSeconds > 0 {
		in := &lambda.UpdateFunctionConfigurationInput{
			FunctionName: awsapi.String(name),
		}
		if opt.Environment != nil {
			in.Environment = &lambdatypes.Environment{Variables: opt.Environment}
		}
		if opt.MemoryBytes > 0 {
			in.MemorySize = awsapi.Int32(int32(opt.MemoryBytes / (1024 * 1024)))
		}
		if opt.TimeoutSeconds > 0 {
			in.Timeout = awsapi.Int32(int32(opt.TimeoutSeconds))
		}
		if _, err := b.c.UpdateFunctionConfiguration(ctx, in); err != nil {
			return translateErr(err, name)
		}
	}
	return nil
}

func translateErr(err error, name string) error {
	var nfe *lambdatypes.ResourceNotFoundException
	if errors.As(err, &nfe) {
		return domain.NoSuchFunction(name)
	}
	var rce *lambdatypes.ResourceConflictException
	if errors.As(err, &rce) {
		return domain.FunctionAlreadyExists(name)
	}
	return fmt.Errorf("lambda %q: %w", name, err)
}
