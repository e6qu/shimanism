// Shared helpers for shim subcommand wiring.

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"google.golang.org/api/option"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type managedControlSpec[B any] struct {
	managedControlSettings
	buildBackend  func(string, managedBackendConfig) (B, error)
	buildFrontend func(string, B) (http.Handler, error)
}

type managedControlSettings struct {
	service         string
	defaultAddr     string
	defaultFrontend string
	frontendHelp    string
	defaultBackend  string
	backendHelp     string
	peerFlag        string
	peerEnv         string
	peerHelp        string
	peerNSFlag      string
	peerNSEnv       string
	peerNSDefault   string
	peerNSHelp      string
	awsEndpointEnv  string
	awsEndpointHelp string
}

func managedControl[B any](
	settings managedControlSettings,
	buildBackend func(string, managedBackendConfig) (B, error),
	buildFrontend func(string, B) (http.Handler, error),
) managedControlSpec[B] {
	return managedControlSpec[B]{
		managedControlSettings: settings,
		buildBackend:           buildBackend,
		buildFrontend:          buildFrontend,
	}
}

func peerManagedControl(service, addr, defaultFrontend, frontends, backends, peerName, peerNamespaceEnv, peerSubject, awsEndpointEnv, awsEndpointHelp string) managedControlSettings {
	return managedControlSettings{
		service:         service,
		defaultAddr:     addr,
		defaultFrontend: defaultFrontend,
		frontendHelp:    "frontend wire protocol: " + frontends,
		defaultBackend:  "inmem",
		backendHelp:     "backend: " + backends,
		peerFlag:        "kubeconfig",
		peerEnv:         "KUBECONFIG",
		peerHelp:        fmt.Sprintf("Path to kubeconfig (%s backend)", peerName),
		peerNSFlag:      peerName + "-namespace",
		peerNSEnv:       peerNamespaceEnv,
		peerNSDefault:   "default",
		peerNSHelp:      "Kubernetes namespace for " + peerSubject,
		awsEndpointEnv:  awsEndpointEnv,
		awsEndpointHelp: awsEndpointHelp,
	}
}

type managedBackendConfig struct {
	kubeconfig   string
	k8sNamespace string
	cloudControlConfig
}

type managedBackendBuilders[B any] struct {
	valid    string
	peerName string
	inmem    func() B
	peer     func(string, string) (B, error)
	aws      func(string) (B, error)
	gcp      func(string, string) (B, error)
	azure    func(string, string, string) (B, error)
}

func runManagedControl[B any](args []string, spec managedControlSpec[B]) error {
	fs := flag.NewFlagSet(spec.service, flag.ContinueOnError)
	addr := fs.String("addr", spec.defaultAddr, "address to listen on")
	frontendName := fs.String("frontend", spec.defaultFrontend, spec.frontendHelp)
	backendName := fs.String("backend", spec.defaultBackend, spec.backendHelp)
	kubeconfig := fs.String(spec.peerFlag, envOr(spec.peerEnv, ""), spec.peerHelp)
	peerNS := fs.String(spec.peerNSFlag, envOr(spec.peerNSEnv, spec.peerNSDefault), spec.peerNSHelp)
	cloudFlags := addCloudControlFlags(fs, spec.awsEndpointEnv, spec.awsEndpointHelp)

	if err := fs.Parse(args); err != nil {
		return err
	}

	backend, err := spec.buildBackend(*backendName, managedBackendConfig{
		kubeconfig:         *kubeconfig,
		k8sNamespace:       *peerNS,
		cloudControlConfig: cloudFlags.config(),
	})
	if err != nil {
		return err
	}
	handler, err := spec.buildFrontend(*frontendName, backend)
	if err != nil {
		return err
	}
	return serveSingle(spec.service, *frontendName, *backendName, *addr, handler)
}

func buildManagedBackend[B any](name string, cfg managedBackendConfig, builders managedBackendBuilders[B]) (B, error) {
	var zero B
	switch name {
	case "inmem":
		return builders.inmem(), nil
	case builders.peerName:
		if cfg.kubeconfig == "" {
			return zero, fmt.Errorf("%s backend requires -kubeconfig (or KUBECONFIG)", builders.peerName)
		}
		return builders.peer(cfg.kubeconfig, cfg.k8sNamespace)
	case "aws":
		return builders.aws(cfg.awsEndpoint)
	case "gcp":
		if cfg.gcpProject == "" {
			return zero, fmt.Errorf("gcp backend requires -gcp-project (or GCP_PROJECT_ID)")
		}
		return builders.gcp(cfg.gcpProject, cfg.gcpRegion)
	case "azure":
		if cfg.azureSubscription == "" || cfg.azureRG == "" {
			return zero, fmt.Errorf("azure backend requires -azure-subscription + -azure-resource-group")
		}
		return builders.azure(cfg.azureSubscription, cfg.azureRG, cfg.azureLocation)
	default:
		return zero, fmt.Errorf("unknown backend %q (valid: %s)", name, builders.valid)
	}
}

