// Package conformance hosts the backend factories used by the
// parameterised rdbms conformance tests.
package conformance

import (
	"context"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
	awsbackend "github.com/e6qu/shimanism/services/rdbms/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/rdbms/backends/azure"
	"github.com/e6qu/shimanism/services/rdbms/backends/cnpg"
	gcpbackend "github.com/e6qu/shimanism/services/rdbms/backends/gcp"
	"github.com/e6qu/shimanism/services/rdbms/backends/inmem"
)

type BackendFactory struct {
	Name string
	Fn   func(t *testing.T) domain.RDBMS
}

// ActiveBackends lists every backend factory the matrix test
// iterates. Each factory internally `t.Skip`s when its
// infrastructure isn't available.
func ActiveBackends() []BackendFactory {
	return []BackendFactory{
		{Name: "inmem", Fn: NewInmem},
		{Name: "cnpg", Fn: NewCNPG},
		{Name: "aws", Fn: NewAWS},
		{Name: "gcp", Fn: NewGCP},
		{Name: "azure", Fn: NewAzure},
	}
}

func NewInmem(t *testing.T) domain.RDBMS {
	t.Helper()
	return inmem.New()
}

// NewCNPG connects to a Kubernetes cluster via KUBECONFIG and
// expects the CloudNativePG operator to be installed (CRDs +
// controller). CI's conformance-cnpg lane starts kind +
// CloudNativePG operator + sets KUBECONFIG.
func NewCNPG(t *testing.T) domain.RDBMS {
	t.Helper()
	if os.Getenv("KUBECONFIG") == "" {
		t.Skip("KUBECONFIG not set (CloudNativePG backend conformance disabled)")
	}
	if os.Getenv("CNPG_CONFORMANCE") != "1" {
		t.Skip("CNPG_CONFORMANCE!=1 (CloudNativePG backend conformance disabled)")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		t.Fatalf("build kubeconfig: %v", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("new dynamic client: %v", err)
	}
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("new kubernetes client: %v", err)
	}
	ns := os.Getenv("CNPG_NAMESPACE")
	if ns == "" {
		ns = "default"
	}
	return cnpg.New(dyn, core, cnpg.Config{Namespace: ns})
}

func NewAWS(t *testing.T) domain.RDBMS {
	t.Helper()
	if os.Getenv("AWS_RDS_CONFORMANCE") != "1" {
		t.Skip("AWS_RDS_CONFORMANCE!=1 (AWS RDS backend conformance disabled)")
	}
	cfg, err := awscfg.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	opts := []func(*rds.Options){}
	if endpoint := os.Getenv("AWS_RDS_ENDPOINT"); endpoint != "" {
		opts = append(opts, func(o *rds.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}
	return awsbackend.New(rds.NewFromConfig(cfg, opts...))
}

func NewGCP(t *testing.T) domain.RDBMS {
	t.Helper()
	if os.Getenv("GCP_CLOUDSQL_CONFORMANCE") != "1" {
		t.Skip("GCP_CLOUDSQL_CONFORMANCE!=1 (GCP Cloud SQL backend conformance disabled)")
	}
	project := os.Getenv("GCP_PROJECT_ID")
	if project == "" {
		t.Skip("GCP_PROJECT_ID not set")
	}
	svc, err := sqladmin.NewService(context.Background(), option.WithUserAgent("shimanism-rdbms-conformance"))
	if err != nil {
		t.Fatalf("new sqladmin service: %v", err)
	}
	return gcpbackend.New(svc, gcpbackend.Config{ProjectID: project})
}

func NewAzure(t *testing.T) domain.RDBMS {
	t.Helper()
	sub := os.Getenv("AZURE_SUBSCRIPTION_ID")
	rg := os.Getenv("AZURE_RESOURCE_GROUP")
	if sub == "" || rg == "" {
		t.Skip("AZURE_SUBSCRIPTION_ID + AZURE_RESOURCE_GROUP not set (Azure rdbms backend disabled)")
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("new Azure credential: %v", err)
	}
	b, err := azurebackend.New(azurebackend.Config{
		SubscriptionID: sub,
		ResourceGroup:  rg,
		Credential:     cred,
	})
	if err != nil {
		t.Fatalf("new Azure rdbms backend: %v", err)
	}
	return b
}
