// Matrix conformance for the functions service. inmem cell green;
// other cells gated on env vars.
package conformance_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/functions/conformance"
)

func TestFunctionsMatrix_AWSFrontend(t *testing.T) {
	ctx := context.Background()
	for _, f := range conformance.ActiveBackends() {
		t.Run(f.Name, func(t *testing.T) {
			be := f.Fn(t)
			srv := harness.StartFunctionsServerAWS(t, be)
			cfg, err := awsconfig.LoadDefaultConfig(ctx,
				awsconfig.WithRegion("us-east-1"),
				awsconfig.WithCredentialsProvider(awsapi.AnonymousCredentials{}),
			)
			if err != nil {
				t.Fatalf("aws config: %v", err)
			}
			client := lambda.NewFromConfig(cfg, func(o *lambda.Options) {
				o.BaseEndpoint = awsapi.String(srv.URL)
			})

			name := fmt.Sprintf("matrix-aws-%s", f.Name)
			// Knative needs a real HTTP-serving container to reach
			// Ready. helloworld-go listens on $PORT and replies;
			// docker.io/library/hello-world exits immediately so the
			// Knative Pod is never healthy and Service never Ready.
			//
			// Role is AWS Lambda-specific. The AWS frontend takes it
			// from the request body and passes it through to the
			// domain; non-AWS backends reject non-empty Role with
			// InvalidArgument (per services/functions/APPLY_INTERSECTION
			// .md). The matrix exercises the AWS *frontend* against
			// every backend, so we omit Role for non-AWS cells. The
			// inmem + aws cells receive it.
			input := &lambda.CreateFunctionInput{
				FunctionName: awsapi.String(name),
				PackageType:  lambdatypes.PackageTypeImage,
				Code: &lambdatypes.FunctionCode{
					ImageUri: awsapi.String("gcr.io/knative-samples/helloworld-go"),
				},
				MemorySize: awsapi.Int32(128),
				Timeout:    awsapi.Int32(60),
			}
			if f.Name == "inmem" || f.Name == "aws" {
				input.Role = awsapi.String("arn:aws:iam::000000000000:role/lambda")
			}
			if _, err := client.CreateFunction(ctx, input); err != nil {
				t.Fatalf("CreateFunction: %v", err)
			}
			t.Cleanup(func() {
				_, _ = client.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
					FunctionName: awsapi.String(name),
				})
			})

			budget := 2 * time.Second
			if f.Name != "inmem" {
				budget = 10 * time.Minute
			}
			deadline := time.Now().Add(budget)
			var active bool
			for time.Now().Before(deadline) {
				out, err := client.GetFunctionConfiguration(ctx, &lambda.GetFunctionConfigurationInput{
					FunctionName: awsapi.String(name),
				})
				if err != nil {
					t.Fatalf("GetFunctionConfiguration: %v", err)
				}
				if out.State == lambdatypes.StateActive {
					active = true
					break
				}
				time.Sleep(time.Second)
			}
			if !active {
				t.Fatalf("%s: function never reached Active state", f.Name)
			}
		})
	}
}
