package main

import (
	"github.com/aws/aws-sdk-go-v2/service/elasticache"

	"github.com/e6qu/shimanism/internal/cache/domain"
	awsbackend "github.com/e6qu/shimanism/services/cache/backends/aws"
)

func newAWSCacheBackend(endpoint string) (domain.Cache, error) {
	return newAWSManagedBackend(endpoint, elasticache.NewFromConfig, setElastiCacheEndpoint,
		func(c *elasticache.Client) domain.Cache { return awsbackend.New(c) })
}

func setElastiCacheEndpoint(opts *elasticache.Options, endpoint *string) {
	opts.BaseEndpoint = endpoint
}
