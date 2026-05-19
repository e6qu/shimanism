// apigateway subcommand wiring.

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
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwapi "google.golang.org/api/apigateway/v1"
	"google.golang.org/api/option"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
	awsapigwfront "github.com/e6qu/shimanism/internal/apigateway/frontends/aws_apigatewayv2"
	azureapimfront "github.com/e6qu/shimanism/internal/apigateway/frontends/azure_apim"
	gcpapigwfront "github.com/e6qu/shimanism/internal/apigateway/frontends/gcp_apigateway"
	awsbackend "github.com/e6qu/shimanism/services/apigateway/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/apigateway/backends/azure"
	envoybackend "github.com/e6qu/shimanism/services/apigateway/backends/envoy"
	gcpbackend "github.com/e6qu/shimanism/services/apigateway/backends/gcp"
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

func runAPIGateway(args []string) error {
	fs := flag.NewFlagSet("apigateway", flag.ContinueOnError)
	addr := fs.String("addr", ":9700", "address to listen on")
	frontendName := fs.String("frontend", "aws_apigatewayv2",
		"frontend wire protocol: aws_apigatewayv2, gcp_apigateway, azure_apim")
	backendName := fs.String("backend", "inmem",
		"backend: inmem, envoy, aws, gcp, azure")

	kubeconfig := fs.String("kubeconfig", envOr("KUBECONFIG", ""), "Path to kubeconfig (envoy backend)")
	envoyNS := fs.String("envoy-namespace", envOr("ENVOY_NAMESPACE", "default"), "Envoy Gateway namespace")
	envoyClass := fs.String("envoy-gateway-class", envOr("ENVOY_GATEWAY_CLASS", "eg"), "Envoy Gateway GatewayClass name")
	awsEndpoint := fs.String("aws-endpoint", envOr("AWS_APIGATEWAY_ENDPOINT", ""), "AWS API Gateway v2 endpoint override")
	gcpProject := fs.String("gcp-project", envOr("GCP_PROJECT_ID", ""), "GCP project ID")
	gcpRegion := fs.String("gcp-region", envOr("GCP_REGION", "us-central1"), "GCP region")
	azureSub := fs.String("azure-subscription", envOr("AZURE_SUBSCRIPTION_ID", ""), "Azure subscription ID")
	azureRG := fs.String("azure-resource-group", envOr("AZURE_RESOURCE_GROUP", ""), "Azure resource group")
	azureSvc := fs.String("azure-apim-service", envOr("AZURE_APIM_SERVICE", ""), "Azure API Management service name")

	if err := fs.Parse(args); err != nil {
		return err
	}

	backend, err := buildAPIGatewayBackend(*backendName, apigatewayBackendConfig{
		kubeconfig:   *kubeconfig,
		envoyNS:      *envoyNS,
		envoyClass:   *envoyClass,
		awsEndpoint:  *awsEndpoint,
		gcpProject:   *gcpProject,
		gcpRegion:    *gcpRegion,
		azureSub:     *azureSub,
		azureRG:      *azureRG,
		azureService: *azureSvc,
	})
	if err != nil {
		return err
	}

	var handler http.Handler
	switch *frontendName {
	case "aws_apigatewayv2":
		handler = awsapigwfront.New(backend)
	case "gcp_apigateway":
		handler = gcpapigwfront.New(backend)
	case "azure_apim":
		handler = azureapimfront.New(backend)
	default:
		return fmt.Errorf("unknown frontend %q (valid: aws_apigatewayv2, gcp_apigateway, azure_apim)", *frontendName)
	}

	fmt.Fprintf(os.Stderr, "shim apigateway: frontend=%s backend=%s addr=%s\n",
		*frontendName, *backendName, *addr)
	return http.ListenAndServe(*addr, handler)
}

type apigatewayBackendConfig struct {
	kubeconfig   string
	envoyNS      string
	envoyClass   string
	awsEndpoint  string
	gcpProject   string
	gcpRegion    string
	azureSub     string
	azureRG      string
	azureService string
}

func buildAPIGatewayBackend(name string, cfg apigatewayBackendConfig) (domain.APIGateway, error) {
	switch name {
	case "inmem":
		return inmem.New(), nil
	case "envoy":
		if cfg.kubeconfig == "" {
			return nil, fmt.Errorf("envoy backend requires -kubeconfig (or KUBECONFIG)")
		}
		k8sCfg, err := clientcmd.BuildConfigFromFlags("", cfg.kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("build kubeconfig: %w", err)
		}
		dyn, err := dynamic.NewForConfig(k8sCfg)
		if err != nil {
			return nil, fmt.Errorf("new dynamic client: %w", err)
		}
		return envoybackend.New(dyn, envoybackend.Config{
			Namespace:        cfg.envoyNS,
			GatewayClassName: cfg.envoyClass,
		}), nil
	case "aws":
		ac, err := awscfg.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		opts := []func(*apigatewayv2.Options){}
		if cfg.awsEndpoint != "" {
			opts = append(opts, func(o *apigatewayv2.Options) {
				o.BaseEndpoint = awsapi.String(cfg.awsEndpoint)
			})
		}
		return awsbackend.New(apigatewayv2.NewFromConfig(ac, opts...)), nil
	case "gcp":
		if cfg.gcpProject == "" {
			return nil, fmt.Errorf("gcp backend requires -gcp-project")
		}
		svc, err := apigwapi.NewService(context.Background(), option.WithUserAgent("shimanism-apigateway"))
		if err != nil {
			return nil, fmt.Errorf("connect to GCP API Gateway: %w", err)
		}
		return gcpbackend.New(svc, gcpbackend.Config{ProjectID: cfg.gcpProject, Region: cfg.gcpRegion}), nil
	case "azure":
		if cfg.azureSub == "" || cfg.azureRG == "" || cfg.azureService == "" {
			return nil, fmt.Errorf("azure backend requires -azure-subscription + -azure-resource-group + -azure-apim-service")
		}
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("azure credential: %w", err)
		}
		return azurebackend.New(azurebackend.Config{
			SubscriptionID: cfg.azureSub,
			ResourceGroup:  cfg.azureRG,
			ServiceName:    cfg.azureService,
			Credential:     cred,
		})
	default:
		return nil, fmt.Errorf("unknown backend %q (valid: inmem, envoy, aws, gcp, azure)", name)
	}
}
