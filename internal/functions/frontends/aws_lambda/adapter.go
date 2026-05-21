// Package aws_lambda is the AWS Lambda-shaped HTTP frontend.
// Phase 11.9 migrated it from a hand-written restJson1 wire layer
// to spec-driven generated stubs. This file is the adapter — it
// implements gen.LambdaBackend by translating each generated
// per-op request type into the neutral domain.Functions interface.
package aws_lambda

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/awsjson"
	"github.com/e6qu/shimanism/internal/functions/domain"
	"github.com/e6qu/shimanism/internal/restxml"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
	gen "github.com/e6qu/shimanism/services/functions/gen"
)

// Adapter binds gen.LambdaBackend to a domain.Functions backend.
type Adapter struct {
	s domain.Functions
}

// New returns the http.Handler dispatching through the generated
// restJson1 router into the adapter bound to the given backend.
// SigV4 verification is wired in; SHIMANISM_TEST_UNAUTHENTICATED=1
// short-circuits during the conformance-lane rewrite (set by the
// harness's init()).
func New(s domain.Functions) http.Handler {
	router := &restxml.Router{}
	gen.RegisterLambdaRoutes(router, &Adapter{s: s})
	verifier := sigv4verifier.New(sigv4verifier.StaticStore{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, sigv4verifier.Options{Service: "lambda", Region: "us-east-1"})
	mw := sigv4verifier.Middleware(verifier, awsjson.WriteError)
	return mw(router)
}

func strPtr(s string) *string { return &s }
func i32Ptr(v int32) *int32   { return &v }

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// nameFromARN extracts the function name from an ARN-or-name.
func nameFromARN(s string) string {
	if i := strings.LastIndex(s, ":"); i >= 0 && strings.HasPrefix(s, "arn:") {
		return s[i+1:]
	}
	return s
}

// stateFromDomain maps the neutral domain.Status onto the Smithy
// State enum.
func stateFromDomain(s domain.Status) gen.State {
	switch s {
	case domain.StatusCreating:
		return "Pending"
	case domain.StatusAvailable, domain.StatusUpdating:
		return "Active"
	case domain.StatusDeleting:
		return "Inactive"
	default:
		return "Failed"
	}
}

func lastUpdateStatusFromDomain(s domain.Status) gen.LastUpdateStatus {
	switch s {
	case domain.StatusUpdating:
		return "InProgress"
	case domain.StatusAvailable:
		return "Successful"
	default:
		return ""
	}
}

// lambdaVersionFor returns the canonical Lambda version string:
// "$LATEST" for unpublished, "1" for the first published version.
func lambdaVersionFor(fn domain.Function) string {
	if fn.Publish {
		return "1"
	}
	return "$LATEST"
}

// functionToAWS produces the FunctionConfiguration generated type
// from a domain.Function. Mirrors the canonical AWS defaults real
// Lambda emits on Get / List.
func functionToAWS(fn domain.Function) *gen.FunctionConfiguration {
	memMB := int32(fn.MemoryBytes / (1024 * 1024))
	if memMB == 0 {
		memMB = 128 // AWS Lambda default
	}
	arn := "arn:aws:lambda:us-east-1:000000000000:function:" + fn.Name
	pkg := gen.PackageType("Image")
	state := stateFromDomain(fn.Status)
	lus := lastUpdateStatusFromDomain(fn.Status)
	cfg := &gen.FunctionConfiguration{
		FunctionName:     strPtr(fn.Name),
		FunctionArn:      strPtr(arn),
		PackageType:      &pkg,
		State:            &state,
		MemorySize:       &memMB,
		Timeout:          i32Ptr(int32(fn.TimeoutSeconds)),
		LastModified:     strPtr(time.Now().UTC().Format(time.RFC3339)),
		Role:             strPtr(fn.Role),
		Version:          strPtr(lambdaVersionFor(fn)),
	}
	if lus != "" {
		cfg.LastUpdateStatus = &lus
	}
	if len(fn.Environment) > 0 {
		cfg.Environment = &gen.EnvironmentResponse{Variables: gen.EnvironmentVariables(fn.Environment)}
	}
	return cfg
}

func functionCodeLocation(fn domain.Function) *gen.FunctionCodeLocation {
	if fn.Image == "" {
		return nil
	}
	return &gen.FunctionCodeLocation{ImageUri: strPtr(fn.Image)}
}

// mapDomainErr maps domain errors onto Lambda restJson1 error
// envelopes via awsjson.BackendError.
func mapDomainErr(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		return &awsjson.BackendError{
			HTTPStatus: http.StatusInternalServerError,
			Type:       "ServiceException",
			Message:    err.Error(),
		}
	}
	be := &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Message: de.Error()}
	switch de.Kind {
	case domain.KindNoSuchFunction:
		be.HTTPStatus = http.StatusNotFound
		be.Type = "ResourceNotFoundException"
	case domain.KindFunctionAlreadyExists:
		be.HTTPStatus = http.StatusConflict
		be.Type = "ResourceConflictException"
	case domain.KindInvalidArgument:
		be.Type = "InvalidParameterValueException"
	default:
		be.HTTPStatus = http.StatusInternalServerError
		be.Type = "ServiceException"
	}
	return be
}

