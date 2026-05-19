// Package conformance hosts the backend factories used by the
// parameterised cache conformance tests.
package conformance

import (
	"context"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"google.golang.org/api/option"
	redisapi "google.golang.org/api/redis/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/e6qu/shimanism/internal/cache/domain"
	awsbackend "github.com/e6qu/shimanism/services/cache/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/cache/backends/azure"
	gcpbackend "github.com/e6qu/shimanism/services/cache/backends/gcp"
	"github.com/e6qu/shimanism/services/cache/backends/inmem"
	"github.com/e6qu/shimanism/services/cache/backends/redisop"
)

type BackendFactory struct {
	Name string
	Fn   func(t *testing.T) domain.Cache
}

func ActiveBackends() []BackendFactory {
	return []BackendFactory{
		{Name: "inmem", Fn: NewInmem},
		{Name: "redisop", Fn: NewRedisOp},
		{Name: "aws", Fn: NewAWS},
		{Name: "gcp", Fn: NewGCP},
		{Name: "azure", Fn: NewAzure},
	}
}

func NewInmem(t *testing.T) domain.Cache {
	t.Helper()
	return inmem.New()
}

// NewRedisOp connects to a Kubernetes cluster via KUBECONFIG and
// expects the OT-CONTAINER-KIT Redis Operator to be installed.
// CI's conformance-redisop lane sets KUBECONFIG +
// REDISOP_CONFORMANCE=1.
func NewRedisOp(t *testing.T) domain.Cache {
	t.Helper()
	if os.Getenv("KUBECONFIG") == "" {
		t.Skip("KUBECONFIG not set (Redis Operator backend conformance disabled)")
	}
	if os.Getenv("REDISOP_CONFORMANCE") != "1" {
		t.Skip("REDISOP_CONFORMANCE!=1 (Redis Operator backend conformance disabled)")
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
	ns := os.Getenv("REDISOP_NAMESPACE")
	if ns == "" {
		ns = "default"
	}
	return redisop.New(dyn, core, redisop.Config{Namespace: ns})
}

func NewAWS(t *testing.T) domain.Cache {
	t.Helper()
	if os.Getenv("AWS_ELASTICACHE_CONFORMANCE") != "1" {
		t.Skip("AWS_ELASTICACHE_CONFORMANCE!=1 (AWS ElastiCache backend conformance disabled)")
	}
	cfg, err := awscfg.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	opts := []func(*elasticache.Options){}
	if endpoint := os.Getenv("AWS_ELASTICACHE_ENDPOINT"); endpoint != "" {
		opts = append(opts, func(o *elasticache.Options) {
			o.BaseEndpoint = awsapi.String(endpoint)
		})
	}
	return awsbackend.New(elasticache.NewFromConfig(cfg, opts...))
}

func NewGCP(t *testing.T) domain.Cache {
	t.Helper()
	if os.Getenv("GCP_MEMORYSTORE_CONFORMANCE") != "1" {
		t.Skip("GCP_MEMORYSTORE_CONFORMANCE!=1 (GCP Memorystore backend conformance disabled)")
	}
	project := os.Getenv("GCP_PROJECT_ID")
	if project == "" {
		t.Skip("GCP_PROJECT_ID not set")
	}
	svc, err := redisapi.NewService(context.Background(), option.WithUserAgent("shimanism-cache-conformance"))
	if err != nil {
		t.Fatalf("new redis service: %v", err)
	}
	return gcpbackend.New(svc, gcpbackend.Config{ProjectID: project})
}

func NewAzure(t *testing.T) domain.Cache {
	t.Helper()
	sub := os.Getenv("AZURE_SUBSCRIPTION_ID")
	rg := os.Getenv("AZURE_RESOURCE_GROUP")
	if sub == "" || rg == "" {
		t.Skip("AZURE_SUBSCRIPTION_ID + AZURE_RESOURCE_GROUP not set (Azure cache backend disabled)")
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
		t.Fatalf("new Azure cache backend: %v", err)
	}
	return b
}
