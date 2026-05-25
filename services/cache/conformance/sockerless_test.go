// Sockerless lane for the cache service. See docs/sockerless-validation.md.
package conformance_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"google.golang.org/api/option"
	redisapi "google.golang.org/api/redis/v1"

	"github.com/e6qu/shimanism/internal/harness"
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
