// rdbms subcommand wiring.

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
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
	awsrdsfront "github.com/e6qu/shimanism/internal/rdbms/frontends/aws_rds"
	azuredbadminfront "github.com/e6qu/shimanism/internal/rdbms/frontends/azure_dbadmin"
	gcpcloudsqlfront "github.com/e6qu/shimanism/internal/rdbms/frontends/gcp_cloudsql"
	awsbackend "github.com/e6qu/shimanism/services/rdbms/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/rdbms/backends/azure"
	"github.com/e6qu/shimanism/services/rdbms/backends/cnpg"
	gcpbackend "github.com/e6qu/shimanism/services/rdbms/backends/gcp"
	"github.com/e6qu/shimanism/services/rdbms/backends/inmem"
)

func runRDBMS(args []string) error {
	fs := flag.NewFlagSet("rdbms", flag.ContinueOnError)
	addr := fs.String("addr", ":9400", "address to listen on")
	frontendName := fs.String("frontend", "aws_rds",
		"frontend wire protocol: aws_rds, gcp_cloudsql, azure_dbadmin")
	backendName := fs.String("backend", "inmem",
		"backend: inmem, cnpg, aws, gcp, azure")

	kubeconfig := fs.String("kubeconfig", envOr("KUBECONFIG", ""), "Path to kubeconfig (cnpg backend)")
	cnpgNamespace := fs.String("cnpg-namespace", envOr("CNPG_NAMESPACE", "default"), "Kubernetes namespace for cnpg resources")
	awsEndpoint := fs.String("aws-endpoint", envOr("AWS_RDS_ENDPOINT", ""), "AWS RDS endpoint override")
	gcpProject := fs.String("gcp-project", envOr("GCP_PROJECT_ID", ""), "GCP project ID")
	gcpRegion := fs.String("gcp-region", envOr("GCP_REGION", "us-central1"), "GCP region")
	azureSubscription := fs.String("azure-subscription", envOr("AZURE_SUBSCRIPTION_ID", ""), "Azure subscription ID")
	azureResourceGroup := fs.String("azure-resource-group", envOr("AZURE_RESOURCE_GROUP", ""), "Azure resource group")
	azureLocation := fs.String("azure-location", envOr("AZURE_LOCATION", "eastus"), "Azure location")

	if err := fs.Parse(args); err != nil {
		return err
	}

	backend, err := buildRDBMSBackend(*backendName, rdbmsBackendConfig{
		kubeconfig:        *kubeconfig,
		cnpgNamespace:     *cnpgNamespace,
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
	case "aws_rds":
		handler = awsrdsfront.New(backend)
	case "gcp_cloudsql":
		handler = gcpcloudsqlfront.New(backend)
	case "azure_dbadmin":
		handler = azuredbadminfront.New(backend)
	default:
		return fmt.Errorf("unknown frontend %q (valid: aws_rds, gcp_cloudsql, azure_dbadmin)", *frontendName)
	}

	fmt.Fprintf(os.Stderr, "shim rdbms: frontend=%s backend=%s addr=%s\n",
		*frontendName, *backendName, *addr)
	return http.ListenAndServe(*addr, handler)
}

type rdbmsBackendConfig struct {
	kubeconfig        string
	cnpgNamespace     string
	awsEndpoint       string
	gcpProject        string
	gcpRegion         string
	azureSubscription string
	azureRG           string
	azureLocation     string
}

func buildRDBMSBackend(name string, cfg rdbmsBackendConfig) (domain.RDBMS, error) {
	switch name {
	case "inmem":
		return inmem.New(), nil
	case "cnpg":
		if cfg.kubeconfig == "" {
			return nil, fmt.Errorf("cnpg backend requires -kubeconfig (or KUBECONFIG)")
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
		return cnpg.New(dyn, core, cnpg.Config{Namespace: cfg.cnpgNamespace}), nil
	case "aws":
		ac, err := awscfg.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		opts := []func(*rds.Options){}
		if cfg.awsEndpoint != "" {
			opts = append(opts, func(o *rds.Options) {
				o.BaseEndpoint = awsapi.String(cfg.awsEndpoint)
			})
		}
		return awsbackend.New(rds.NewFromConfig(ac, opts...)), nil
	case "gcp":
		if cfg.gcpProject == "" {
			return nil, fmt.Errorf("gcp backend requires -gcp-project (or GCP_PROJECT_ID)")
		}
		svc, err := sqladmin.NewService(context.Background(), option.WithUserAgent("shimanism-rdbms"))
		if err != nil {
			return nil, fmt.Errorf("connect to Cloud SQL Admin: %w", err)
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
		return nil, fmt.Errorf("unknown backend %q (valid: inmem, cnpg, aws, gcp, azure)", name)
	}
}
