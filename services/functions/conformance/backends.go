// Package conformance hosts the backend factories.
package conformance

import (
	"context"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"google.golang.org/api/option"
	runapi "google.golang.org/api/run/v2"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/e6qu/shimanism/internal/functions/domain"
	awsbackend "github.com/e6qu/shimanism/services/functions/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/functions/backends/azure"
	gcpbackend "github.com/e6qu/shimanism/services/functions/backends/gcp"
	"github.com/e6qu/shimanism/services/functions/backends/inmem"
	"github.com/e6qu/shimanism/services/functions/backends/knative"
)

type BackendFactory struct {
	Name string
	Fn   func(t *testing.T) domain.Functions
}

func ActiveBackends() []BackendFactory {
	return []BackendFactory{
		{Name: "inmem", Fn: NewInmem},
		{Name: "knative", Fn: NewKnative},
		{Name: "aws", Fn: NewAWS},
		{Name: "gcp", Fn: NewGCP},
		{Name: "azure", Fn: NewAzure},
	}
}

func NewInmem(t *testing.T) domain.Functions {
	t.Helper()
	return inmem.New()
}

// NewKnative connects to a Kubernetes cluster via KUBECONFIG. CI's
// conformance-knative lane sets KUBECONFIG + KNATIVE_CONFORMANCE=1.
func NewKnative(t *testing.T) domain.Functions {
	t.Helper()
	if os.Getenv("KUBECONFIG") == "" {
		t.Skip("KUBECONFIG not set (Knative backend disabled)")
	}
	if os.Getenv("KNATIVE_CONFORMANCE") != "1" {
		t.Skip("KNATIVE_CONFORMANCE!=1 (Knative backend disabled)")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		t.Fatalf("build kubeconfig: %v", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("new dynamic client: %v", err)
	}
	ns := os.Getenv("KNATIVE_NAMESPACE")
	if ns == "" {
		ns = "default"
	}
	return knative.New(dyn, knative.Config{Namespace: ns})
}

func NewAWS(t *testing.T) domain.Functions {
	t.Helper()
	if os.Getenv("AWS_LAMBDA_CONFORMANCE") != "1" {
		t.Skip("AWS_LAMBDA_CONFORMANCE!=1 (AWS Lambda backend disabled)")
	}
	role := os.Getenv("AWS_LAMBDA_ROLE_ARN")
	if role == "" {
		t.Skip("AWS_LAMBDA_ROLE_ARN not set")
	}
	cfg, err := awscfg.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	opts := []func(*lambda.Options){}
	if endpoint := os.Getenv("AWS_LAMBDA_ENDPOINT"); endpoint != "" {
		opts = append(opts, func(o *lambda.Options) {
			o.BaseEndpoint = awsapi.String(endpoint)
		})
	}
	return awsbackend.New(lambda.NewFromConfig(cfg, opts...), awsbackend.Config{ExecutionRoleARN: role})
}

func NewGCP(t *testing.T) domain.Functions {
	t.Helper()
	if os.Getenv("GCP_CLOUDRUN_CONFORMANCE") != "1" {
		t.Skip("GCP_CLOUDRUN_CONFORMANCE!=1 (Cloud Run backend disabled)")
	}
	project := os.Getenv("GCP_PROJECT_ID")
	if project == "" {
		t.Skip("GCP_PROJECT_ID not set")
	}
	svc, err := runapi.NewService(context.Background(), option.WithUserAgent("shimanism-functions-conformance"))
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}
	return gcpbackend.New(svc, gcpbackend.Config{ProjectID: project})
}

func NewAzure(t *testing.T) domain.Functions {
	t.Helper()
	sub := os.Getenv("AZURE_SUBSCRIPTION_ID")
	rg := os.Getenv("AZURE_RESOURCE_GROUP")
	env := os.Getenv("AZURE_CONTAINERAPP_ENV_ID")
	if sub == "" || rg == "" || env == "" {
		t.Skip("AZURE_SUBSCRIPTION_ID + AZURE_RESOURCE_GROUP + AZURE_CONTAINERAPP_ENV_ID not set")
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("azure credential: %v", err)
	}
	b, err := azurebackend.New(azurebackend.Config{
		SubscriptionID:       sub,
		ResourceGroup:        rg,
		ManagedEnvironmentID: env,
		Credential:           cred,
	})
	if err != nil {
		t.Fatalf("new Azure functions backend: %v", err)
	}
	return b
}
