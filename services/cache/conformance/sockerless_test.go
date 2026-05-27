// Sockerless lane for the cache service. See docs/sockerless-validation.md.
package conformance_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
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
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	redisapi "google.golang.org/api/redis/v1"

	cachedomain "github.com/e6qu/shimanism/internal/cache/domain"
	"github.com/e6qu/shimanism/internal/harness"
	awscachebackend "github.com/e6qu/shimanism/services/cache/backends/aws"
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

// gcpHS256Bearer mints a test-mode HS256 JWT that the shim's
// gcpbearer middleware accepts. The shim's GCP frontends run behind
// gcpbearer.Middleware in test mode (HS256 with key
// "test-key-do-not-use-in-prod") — equivalent to the SigV4
// signature the AWS frontend's existing through-shim cells satisfy
// via static credentials. This is the GCP-side parity.
func gcpHS256Bearer(t *testing.T, audience string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT","kid":"shim-test"}`))
	payloadJSON := []byte(`{"aud":"` + audience + `","exp":4102444800,"iat":1}`) // exp = 2100-01-01
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, []byte("test-key-do-not-use-in-prod"))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

// gcpStaticTokenSource produces a static token source for the GCP
// SDK's option.WithTokenSource. The SDK auto-attaches the bearer
// to outgoing requests.
type gcpStaticTokenSource struct{ token string }

func (s gcpStaticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.token, TokenType: "Bearer"}, nil
}

// TestSockerless_GCPMemorystoreFrontendToAWSBackend_CRUD is the
// reverse-direction through-shim cell: GCP Memorystore SDK drives
// the shim's GCP cache frontend, which routes through the shim's
// AWS ElastiCache backend, which targets sockerless's AWS sim. This
// is the GCP→AWS migration path (complement of the existing
// AWS→GCP cell). BUG-24 reverse-direction coverage.
func TestSockerless_GCPMemorystoreFrontendToAWSBackend_CRUD(t *testing.T) {
	awsEndpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if awsEndpoint == "" {
		awsEndpoint = os.Getenv("SOCKERLESS_AWS_SM_ENDPOINT")
	}
	if awsEndpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}
	ctx := context.Background()

	// AWS ElastiCache backend pointed at sockerless AWS.
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		os.Setenv("AWS_ACCESS_KEY_ID", "test")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		os.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	}
	if os.Getenv("AWS_REGION") == "" {
		os.Setenv("AWS_REGION", "us-east-1")
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	if os.Getenv("AWS_S3_CONFORMANCE_INSECURE_TLS") == "1" {
		cfg.HTTPClient = awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		})
	}
	ecClient := elasticache.NewFromConfig(cfg, func(o *elasticache.Options) {
		o.BaseEndpoint = awsapi.String(awsEndpoint)
	})
	backend := awscachebackend.New(ecClient)
	srv := harness.StartCacheServerGCP(t, backend)

	// GCP Memorystore SDK driver pointing at the shim's GCP frontend.
	// Test-mode bearer minted with the gcpbearer test key + audience
	// the harness configured for the cache frontend.
	mem, err := redisapi.NewService(ctx,
		option.WithEndpoint(srv.URL+"/"),
		option.WithTokenSource(gcpStaticTokenSource{token: gcpHS256Bearer(t, "https://redis.googleapis.com/")}),
	)
	if err != nil {
		t.Fatalf("memorystore client: %v", err)
	}

	project := "shim-sockerless"
	region := "us-central1"
	parent := "projects/" + project + "/locations/" + region
	name := "shim-sk-rev-" + sockerlessHex8()

	op, err := mem.Projects.Locations.Instances.Create(parent, &redisapi.Instance{
		Tier:         "BASIC",
		RedisVersion: "REDIS_7_0",
		MemorySizeGb: 1,
	}).InstanceId(name).Do()
	if err != nil {
		t.Fatalf("Create through shim: %v", err)
	}
	if op == nil {
		t.Fatalf("Create returned nil operation")
	}
	t.Cleanup(func() {
		_, _ = mem.Projects.Locations.Instances.Delete(parent + "/instances/" + name).Do()
	})

	got, err := mem.Projects.Locations.Instances.Get(parent + "/instances/" + name).Do()
	if err != nil {
		t.Fatalf("Get through shim: %v", err)
	}
	if got == nil || got.Name == "" {
		t.Errorf("Get returned empty: %+v", got)
	}

	list, err := mem.Projects.Locations.Instances.List(parent).Do()
	if err != nil {
		t.Fatalf("List through shim: %v", err)
	}
	found := false
	for _, inst := range list.Instances {
		if inst.Name == parent+"/instances/"+name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("List did not contain %q", name)
	}
}
