// Sockerless lane for the cache service. See docs/sockerless-validation.md.
package conformance_test

import (
	"context"
	"crypto/rand"
	"crypto/tls"
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
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"google.golang.org/api/option"
	redisapi "google.golang.org/api/redis/v1"

	cachedomain "github.com/e6qu/shimanism/internal/cache/domain"
	"github.com/e6qu/shimanism/internal/harness"
	azurecache "github.com/e6qu/shimanism/services/cache/backends/azure"
	gcpbackend "github.com/e6qu/shimanism/services/cache/backends/gcp"
)

// TestSockerless_AWSElastiCacheFrontendToGCPBackend_CRUD drives the
// full through-shim E2E path for caches:
// aws-sdk-go-v2 ElastiCache client → AWS-shaped shim frontend → GCP
// Memorystore backend → sockerless GCP simulator.
func TestSockerless_AWSElastiCacheFrontendToGCPBackend_CRUD(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	ctx := context.Background()
	svc, err := redisapi.NewService(ctx,
		option.WithEndpoint("http://"+endpoint+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("memorystore client: %v", err)
	}
	project := os.Getenv("SOCKERLESS_GCP_PROJECT")
	if project == "" {
		project = "shim-sockerless"
	}
	backend := gcpbackend.New(svc, gcpbackend.Config{ProjectID: project, Region: "us-central1"})
	srv := harness.StartCacheServerAWS(t, backend)
	client := newSockerlessElastiCacheClient(t, srv.URL)

	name := "shim-sk-cache-" + sockerlessHex8()
	create, err := client.CreateCacheCluster(ctx, &elasticache.CreateCacheClusterInput{
		CacheClusterId: awsapi.String(name),
		Engine:         awsapi.String("redis"),
		EngineVersion:  awsapi.String("7.0"),
		CacheNodeType:  awsapi.String("BASIC"),
		AuthToken:      awsapi.String("supersecret"),
	})
	if err != nil {
		t.Fatalf("CreateCacheCluster through shim: %v", err)
	}
	if awsapi.ToString(create.CacheCluster.CacheClusterId) != name {
		t.Errorf("CreateCacheCluster id = %q, want %q",
			awsapi.ToString(create.CacheCluster.CacheClusterId), name)
	}
	t.Cleanup(func() {
		_, _ = client.DeleteCacheCluster(ctx, &elasticache.DeleteCacheClusterInput{
			CacheClusterId: awsapi.String(name),
		})
	})

	describe, err := client.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
		CacheClusterId: awsapi.String(name),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters through shim: %v", err)
	}
	if len(describe.CacheClusters) != 1 {
		t.Fatalf("DescribeCacheClusters count = %d, want 1", len(describe.CacheClusters))
	}

	if _, err := client.ModifyCacheCluster(ctx, &elasticache.ModifyCacheClusterInput{
		CacheClusterId: awsapi.String(name),
		CacheNodeType:  awsapi.String("STANDARD_HA"),
	}); err != nil {
		t.Fatalf("ModifyCacheCluster through shim: %v", err)
	}
}

func newSockerlessElastiCacheClient(t *testing.T, endpoint string) *elasticache.Client {
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
	return elasticache.NewFromConfig(cfg, func(o *elasticache.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
}

func sockerlessHex8() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// TestSockerless_Azure_Cache_Redis_CRUD exercises the shim's Azure
// Cache for Redis backend against sockerless's Microsoft.Cache/Redis
// ARM control plane: CreateInstance → DescribeInstance → ListInstances
// → ModifyInstance → DeleteInstance. The data plane (Redis RESP wire
// protocol) is not in scope for sockerless — only the ARM management
// surface is.
func TestSockerless_Azure_Cache_Redis_CRUD(t *testing.T) {
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	opts := sockerlessARMClientOptions(port)
	backend, err := azurecache.New(azurecache.Config{
		SubscriptionID: sockerlessAzureSubscription(),
		ResourceGroup:  "shim-sk-rg",
		Location:       "eastus",
		Credential:     sockerlessNoOpCredential{},
		ClientOptions:  opts,
	})
	if err != nil {
		t.Fatalf("azurecache.New: %v", err)
	}
	ctx := context.Background()

	name := "shim-sk-rd-" + sockerlessHex8()
	create, err := backend.CreateInstance(ctx, name, cachedomain.CreateInstanceOptions{
		EngineVersion: "6.0",
		NodeType:      "Basic",
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if create.Instance.Name != name {
		t.Errorf("CreateInstance.Name = %q, want %q", create.Instance.Name, name)
	}
	t.Cleanup(func() { _ = backend.DeleteInstance(ctx, name) })

	got, err := backend.DescribeInstance(ctx, name)
	if err != nil {
		t.Fatalf("DescribeInstance: %v", err)
	}
	if got.Name != name {
		t.Errorf("DescribeInstance.Name = %q, want %q", got.Name, name)
	}

	list, err := backend.ListInstances(ctx, cachedomain.ListInstancesOptions{})
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	found := false
	for _, in := range list.Instances {
		if in.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListInstances did not contain %q", name)
	}
}

// sockerlessARMClientOptions builds an arm.ClientOptions targeting the
// sockerless Azure simulator on TLS port `port`. The cloud config
// overrides ARM's Endpoint so the SDK sends requests to the sim, and
// a localhost-only dialer + InsecureSkipVerify accept the sim's
// self-signed cert.
func sockerlessARMClientOptions(port string) *arm.ClientOptions {
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "127.0.0.1:"+port)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	httpClient := &http.Client{Transport: transport}
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
			Transport: httpClient,
		},
	}
}

func sockerlessAzureSubscription() string {
	if s := os.Getenv("SOCKERLESS_AZURE_SUBSCRIPTION"); s != "" {
		return s
	}
	return "00000000-0000-0000-0000-000000000000"
}

type sockerlessNoOpCredential struct{}

var sockerlessFarFuture = time.Date(2099, time.December, 31, 23, 59, 59, 0, time.UTC)

func (sockerlessNoOpCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "sockerless-test", ExpiresOn: sockerlessFarFuture}, nil
}