// ---------------------------------------------------------------------
// Per-operation methods.
// ---------------------------------------------------------------------

func (a *Adapter) CreateFunction(ctx context.Context, in *gen.CreateFunctionRequest) (*gen.FunctionConfiguration, error) {
	if in.FunctionName == "" {
		return nil, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterValueException",
			Message:    "FunctionName is required",
		}
	}
	if in.PackageType != nil && *in.PackageType != "Image" {
		return nil, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterValueException",
			Message:    "PackageType=Zip is out of intersection; only Image is supported",
		}
	}
	if in.Code == nil || in.Code.ImageUri == nil || *in.Code.ImageUri == "" {
		return nil, &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "InvalidParameterValueException",
			Message:    "Code.ImageUri is required",
		}
	}
	opt := domain.CreateFunctionOptions{
		Image:   *in.Code.ImageUri,
		Role:    in.Role,
		Publish: in.Publish != nil && *in.Publish,
	}
	if in.MemorySize != nil {
		opt.MemoryBytes = int64(*in.MemorySize) * 1024 * 1024
	}
	if in.Timeout != nil {
		opt.TimeoutSeconds = int(*in.Timeout)
	}
	if in.Environment != nil && len(in.Environment.Variables) > 0 {
		opt.Environment = map[string]string(in.Environment.Variables)
	}
	fn, err := a.s.CreateFunction(ctx, in.FunctionName, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return functionToAWS(fn), nil
}

func (a *Adapter) GetFunction(ctx context.Context, in *gen.GetFunctionRequest) (*gen.GetFunctionResponse, error) {
	name := in.FunctionName
	fn, err := a.s.DescribeFunction(ctx, name)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.GetFunctionResponse{
		Configuration: functionToAWS(fn),
		Code:          functionCodeLocation(fn),
	}, nil
}

func (a *Adapter) GetFunctionConfiguration(ctx context.Context, in *gen.GetFunctionConfigurationRequest) (*gen.FunctionConfiguration, error) {
	name := in.FunctionName
	fn, err := a.s.DescribeFunction(ctx, name)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return functionToAWS(fn), nil
}

func (a *Adapter) DeleteFunction(ctx context.Context, in *gen.DeleteFunctionRequest) (*gen.DeleteFunctionResponse, error) {
	name := in.FunctionName
	if err := a.s.DeleteFunction(ctx, name); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.DeleteFunctionResponse{}, nil
}

func (a *Adapter) ListFunctions(ctx context.Context, in *gen.ListFunctionsRequest) (*gen.ListFunctionsResponse, error) {
	res, err := a.s.ListFunctions(ctx, domain.ListFunctionsOptions{})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.ListFunctionsResponse{}
	for _, fn := range res.Functions {
		out.Functions = append(out.Functions, *functionToAWS(fn))
	}
	return out, nil
}

