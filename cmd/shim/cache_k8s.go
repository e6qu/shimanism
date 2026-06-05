package main

import (
	"github.com/e6qu/shimanism/internal/cache/domain"
	"github.com/e6qu/shimanism/services/cache/backends/inmem"
	"github.com/e6qu/shimanism/services/cache/backends/redisop"
)

func newInmemCacheBackend() domain.Cache {
	return inmem.New()
}

func newRedisOpCacheBackend(kubeconfig, namespace string) (domain.Cache, error) {
	dyn, core, err := k8sDynamicCore(kubeconfig)
	if err != nil {
		return nil, err
	}
	return redisop.New(dyn, core, redisop.Config{Namespace: namespace}), nil
}
