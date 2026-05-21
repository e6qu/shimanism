// Matrix conformance for the functions service. inmem cell green;
// other cells gated on env vars.
package conformance_test

import (
	"context"
	"fmt"
	"strings"
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
			// Role is AWS Lambda-specific. The AWS Lambda SDK enforces
			// Role as a required field client-side, so the request
			// must carry one. The AWS frontend forwards Role through
			// to the domain; non-AWS backends honestly reject non-
			// empty Role with InvalidArgument (per
			// services/functions/APPLY_INTERSECTION.md). For non-AWS
			// cells the matrix therefore asserts the honest-reject
			// behaviour and stops there — the cross-cloud CreateFunction
			// path with a Role is intersection-out.
			input := &lambda.CreateFunctionInput{
				FunctionName: awsapi.String(name),
				PackageType:  lambdatypes.PackageTypeImage,
				Code: &lambdatypes.FunctionCode{
					ImageUri: awsapi.String("gcr.io/knative-samples/helloworld-go"),
				},
				MemorySize: awsapi.Int32(128),
				Timeout:    awsapi.Int32(60),
				Role:       awsapi.String("arn:aws:iam::000000000000:role/lambda"),
			}
			_, err = client.CreateFunction(ctx, input)
			if f.Name != "inmem" && f.Name != "aws" {
				if err == nil {
					t.Fatalf("%s: expected InvalidParameterValueException for Role on non-AWS backend; got success", f.Name)
				}
				if !strings.Contains(err.Error(), "InvalidParameterValueException") {
					t.Fatalf("%s: expected InvalidParameterValueException, got: %v", f.Name, err)
				}
				return
			}
			if err != nil {
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