func (a *Adapter) UpdateFunctionConfiguration(ctx context.Context, in *gen.UpdateFunctionConfigurationRequest) (*gen.FunctionConfiguration, error) {
	name := in.FunctionName
	opt := domain.UpdateFunctionOptions{}
	if in.MemorySize != nil {
		opt.MemoryBytes = int64(*in.MemorySize) * 1024 * 1024
	}
	if in.Timeout != nil {
		opt.TimeoutSeconds = int(*in.Timeout)
	}
	if in.Environment != nil && len(in.Environment.Variables) > 0 {
		opt.Environment = map[string]string(in.Environment.Variables)
	}
	if err := a.s.UpdateFunction(ctx, name, opt); err != nil {
		return nil, mapDomainErr(err)
	}
	fn, _ := a.s.DescribeFunction(ctx, name)
	return functionToAWS(fn), nil
}

func (a *Adapter) UpdateFunctionCode(ctx context.Context, in *gen.UpdateFunctionCodeRequest) (*gen.FunctionConfiguration, error) {
	name := in.FunctionName
	opt := domain.UpdateFunctionOptions{Image: strDeref(in.ImageUri)}
	if err := a.s.UpdateFunction(ctx, name, opt); err != nil {
		return nil, mapDomainErr(err)
	}
	fn, _ := a.s.DescribeFunction(ctx, name)
	return functionToAWS(fn), nil
}

// ---------------------------------------------------------------------
// Probe ops — return canonical "feature unset" envelopes. Real Lambda
// always carries these on a freshly-created function; the
// hashicorp/aws aws_lambda_function importer calls them during Read.
// ---------------------------------------------------------------------

func (a *Adapter) ListVersionsByFunction(ctx context.Context, in *gen.ListVersionsByFunctionRequest) (*gen.ListVersionsByFunctionResponse, error) {
	name := in.FunctionName
	fn, err := a.s.DescribeFunction(ctx, name)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	cfg := functionToAWS(fn)
	cfg.Version = strPtr("$LATEST")
	return &gen.ListVersionsByFunctionResponse{
		Versions: gen.FunctionList{*cfg},
	}, nil
}

func (a *Adapter) GetFunctionUrlConfig(ctx context.Context, in *gen.GetFunctionUrlConfigRequest) (*gen.GetFunctionUrlConfigResponse, error) {
	return nil, &awsjson.BackendError{
		HTTPStatus: http.StatusNotFound,
		Type:       "ResourceNotFoundException",
		Message:    "function URL config does not exist",
	}
}

func (a *Adapter) ListTags(ctx context.Context, in *gen.ListTagsRequest) (*gen.ListTagsResponse, error) {
	// Lambda's ListTags is tolerant of "no such function" — returns an
	// empty tag set. Probe DescribeFunction to confirm the function
	// exists, but swallow the error (real Lambda doesn't surface it).
	_, _ = a.s.DescribeFunction(ctx, nameFromARN(in.Resource))
	return &gen.ListTagsResponse{Tags: gen.Tags{}}, nil
}

func (a *Adapter) GetPolicy(ctx context.Context, in *gen.GetPolicyRequest) (*gen.GetPolicyResponse, error) {
	return nil, &awsjson.BackendError{
		HTTPStatus: http.StatusNotFound,
		Type:       "ResourceNotFoundException",
		Message:    "resource policy does not exist for this function",
	}
}

func (a *Adapter) GetFunctionConcurrency(ctx context.Context, in *gen.GetFunctionConcurrencyRequest) (*gen.GetFunctionConcurrencyResponse, error) {
	return &gen.GetFunctionConcurrencyResponse{}, nil
}

func (a *Adapter) GetFunctionEventInvokeConfig(ctx context.Context, in *gen.GetFunctionEventInvokeConfigRequest) (*gen.FunctionEventInvokeConfig, error) {
	return nil, &awsjson.BackendError{
		HTTPStatus: http.StatusNotFound,
		Type:       "ResourceNotFoundException",
		Message:    "event invoke config does not exist",
	}
}

func (a *Adapter) GetFunctionCodeSigningConfig(ctx context.Context, in *gen.GetFunctionCodeSigningConfigRequest) (*gen.GetFunctionCodeSigningConfigResponse, error) {
	return &gen.GetFunctionCodeSigningConfigResponse{}, nil
}
