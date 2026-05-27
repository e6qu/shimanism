// Sockerless lane for the functions service. See docs/sockerless-validation.md.
package conformance_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	runapi "google.golang.org/api/run/v2"

	functionsdomain "github.com/e6qu/shimanism/internal/functions/domain"
	"github.com/e6qu/shimanism/internal/harness"
	awsbackend "github.com/e6qu/shimanism/services/functions/backends/aws"
	azurefunctions "github.com/e6qu/shimanism/services/functions/backends/azure"
	gcpfunctions "github.com/e6qu/shimanism/services/functions/backends/gcp"
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

// TestSockerless_Azure_Functions_ContainerApps_CRUD exercises the
// shim's Azure Container Apps functions backend against sockerless's
// Microsoft.App/containerApps ARM control plane: CreateFunction →
// DescribeFunction → ListFunctions → DeleteFunction.
//
// Unlike the pure-control-plane sockerless lanes, sockerless's
// Container Apps handler actually invokes the local container runtime
// to start a replica — matching real Azure's behavior, where the
// underlying execution is opaque to the caller. That means this lane
// needs a docker/podman daemon AND the requested image must be
// reachable. Set SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE to a known-
// pullable image (e.g. an image you've pre-pulled into the daemon)
// to opt in. Default-skipped because requiring an outbound pull
// during conformance is too much CI variance for the bundled lane.
func TestSockerless_Azure_Functions_ContainerApps_CRUD(t *testing.T) {
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	image := os.Getenv("SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE")
	if image == "" {
		t.Skip("SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE not set; pre-pull an image into the local container runtime and export the reference to opt in.")
	}
	subscription := sockerlessAzureSubscriptionFunctions()
	envID := "/subscriptions/" + subscription +
		"/resourceGroups/shim-sk-rg/providers/Microsoft.App/managedEnvironments/shim-sk-env"
	backend, err := azurefunctions.New(azurefunctions.Config{
		SubscriptionID:       subscription,
		ResourceGroup:        "shim-sk-rg",
		Location:             "eastus",
		ManagedEnvironmentID: envID,
		Credential:           sockerlessNoOpCredentialFunctions{},
		ClientOptions:        sockerlessARMClientOptionsFunctions(port),
	})
	if err != nil {
		t.Fatalf("azurefunctions.New: %v", err)
	}
	ctx := context.Background()

	name := "shim-sk-fn-" + sockerlessHex8()
	fn, err := backend.CreateFunction(ctx, name, functionsdomain.CreateFunctionOptions{
		Image:          image,
		MemoryBytes:    512 << 20,
		CPUMilliCores:  250,
		TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
	if fn.Name != name {
		t.Errorf("CreateFunction.Name = %q, want %q", fn.Name, name)
	}
	t.Cleanup(func() { _ = backend.DeleteFunction(ctx, name) })

	got, err := backend.DescribeFunction(ctx, name)
	if err != nil {
		t.Fatalf("DescribeFunction: %v", err)
	}
	if got.Name != name {
		t.Errorf("DescribeFunction.Name = %q, want %q", got.Name, name)
	}

	list, err := backend.ListFunctions(ctx, functionsdomain.ListFunctionsOptions{})
	if err != nil {
		t.Fatalf("ListFunctions: %v", err)
	}
	found := false
	for _, f := range list.Functions {
		if f.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListFunctions did not contain %q", name)
	}
}

func sockerlessARMClientOptionsFunctions(port string) *arm.ClientOptions {
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "127.0.0.1:"+port)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://localhost:" + port + "/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {
						Audience: "https://management.azure.com",
						Endpoint: "https://localhost:" + port,
					},
				},
			},
			Transport: &http.Client{Transport: transport},
		},
	}
}

func sockerlessAzureSubscriptionFunctions() string {
	if s := os.Getenv("SOCKERLESS_AZURE_SUBSCRIPTION"); s != "" {
		return s
	}
	return "00000000-0000-0000-0000-000000000000"
}

type sockerlessNoOpCredentialFunctions struct{}

var sockerlessFarFutureFunctions = time.Date(2099, time.December, 31, 23, 59, 59, 0, time.UTC)

func (sockerlessNoOpCredentialFunctions) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "sockerless-test", ExpiresOn: sockerlessFarFutureFunctions}, nil
}

