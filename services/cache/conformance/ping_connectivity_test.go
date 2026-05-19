// Phase 6 exit criterion (sub-phase 6.15): the shim provisions a
// Redis-Operator instance via the AWS ElastiCache frontend, then
// the test opens a *real* RESP connection through the returned
// Connection block and runs PING. The shim plays no role on the
// data path — RESP goes straight to the operator-managed Redis.
//
// Gated on REDISOP_CONFORMANCE=1 + KUBECONFIG so it's opt-in.
// CI's conformance-redisop lane sets both.
package conformance_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/redis/go-redis/v9"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/cache/conformance"
)

func TestPingConnectivity_RedisOp(t *testing.T) {
	if os.Getenv("REDISOP_CONFORMANCE") != "1" {
		t.Skip("REDISOP_CONFORMANCE!=1 (PING connectivity test against Redis Operator disabled)")
	}
	ctx := context.Background()
	be := conformance.NewRedisOp(t)
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

	const name = "ping-test"
	const password = "shim-ping-supersecret"
	cOut, err := client.CreateCacheCluster(ctx, &elasticache.CreateCacheClusterInput{
		CacheClusterId: awsapi.String(name),
		Engine:         awsapi.String("redis"),
		EngineVersion:  awsapi.String("7.0"),
		CacheNodeType:  awsapi.String("cache.t3.micro"),
		AuthToken:      awsapi.String(password),
	})
	if err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}
	_ = cOut
	t.Cleanup(func() {
		_, _ = client.DeleteCacheCluster(ctx, &elasticache.DeleteCacheClusterInput{
			CacheClusterId: awsapi.String(name),
		})
	})

	// Poll until status=available + Endpoint populated.
	deadline := time.Now().Add(10 * time.Minute)
	var endpoint string
	for time.Now().Before(deadline) {
		out, err := client.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
			CacheClusterId: awsapi.String(name),
		})
		if err != nil {
			t.Logf("DescribeCacheClusters (will retry): %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if len(out.CacheClusters) == 1 &&
			awsapi.ToString(out.CacheClusters[0].CacheClusterStatus) == "available" &&
			out.CacheClusters[0].ConfigurationEndpoint != nil {
			endpoint = awsapi.ToString(out.CacheClusters[0].ConfigurationEndpoint.Address)
			break
		}
		time.Sleep(5 * time.Second)
	}
	if endpoint == "" {
		t.Fatal("Redis instance never reached available state")
	}
	t.Logf("instance ready, endpoint=%s", endpoint)

	// The endpoint is the operator-emitted in-cluster Service DNS;
	// port-forward to dial from the host runner.
	host, port := portForwardService(t, ctx, endpoint)

	// Open a real RESP connection. The shim is not on this path.
	// Auth uses the password the test supplied at CreateCacheCluster;
	// the shim stored it in the Secret the operator references.
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
	})
	defer rdb.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var lastErr error
	for i := 0; i < 30; i++ {
		if resp, err := rdb.Ping(pingCtx).Result(); err == nil {
			if resp != "PONG" {
				t.Errorf("PING returned %q, want PONG", resp)
			}
			return
		} else {
			lastErr = err
			time.Sleep(2 * time.Second)
		}
	}
	t.Fatalf("PING never succeeded: %v", lastErr)
}

// portForwardService spawns kubectl port-forward against the
// operator-emitted Service so the test (running outside kind)
// can dial Redis.
func portForwardService(t *testing.T, ctx context.Context, endpoint string) (string, int) {
	t.Helper()
	parts := strings.Split(endpoint, ".")
	if len(parts) < 2 {
		t.Fatalf("unexpected endpoint shape %q (want <name>.<ns>.svc...)", endpoint)
	}
	svcName := parts[0]
	namespace := parts[1]

	lst, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	localPort := lst.Addr().(*net.TCPAddr).Port
	lst.Close()

	cmd := exec.CommandContext(ctx, "kubectl",
		"-n", namespace,
		"port-forward",
		"svc/"+svcName,
		fmt.Sprintf("%d:6379", localPort),
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start kubectl port-forward: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), time.Second)
		if err == nil {
			conn.Close()
			return "127.0.0.1", localPort
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("kubectl port-forward never opened localhost:%d", localPort)
	return "", 0
}
