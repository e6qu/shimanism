// functions subcommand wiring.

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
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"google.golang.org/api/option"
	runapi "google.golang.org/api/run/v2"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/e6qu/shimanism/internal/functions/domain"
	awslambdafront "github.com/e6qu/shimanism/internal/functions/frontends/aws_lambda"
	azurecafront "github.com/e6qu/shimanism/internal/functions/frontends/azure_containerapps"
	gcpcrfront "github.com/e6qu/shimanism/internal/functions/frontends/gcp_cloudrun"
	awsbackend "github.com/e6qu/shimanism/services/functions/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/functions/backends/azure"
	gcpbackend "github.com/e6qu/shimanism/services/functions/backends/gcp"
	"github.com/e6qu/shimanism/services/functions/backends/inmem"
	"github.com/e6qu/shimanism/services/functions/backends/knative"
)

func runFunctions(args []string) error {
	fs := flag.NewFlagSet("functions", flag.ContinueOnError)
	addr := fs.String("addr", ":9600", "address to listen on")
	frontendName := fs.String("frontend", "aws_lambda",
		"frontend wire protocol: aws_lambda, gcp_cloudrun, azure_containerapps")
	backendName := fs.String("backend", "inmem",
		"backend: inmem, knative, aws, gcp, azure")

	kubeconfig := fs.String("kubeconfig", envOr("KUBECONFIG", ""), "Path to kubeconfig (knative backend)")
	knativeNS := fs.String("knative-namespace", envOr("KNATIVE_NAMESPACE", "default"), "Knative Service namespace")
	awsEndpoint := fs.String("aws-endpoint", envOr("AWS_LAMBDA_ENDPOINT", ""), "AWS Lambda endpoint override")
	awsRole := fs.String("aws-role", envOr("AWS_LAMBDA_ROLE_ARN", ""), "AWS Lambda execution role ARN")
	gcpProject := fs.String("gcp-project", envOr("GCP_PROJECT_ID", ""), "GCP project ID")
	gcpRegion := fs.String("gcp-region", envOr("GCP_REGION", "us-central1"), "GCP region")
	azureSub := fs.String("azure-subscription", envOr("AZURE_SUBSCRIPTION_ID", ""), "Azure subscription ID")
	azureRG := fs.String("azure-resource-group", envOr("AZURE_RESOURCE_GROUP", ""), "Azure resource group")
	azureLoc := fs.String("azure-location", envOr("AZURE_LOCATION", "eastus"), "Azure location")
	azureEnv := fs.String("azure-containerapp-env", envOr("AZURE_CONTAINERAPP_ENV_ID", ""), "Azure Container Apps managed environment ID")

	if err := fs.Parse(args); err != nil {
		return err
	}

	backend, err := buildFunctionsBackend(*backendName, functionsBackendConfig{
		kubeconfig:       *kubeconfig,
		knativeNamespace: *knativeNS,
		awsEndpoint:      *awsEndpoint,
		awsRole:          *awsRole,
		gcpProject:       *gcpProject,
		gcpRegion:        *gcpRegion,
		azureSub:         *azureSub,
		azureRG:          *azureRG,
		azureLoc:         *azureLoc,
		azureEnv:         *azureEnv,
	})
	if err != nil {
		return err
	}

	var handler http.Handler
	switch *frontendName {
	case "aws_lambda":
		handler = awslambdafront.New(backend)
	case "gcp_cloudrun":
		handler = gcpcrfront.New(backend)
	case "azure_containerapps":
		handler = azurecafront.New(backend)
	default:
		return fmt.Errorf("unknown frontend %q (valid: aws_lambda, gcp_cloudrun, azure_containerapps)", *frontendName)
	}

	fmt.Fprintf(os.Stderr, "shim functions: frontend=%s backend=%s addr=%s\n",
		*frontendName, *backendName, *addr)
	return http.ListenAndServe(*addr, handler)
}

type functionsBackendConfig struct {
	kubeconfig       string
	knativeNamespace string
	awsEndpoint      string
	awsRole          string
	gcpProject       string
	gcpRegion        string
	azureSub         string
	azureRG          string
	azureLoc         string
	azureEnv         string
}

func buildFunctionsBackend(name string, cfg functionsBackendConfig) (domain.Functions, error) {
	switch name {
	case "inmem":
		return inmem.New(), nil
	case "knative":
		if cfg.kubeconfig == "" {
			return nil, fmt.Errorf("knative backend requires -kubeconfig (or KUBECONFIG)")
		}
		k8sCfg, err := clientcmd.BuildConfigFromFlags("", cfg.kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("build kubeconfig: %w", err)
		}
		dyn, err := dynamic.NewForConfig(k8sCfg)
		if err != nil {
			return nil, fmt.Errorf("new dynamic client: %w", err)
		}
		return knative.New(dyn, knative.Config{Namespace: cfg.knativeNamespace}), nil
	case "aws":
		if cfg.awsRole == "" {
			return nil, fmt.Errorf("aws backend requires -aws-role (or AWS_LAMBDA_ROLE_ARN)")
		}
		ac, err := awscfg.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		opts := []func(*lambda.Options){}
		if cfg.awsEndpoint != "" {
			opts = append(opts, func(o *lambda.Options) {
				o.BaseEndpoint = awsapi.String(cfg.awsEndpoint)
			})
		}
		return awsbackend.New(lambda.NewFromConfig(ac, opts...), awsbackend.Config{ExecutionRoleARN: cfg.awsRole}), nil
	case "gcp":
		if cfg.gcpProject == "" {
			return nil, fmt.Errorf("gcp backend requires -gcp-project")
		}
		svc, err := runapi.NewService(context.Background(), option.WithUserAgent("shimanism-functions"))
		if err != nil {
			return nil, fmt.Errorf("connect to Cloud Run: %w", err)
		}
		return gcpbackend.New(svc, gcpbackend.Config{ProjectID: cfg.gcpProject, Region: cfg.gcpRegion}), nil
	case "azure":
		if cfg.azureSub == "" || cfg.azureRG == "" || cfg.azureEnv == "" {
			return nil, fmt.Errorf("azure backend requires -azure-subscription + -azure-resource-group + -azure-containerapp-env")
		}
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("azure credential: %w", err)
		}
		return azurebackend.New(azurebackend.Config{
			SubscriptionID:       cfg.azureSub,
			ResourceGroup:        cfg.azureRG,
			Location:             cfg.azureLoc,
			ManagedEnvironmentID: cfg.azureEnv,
			Credential:           cred,
		})
	default:
		return nil, fmt.Errorf("unknown backend %q (valid: inmem, knative, aws, gcp, azure)", name)
	}
}
