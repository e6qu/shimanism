// cache subcommand wiring.

package main

import "github.com/e6qu/shimanism/internal/cache/domain"

var cacheControl = peerManagedControl("cache", ":9500", "aws_elasticache",
	"aws_elasticache, gcp_memorystore, azure_redis", "inmem, redisop, aws, gcp, azure",
	"redisop", "REDISOP_NAMESPACE", "Redis CRs", "AWS_ELASTICACHE_ENDPOINT", "AWS ElastiCache endpoint override")

func runCache(args []string) error {
	return runManagedControl(args, managedControl(cacheControl, buildCacheBackend, buildCacheFrontend))
}

func buildCacheBackend(name string, cfg managedBackendConfig) (domain.Cache, error) {
	return buildManagedBackend(name, cfg, managedBackendBuilders[domain.Cache]{
		valid:    "inmem, redisop, aws, gcp, azure",
		peerName: "redisop",
		inmem:    newInmemCacheBackend,
		peer:     newRedisOpCacheBackend,
		aws:      newAWSCacheBackend,
		gcp:      newGCPCacheBackend,
		azure:    newAzureCacheBackend,
	})
}
