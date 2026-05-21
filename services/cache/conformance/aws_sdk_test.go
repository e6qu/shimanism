// Phase 6 conformance: AWS ElastiCache-shaped frontend exercised
// by the official `aws-sdk-go-v2/service/elasticache` SDK against
// the in-mem backend. Verifies the Create → poll-until-available
// → Modify → Reboot → Delete lifecycle with the explicit Status
// transitions the cache domain surfaces.
package conformance_test

import (
	"context"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/cache/backends/inmem"
)

func TestAWSSDK_CacheLifecycle(t *testing.T) {
	srv := harness.StartCacheServerAWS(t, inmem.New())
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
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
	client := elasticache.NewFromConfig(cfg, func(o *elasticache.Options) {
		o.BaseEndpoint = awsapi.String(srv.URL)
	})

	if _, err := client.CreateCacheCluster(ctx, &elasticache.CreateCacheClusterInput{
		CacheClusterId: awsapi.String("shim-cache-test"),
		Engine:         awsapi.String("redis"),
		EngineVersion:  awsapi.String("7.0"),
		CacheNodeType:  awsapi.String("cache.t3.micro"),
		AuthToken:      awsapi.String("cli-supersecret"),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var available bool
	for time.Now().Before(deadline) {
		out, err := client.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
			CacheClusterId: awsapi.String("shim-cache-test"),
		})
		if err != nil {
			t.Fatalf("DescribeCacheClusters: %v", err)
		}
		if len(out.CacheClusters) == 1 &&
			awsapi.ToString(out.CacheClusters[0].CacheClusterStatus) == "available" {
			available = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !available {
		t.Fatal("cache cluster never reached available state")
	}

	if _, err := client.ModifyCacheCluster(ctx, &elasticache.ModifyCacheClusterInput{
		CacheClusterId: awsapi.String("shim-cache-test"),
		CacheNodeType:  awsapi.String("cache.t3.small"),
	}); err != nil {
		t.Errorf("ModifyCacheCluster: %v", err)
	}

	if _, err := client.RebootCacheCluster(ctx, &elasticache.RebootCacheClusterInput{
		CacheClusterId:       awsapi.String("shim-cache-test"),
		CacheNodeIdsToReboot: []string{"0001"},
	}); err != nil {
		t.Errorf("RebootCacheCluster: %v", err)
	}

	if _, err := client.DeleteCacheCluster(ctx, &elasticache.DeleteCacheClusterInput{
		CacheClusterId: awsapi.String("shim-cache-test"),
	}); err != nil {
		t.Errorf("DeleteCacheCluster: %v", err)
	}
}
