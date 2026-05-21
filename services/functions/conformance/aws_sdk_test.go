// Phase 7 conformance: AWS Lambda-shaped frontend exercised by
// `aws-sdk-go-v2/service/lambda`. Verifies Create → Describe (polls
// until Active) → Update → Delete lifecycle via the restJson1 wire
// protocol against the in-mem backend.
package conformance_test

import (
	"context"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/functions/backends/inmem"
)

func TestAWSSDK_FunctionsLifecycle(t *testing.T) {
	srv := harness.StartFunctionsServerAWS(t, inmem.New())
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
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
	client := lambda.NewFromConfig(cfg, func(o *lambda.Options) {
		o.BaseEndpoint = awsapi.String(srv.URL)
	})

	if _, err := client.CreateFunction(ctx, &lambda.CreateFunctionInput{
		FunctionName: awsapi.String("hello-shim"),
		PackageType:  lambdatypes.PackageTypeImage,
		Code: &lambdatypes.FunctionCode{
			ImageUri: awsapi.String("docker.io/library/hello-world:latest"),
		},
		Role:       awsapi.String("arn:aws:iam::000000000000:role/lambda-execution"),
		MemorySize: awsapi.Int32(128),
		Timeout:    awsapi.Int32(3),
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var active bool
	for time.Now().Before(deadline) {
		out, err := client.GetFunctionConfiguration(ctx, &lambda.GetFunctionConfigurationInput{
			FunctionName: awsapi.String("hello-shim"),
		})
		if err != nil {
			t.Fatalf("GetFunctionConfiguration: %v", err)
		}
		if out.State == lambdatypes.StateActive {
			active = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !active {
		t.Fatal("function never reached Active state")
	}

	if _, err := client.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
		FunctionName: awsapi.String("hello-shim"),
		MemorySize:   awsapi.Int32(256),
	}); err != nil {
		t.Errorf("UpdateFunctionConfiguration: %v", err)
	}

	if _, err := client.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
		FunctionName: awsapi.String("hello-shim"),
		ImageUri:     awsapi.String("docker.io/library/hello-world:v2"),
	}); err != nil {
		t.Errorf("UpdateFunctionCode: %v", err)
	}

	if _, err := client.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
		FunctionName: awsapi.String("hello-shim"),
	}); err != nil {
		t.Errorf("DeleteFunction: %v", err)
	}
}
