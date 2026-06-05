package main

import (
	"net/http"

	"github.com/e6qu/shimanism/internal/cache/domain"
	awsecfront "github.com/e6qu/shimanism/internal/cache/frontends/aws_elasticache"
	azureredisfront "github.com/e6qu/shimanism/internal/cache/frontends/azure_redis"
	gcpmsfront "github.com/e6qu/shimanism/internal/cache/frontends/gcp_memorystore"
)

func buildCacheFrontend(name string, backend domain.Cache) (http.Handler, error) {
	return selectedFrontend(name, "aws_elasticache, gcp_memorystore, azure_redis", backend, map[string]func(domain.Cache) http.Handler{
		"aws_elasticache": awsecfront.New,
		"gcp_memorystore": func(b domain.Cache) http.Handler { return gcpmsfront.New(b) },
		"azure_redis":     func(b domain.Cache) http.Handler { return azureredisfront.New(b) },
	})
}
