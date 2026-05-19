// Package conformance hosts the backend factories used by the
// parameterised matrix tests in this directory. The test file
// lives in `package conformance_test`; this package is the
// importable side.
package conformance

import (
	"context"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwapi "google.golang.org/api/apigateway/v1"
	"google.golang.org/api/option"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
	awsbackend "github.com/e6qu/shimanism/services/apigateway/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/apigateway/backends/azure"
	envoybackend "github.com/e6qu/shimanism/services/apigateway/backends/envoy"
	gcpbackend "github.com/e6qu/shimanism/services/apigateway/backends/gcp"
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

// BackendFactory returns an APIGateway backend ready for use. Each
// factory may call t.Skip if required infrastructure isn't there.
type BackendFactory struct {
	Name string
	Fn   func(t *testing.T) domain.APIGateway
}

// ActiveBackends returns the set of backend factories. Inmem
// always runs; the others skip absent env wiring.
func ActiveBackends() []BackendFactory {
	return []BackendFactory{
		{Name: "inmem", Fn: NewInmem},
		{Name: "envoy", Fn: NewEnvoy},
		{Name: "aws", Fn: NewAWS},
		{Name: "gcp", Fn: NewGCP},
		{Name: "azure", Fn: NewAzure},
	}
}

func NewInmem(t *testing.T) domain.APIGateway {
	t.Helper()
	return inmem.New()
}

// NewEnvoy needs a running K8s cluster with Gateway API CRDs +
// Envoy Gateway installed. Set ENVOY_CONFORMANCE=1 to opt in; CI's
// conformance-envoy job sets it after kind+Envoy install.
func NewEnvoy(t *testing.T) domain.APIGateway {
	t.Helper()
	if os.Getenv("ENVOY_CONFORMANCE") != "1" {
		t.Skip("ENVOY_CONFORMANCE != 1 (envoy backend conformance disabled)")
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("KUBECONFIG not set (envoy backend conformance disabled)")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("build kubeconfig: %v", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("dynamic client: %v", err)
	}
	ns := os.Getenv("ENVOY_NAMESPACE")
	if ns == "" {
		ns = "default"
	}
	return envoybackend.New(dyn, envoybackend.Config{Namespace: ns, GatewayClassName: "eg"})
}

// NewAWS connects to real AWS API Gateway v2 when
// AWS_APIGATEWAY_CONFORMANCE=1 is set + AWS_REGION/credentials are
// in the environment. Track A backends.
func NewAWS(t *testing.T) domain.APIGateway {
	t.Helper()
	if os.Getenv("AWS_APIGATEWAY_CONFORMANCE") != "1" {
		t.Skip("AWS_APIGATEWAY_CONFORMANCE != 1 (aws backend conformance disabled)")
	}
	cfg, err := awscfg.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	opts := []func(*apigatewayv2.Options){}
	if ep := os.Getenv("AWS_APIGATEWAY_ENDPOINT"); ep != "" {
		opts = append(opts, func(o *apigatewayv2.Options) {
			o.BaseEndpoint = awsapi.String(ep)
		})
	}
	return awsbackend.New(apigatewayv2.NewFromConfig(cfg, opts...))
}

// NewGCP connects to real GCP API Gateway when
// GCP_APIGATEWAY_CONFORMANCE=1 + GCP_PROJECT_ID are set.
func NewGCP(t *testing.T) domain.APIGateway {
	t.Helper()
	if os.Getenv("GCP_APIGATEWAY_CONFORMANCE") != "1" {
		t.Skip("GCP_APIGATEWAY_CONFORMANCE != 1 (gcp backend conformance disabled)")
	}
	project := os.Getenv("GCP_PROJECT_ID")
	if project == "" {
		t.Skip("GCP_PROJECT_ID not set (gcp backend conformance disabled)")
	}
	svc, err := apigwapi.NewService(context.Background(), option.WithUserAgent("shimanism-conformance"))
	if err != nil {
		t.Fatalf("gcp apigateway service: %v", err)
	}
	region := os.Getenv("GCP_REGION")
	if region == "" {
		region = "us-central1"
	}
	return gcpbackend.New(svc, gcpbackend.Config{ProjectID: project, Region: region})
}

// NewAzure connects to real Azure API Management when
// AZURE_APIM_CONFORMANCE=1 + subscription/RG/service env vars set.
func NewAzure(t *testing.T) domain.APIGateway {
	t.Helper()
	if os.Getenv("AZURE_APIM_CONFORMANCE") != "1" {
		t.Skip("AZURE_APIM_CONFORMANCE != 1 (azure backend conformance disabled)")
	}
	sub := os.Getenv("AZURE_SUBSCRIPTION_ID")
	rg := os.Getenv("AZURE_RESOURCE_GROUP")
	svc := os.Getenv("AZURE_APIM_SERVICE")
	if sub == "" || rg == "" || svc == "" {
		t.Skip("AZURE_SUBSCRIPTION_ID/AZURE_RESOURCE_GROUP/AZURE_APIM_SERVICE not all set")
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("azure cred: %v", err)
	}
	b, err := azurebackend.New(azurebackend.Config{
		SubscriptionID: sub,
		ResourceGroup:  rg,
		ServiceName:    svc,
		Credential:     cred,
	})
	if err != nil {
		t.Fatalf("azure backend: %v", err)
	}
	return b
}
