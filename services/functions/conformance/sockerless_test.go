// Sockerless lane for the functions service. See doc/SOCKERLESS_VALIDATION.md.
package conformance_test

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"os"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/e6qu/shimanism/internal/harness"
	awsbackend "github.com/e6qu/shimanism/services/functions/backends/aws"
)

// TestSockerless_AWSLambdaFrontendToAWSBackend_CRUD drives the full
// through-shim E2E path for functions:
// aws-sdk-go-v2 Lambda client → AWS-shaped shim frontend → AWS Lambda
// backend → sockerless AWS simulator.
//
// Functions intentionally use the AWS destination in this lane:
// Lambda's required Role field is AWS-specific and the GCP/Azure
// function backends reject it rather than silently discarding it.
func TestSockerless_AWSLambdaFrontendToAWSBackend_CRUD(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		endpoint = os.Getenv("SOCKERLESS_AWS_SM_ENDPOINT")
	}
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}
	ctx := context.Background()
	backendClient := newSockerlessLambdaBackendClient(t, endpoint)
	backend := awsbackend.New(backendClient, awsbackend.Config{
		ExecutionRoleARN: "arn:aws:iam::000000000000:role/lambda-execution",
	})
	srv := harness.StartFunctionsServerAWS(t, backend)
	client := newSockerlessLambdaSourceClient(t, srv.URL)

	name := "shim-sk-fn-" + sockerlessHex8()
	create, err := client.CreateFunction(ctx, &lambda.CreateFunctionInput{
		FunctionName: awsapi.String(name),
		PackageType:  lambdatypes.PackageTypeImage,
		Code: &lambdatypes.FunctionCode{
			ImageUri: awsapi.String("docker.io/library/hello-world:latest"),
		},
		Role:       awsapi.String("arn:aws:iam::000000000000:role/lambda-execution"),
		MemorySize: awsapi.Int32(128),
		Timeout:    awsapi.Int32(3),
	})
	if err != nil {
		t.Fatalf("CreateFunction through shim: %v", err)
	}
	if awsapi.ToString(create.FunctionName) != name {
		t.Errorf("CreateFunction.FunctionName = %q, want %q", awsapi.ToString(create.FunctionName), name)
	}
	t.Cleanup(func() {
		_, _ = client.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
			FunctionName: awsapi.String(name),
		})
	})

	get, err := client.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: awsapi.String(name),
	})
	if err != nil {
		t.Fatalf("GetFunction through shim: %v", err)
	}
	if get.Configuration == nil || awsapi.ToString(get.Configuration.FunctionName) != name {
		t.Fatalf("GetFunction.Configuration.FunctionName = %+v, want %q", get.Configuration, name)
	}

	if _, err := client.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
		FunctionName: awsapi.String(name),
		MemorySize:   awsapi.Int32(256),
	}); err != nil {
		t.Fatalf("UpdateFunctionConfiguration through shim: %v", err)
	}

	list, err := client.ListFunctions(ctx, &lambda.ListFunctionsInput{})
	if err != nil {
		t.Fatalf("ListFunctions through shim: %v", err)
	}
	found := false
	for _, fn := range list.Functions {
		if awsapi.ToString(fn.FunctionName) == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListFunctions did not contain %q", name)
	}
}

func newSockerlessLambdaBackendClient(t *testing.T, endpoint string) *lambda.Client {
	t.Helper()
	cfg := sockerlessLambdaAWSConfig(t)
	if os.Getenv("AWS_S3_CONFORMANCE_INSECURE_TLS") == "1" {
		cfg.HTTPClient = awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		})
	}
	return lambda.NewFromConfig(cfg, func(o *lambda.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
}

func newSockerlessLambdaSourceClient(t *testing.T, endpoint string) *lambda.Client {
	t.Helper()
	cfg := sockerlessLambdaAWSConfig(t)
	return lambda.NewFromConfig(cfg, func(o *lambda.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
}

func sockerlessLambdaAWSConfig(t *testing.T) awsapi.Config {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func sockerlessHex8() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
