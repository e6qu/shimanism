// cache subcommand wiring.

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

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
	awsecfront "github.com/e6qu/shimanism/internal/cache/frontends/aws_elasticache"
	azureredisfront "github.com/e6qu/shimanism/internal/cache/frontends/azure_redis"
	gcpmsfront "github.com/e6qu/shimanism/internal/cache/frontends/gcp_memorystore"
	awsbackend "github.com/e6qu/shimanism/services/cache/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/cache/backends/azure"
	gcpbackend "github.com/e6qu/shimanism/services/cache/backends/gcp"
	"github.com/e6qu/shimanism/services/cache/backends/inmem"
	"github.com/e6qu/shimanism/services/cache/backends/redisop"
)

func runCache(args []string) error {
	fs := flag.NewFlagSet("cache", flag.ContinueOnError)
	addr := fs.String("addr", ":9500", "address to listen on")
	frontendName := fs.String("frontend", "aws_elasticache",
		"frontend wire protocol: aws_elasticache, gcp_memorystore, azure_redis")
	backendName := fs.String("backend", "inmem",
		"backend: inmem, redisop, aws, gcp, azure")

	kubeconfig := fs.String("kubeconfig", envOr("KUBECONFIG", ""), "Path to kubeconfig (redisop backend)")
	redisopNS := fs.String("redisop-namespace", envOr("REDISOP_NAMESPACE", "default"), "Kubernetes namespace for Redis CRs")
	awsEndpoint := fs.String("aws-endpoint", envOr("AWS_ELASTICACHE_ENDPOINT", ""), "AWS ElastiCache endpoint override")
	gcpProject := fs.String("gcp-project", envOr("GCP_PROJECT_ID", ""), "GCP project ID")
	gcpRegion := fs.String("gcp-region", envOr("GCP_REGION", "us-central1"), "GCP region")
	azureSubscription := fs.String("azure-subscription", envOr("AZURE_SUBSCRIPTION_ID", ""), "Azure subscription ID")
	azureResourceGroup := fs.String("azure-resource-group", envOr("AZURE_RESOURCE_GROUP", ""), "Azure resource group")
	azureLocation := fs.String("azure-location", envOr("AZURE_LOCATION", "eastus"), "Azure location")

	if err := fs.Parse(args); err != nil {
		return err
	}

	backend, err := buildCacheBackend(*backendName, cacheBackendConfig{
		kubeconfig:        *kubeconfig,
		redisopNamespace:  *redisopNS,
		awsEndpoint:       *awsEndpoint,
		gcpProject:        *gcpProject,
		gcpRegion:         *gcpRegion,
		azureSubscription: *azureSubscription,
		azureRG:           *azureResourceGroup,
		azureLocation:     *azureLocation,
	})
	if err != nil {
		return err
	}

	var handler http.Handler
	switch *frontendName {
	case "aws_elasticache":
		handler = awsecfront.New(backend)
	case "gcp_memorystore":
		handler = gcpmsfront.New(backend)
	case "azure_redis":
		handler = azureredisfront.New(backend)
	default:
		return fmt.Errorf("unknown frontend %q (valid: aws_elasticache, gcp_memorystore, azure_redis)", *frontendName)
	}

	fmt.Fprintf(os.Stderr, "shim cache: frontend=%s backend=%s addr=%s\n",
		*frontendName, *backendName, *addr)
	return http.ListenAndServe(*addr, handler)
}

type cacheBackendConfig struct {
	kubeconfig        string
	redisopNamespace  string
	awsEndpoint       string
	gcpProject        string
	gcpRegion         string
	azureSubscription string
	azureRG           string
	azureLocation     string
}

func buildCacheBackend(name string, cfg cacheBackendConfig) (domain.Cache, error) {
	switch name {
	case "inmem":
		return inmem.New(), nil
	case "redisop":
		if cfg.kubeconfig == "" {
			return nil, fmt.Errorf("redisop backend requires -kubeconfig (or KUBECONFIG)")
		}
		k8sCfg, err := clientcmd.BuildConfigFromFlags("", cfg.kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("build kubeconfig: %w", err)
		}
		dyn, err := dynamic.NewForConfig(k8sCfg)
		if err != nil {
			return nil, fmt.Errorf("new dynamic client: %w", err)
		}
		core, err := kubernetes.NewForConfig(k8sCfg)
		if err != nil {
			return nil, fmt.Errorf("new kubernetes client: %w", err)
		}
		return redisop.New(dyn, core, redisop.Config{Namespace: cfg.redisopNamespace}), nil
	case "aws":
		ac, err := awscfg.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		opts := []func(*elasticache.Options){}
		if cfg.awsEndpoint != "" {
			opts = append(opts, func(o *elasticache.Options) {
				o.BaseEndpoint = awsapi.String(cfg.awsEndpoint)
			})
		}
		return awsbackend.New(elasticache.NewFromConfig(ac, opts...)), nil
	case "gcp":
		if cfg.gcpProject == "" {
			return nil, fmt.Errorf("gcp backend requires -gcp-project (or GCP_PROJECT_ID)")
		}
		svc, err := redisapi.NewService(context.Background(), option.WithUserAgent("shimanism-cache"))
		if err != nil {
			return nil, fmt.Errorf("connect to Memorystore: %w", err)
		}
		return gcpbackend.New(svc, gcpbackend.Config{ProjectID: cfg.gcpProject, Region: cfg.gcpRegion}), nil
	case "azure":
		if cfg.azureSubscription == "" || cfg.azureRG == "" {
			return nil, fmt.Errorf("azure backend requires -azure-subscription + -azure-resource-group")
		}
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("azure credential chain: %w", err)
		}
		return azurebackend.New(azurebackend.Config{
			SubscriptionID: cfg.azureSubscription,
			ResourceGroup:  cfg.azureRG,
			Location:       cfg.azureLocation,
			Credential:     cred,
		})
	default:
		return nil, fmt.Errorf("unknown backend %q (valid: inmem, redisop, aws, gcp, azure)", name)
	}
}