// TestSockerless_GCP_CloudRun_CRUD exercises the shim's GCP Cloud Run
// functions backend against sockerless's Cloud Run v2 service surface
// (`/v2/projects/{project}/locations/{location}/services`):
// CreateFunction → DescribeFunction → ListFunctions → DeleteFunction.
//
// Like Azure Container Apps, sockerless's Cloud Run handler does real
// container execution — `SIM_GCP_CPU_QUOTA_PER_REGION` quota check
// then `StartHTTPContainer` to spawn a real replica. Set
// `SOCKERLESS_GCP_CLOUDRUN_IMAGE` to a pullable image reference
// (scripts/run-sockerless-storage.sh defaults to nginx:alpine and
// pre-pulls it). Default-skipped if the env var isn't set.
func TestSockerless_GCP_CloudRun_CRUD(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	image := os.Getenv("SOCKERLESS_GCP_CLOUDRUN_IMAGE")
	if image == "" {
		t.Skip("SOCKERLESS_GCP_CLOUDRUN_IMAGE not set; pre-pull an image and export the reference to opt in.")
	}
	ctx := context.Background()
	svc, err := runapi.NewService(ctx,
		option.WithEndpoint("http://"+endpoint+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("cloud run client: %v", err)
	}
	project := os.Getenv("SOCKERLESS_GCP_PROJECT")
	if project == "" {
		project = "shim-sockerless"
	}
	backend := gcpfunctions.New(svc, gcpfunctions.Config{ProjectID: project, Region: "us-central1"})

	name := "shim-sk-cr-" + sockerlessHex8()
	fn, err := backend.CreateFunction(ctx, name, functionsdomain.CreateFunctionOptions{
		Image:          image,
		MemoryBytes:    256 << 20,
		CPUMilliCores:  250,
		TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
	if fn.Name != name {
		t.Errorf("CreateFunction.Name = %q, want %q", fn.Name, name)
	}
	t.Cleanup(func() { _ = backend.DeleteFunction(ctx, name) })

	got, err := backend.DescribeFunction(ctx, name)
	if err != nil {
		t.Fatalf("DescribeFunction: %v", err)
	}
	if got.Name != name {
		t.Errorf("DescribeFunction.Name = %q, want %q", got.Name, name)
	}

	list, err := backend.ListFunctions(ctx, functionsdomain.ListFunctionsOptions{})
	if err != nil {
		t.Fatalf("ListFunctions: %v", err)
	}
	found := false
	for _, f := range list.Functions {
		if f.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListFunctions did not contain %q", name)
	}
}

// gcpHS256Bearer mints a test-mode HS256 JWT that the shim's
// gcpbearer middleware accepts in test mode.
func gcpHS256Bearer(t *testing.T, audience string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT","kid":"shim-test"}`))
	payloadJSON := []byte(`{"aud":"` + audience + `","exp":4102444800,"iat":1}`)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, []byte("test-key-do-not-use-in-prod"))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

type gcpStaticTokenSource struct{ token string }

func (s gcpStaticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.token, TokenType: "Bearer"}, nil
}

// TestSockerless_GCPCloudRunFrontendToAWSBackend_CRUD is the
// reverse-direction through-shim cell: GCP Cloud Run SDK drives
// the shim's GCP functions frontend, which routes through the
// shim's AWS Lambda backend, which targets sockerless's AWS sim.
// BUG-24 reverse-direction coverage. Cross-cloud cell, complement
// of the existing same-cloud AWS Lambda forward cell.
func TestSockerless_GCPCloudRunFrontendToAWSBackend_CRUD(t *testing.T) {
	awsEndpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if awsEndpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}
	ctx := context.Background()

	lambdaClient := newSockerlessLambdaBackendClient(t, awsEndpoint)
	backend := awsbackend.New(lambdaClient, awsbackend.Config{
		ExecutionRoleARN: "arn:aws:iam::000000000000:role/lambda-execution",
	})
	srv := harness.StartFunctionsServerGCP(t, backend)

	svc, err := runapi.NewService(ctx,
		option.WithEndpoint(srv.URL+"/"),
		option.WithTokenSource(gcpStaticTokenSource{token: gcpHS256Bearer(t, "https://run.googleapis.com/")}),
	)
	if err != nil {
		t.Fatalf("cloud run client: %v", err)
	}

	project := "shim-sockerless"
	region := "us-central1"
	parent := "projects/" + project + "/locations/" + region
	name := "shim-sk-rev-cr-" + sockerlessHex8()
	fullName := parent + "/services/" + name

	createReq := &runapi.GoogleCloudRunV2Service{
		Template: &runapi.GoogleCloudRunV2RevisionTemplate{
			Containers: []*runapi.GoogleCloudRunV2Container{{
				Image: "docker.io/library/hello-world:latest",
			}},
		},
	}
	if _, err := svc.Projects.Locations.Services.Create(parent, createReq).ServiceId(name).Do(); err != nil {
		t.Fatalf("Create through shim: %v", err)
	}
	t.Cleanup(func() { _, _ = svc.Projects.Locations.Services.Delete(fullName).Do() })

	if _, err := svc.Projects.Locations.Services.Get(fullName).Do(); err != nil {
		t.Fatalf("Get through shim: %v", err)
	}

	list, err := svc.Projects.Locations.Services.List(parent).Do()
	if err != nil {
		t.Fatalf("List through shim: %v", err)
	}
	found := false
	for _, s := range list.Services {
		if s.Name == fullName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("List did not contain %q", fullName)
	}
}