type cloudControlFlags struct {
	awsEndpoint        *string
	gcpProject         *string
	gcpRegion          *string
	azureSubscription  *string
	azureResourceGroup *string
	azureLocation      *string
}

type cloudControlConfig struct {
	awsEndpoint       string
	gcpProject        string
	gcpRegion         string
	azureSubscription string
	azureRG           string
	azureLocation     string
}

func addCloudControlFlags(fs *flag.FlagSet, awsEndpointEnv, awsEndpointHelp string) cloudControlFlags {
	return cloudControlFlags{
		awsEndpoint:        fs.String("aws-endpoint", envOr(awsEndpointEnv, ""), awsEndpointHelp),
		gcpProject:         fs.String("gcp-project", envOr("GCP_PROJECT_ID", ""), "GCP project ID"),
		gcpRegion:          fs.String("gcp-region", envOr("GCP_REGION", "us-central1"), "GCP region"),
		azureSubscription:  fs.String("azure-subscription", envOr("AZURE_SUBSCRIPTION_ID", ""), "Azure subscription ID"),
		azureResourceGroup: fs.String("azure-resource-group", envOr("AZURE_RESOURCE_GROUP", ""), "Azure resource group"),
		azureLocation:      fs.String("azure-location", envOr("AZURE_LOCATION", "eastus"), "Azure location"),
	}
}

func (f cloudControlFlags) config() cloudControlConfig {
	return cloudControlConfig{
		awsEndpoint:       *f.awsEndpoint,
		gcpProject:        *f.gcpProject,
		gcpRegion:         *f.gcpRegion,
		azureSubscription: *f.azureSubscription,
		azureRG:           *f.azureResourceGroup,
		azureLocation:     *f.azureLocation,
	}
}

func serveSingle(service, frontend, backend, addr string, handler http.Handler) error {
	fmt.Fprintf(os.Stderr, "shim %s: frontend=%s backend=%s addr=%s\n", service, frontend, backend, addr)
	return http.ListenAndServe(addr, handler)
}

func selectedFrontend[B any](name, valid string, backend B, frontends map[string]func(B) http.Handler) (http.Handler, error) {
	build, ok := frontends[name]
	if !ok {
		return nil, fmt.Errorf("unknown frontend %q (valid: %s)", name, valid)
	}
	return build(backend), nil
}

func defaultAWSConfig() (awsapi.Config, error) {
	cfg, err := awscfg.LoadDefaultConfig(context.Background())
	if err != nil {
		return awsapi.Config{}, fmt.Errorf("load AWS config: %w", err)
	}
	return cfg, nil
}

func newAWSManagedBackend[O, C, B any](
	endpoint string,
	newClient func(awsapi.Config, ...func(*O)) C,
	setEndpoint func(*O, *string),
	build func(C) B,
) (B, error) {
	var zero B
	cfg, err := defaultAWSConfig()
	if err != nil {
		return zero, err
	}
	opts := []func(*O){}
	if endpoint != "" {
		opts = append(opts, func(o *O) {
			setEndpoint(o, awsapi.String(endpoint))
		})
	}
	return build(newClient(cfg, opts...)), nil
}

func newGCPManagedBackend[S, C, B any](
	project, region, userAgent, errLabel string,
	newService func(context.Context, ...option.ClientOption) (S, error),
	config func(string, string) C,
	build func(S, C) B,
) (B, error) {
	var zero B
	svc, err := newService(context.Background(), option.WithUserAgent(userAgent))
	if err != nil {
		return zero, fmt.Errorf("%s: %w", errLabel, err)
	}
	return build(svc, config(project, region)), nil
}

func defaultAzureCredential(errLabel string) (azcore.TokenCredential, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", errLabel, err)
	}
	return cred, nil
}

func newAzureManagedBackend[C, B any](
	subscription, resourceGroup, location string,
	config func(string, string, string, azcore.TokenCredential) C,
	build func(C) (B, error),
) (B, error) {
	var zero B
	cred, err := defaultAzureCredential("azure credential chain")
	if err != nil {
		return zero, err
	}
	return build(config(subscription, resourceGroup, location, cred))
}

func k8sDynamicCore(kubeconfig string) (dynamic.Interface, kubernetes.Interface, error) {
	k8sCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("build kubeconfig: %w", err)
	}
	dyn, err := dynamic.NewForConfig(k8sCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("new dynamic client: %w", err)
	}
	core, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("new kubernetes client: %w", err)
	}
	return dyn, core, nil
}
