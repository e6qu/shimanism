package main

import (
	redisapi "google.golang.org/api/redis/v1"

	"github.com/e6qu/shimanism/internal/cache/domain"
	gcpbackend "github.com/e6qu/shimanism/services/cache/backends/gcp"
)

func newGCPCacheBackend(project, region string) (domain.Cache, error) {
	return newGCPManagedBackend(project, region, "shimanism-cache", "connect to Memorystore",
		redisapi.NewService, cacheGCPConfig, cacheGCPBackend)
}

func cacheGCPConfig(project, region string) gcpbackend.Config {
	return gcpbackend.Config{ProjectID: project, Region: region}
}

func cacheGCPBackend(svc *redisapi.Service, cfg gcpbackend.Config) domain.Cache {
	return gcpbackend.New(svc, cfg)
}
