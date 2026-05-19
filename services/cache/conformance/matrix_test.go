// Matrix conformance for the cache service. inmem cell exercises
// the lifecycle; redisop / aws / gcp / azure cells need real
// infrastructure and skip cleanly when their env-var gate isn't
// set.
package conformance_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/cache/conformance"
)

func TestCacheMatrix_AWSFrontend(t *testing.T) {
	ctx := context.Background()
	for _, f := range conformance.ActiveBackends() {
		t.Run(f.Name, func(t *testing.T) {
			be := f.Fn(t)
			srv := harness.StartCacheServerAWS(t, be)
			cfg, err := awsconfig.LoadDefaultConfig(ctx,
				awsconfig.WithRegion("us-east-1"),
				awsconfig.WithCredentialsProvider(awsapi.AnonymousCredentials{}),
			)
			if err != nil {
				t.Fatalf("aws config: %v", err)
			}
			client := elasticache.NewFromConfig(cfg, func(o *elasticache.Options) {
				o.BaseEndpoint = awsapi.String(srv.URL)
			})

			name := fmt.Sprintf("matrix-aws-%s", f.Name)
			if _, err := client.CreateCacheCluster(ctx, &elasticache.CreateCacheClusterInput{
				CacheClusterId: awsapi.String(name),
				Engine:         awsapi.String("redis"),
				EngineVersion:  awsapi.String("7.0"),
				CacheNodeType:  awsapi.String("cache.t3.micro"),
			}); err != nil {
				t.Fatalf("CreateCacheCluster: %v", err)
			}
			t.Cleanup(func() {
				_, _ = client.DeleteCacheCluster(ctx, &elasticache.DeleteCacheClusterInput{
					CacheClusterId: awsapi.String(name),
				})
			})

			budget := 2 * time.Second
			if f.Name != "inmem" {
				budget = 10 * time.Minute
			}
			deadline := time.Now().Add(budget)
			var available bool
			for time.Now().Before(deadline) {
				out, err := client.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
					CacheClusterId: awsapi.String(name),
				})
				if err != nil {
					t.Fatalf("DescribeCacheClusters: %v", err)
				}
				if len(out.CacheClusters) == 1 &&
					awsapi.ToString(out.CacheClusters[0].CacheClusterStatus) == "available" {
					available = true
					break
				}
				time.Sleep(time.Second)
			}
			if !available {
				t.Fatalf("%s: cluster never reached available state", f.Name)
			}
		})
	}
}
