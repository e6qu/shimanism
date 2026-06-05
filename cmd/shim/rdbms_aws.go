package main

import (
	"github.com/aws/aws-sdk-go-v2/service/rds"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
	awsbackend "github.com/e6qu/shimanism/services/rdbms/backends/aws"
)

func newAWSRDBMSBackend(endpoint string) (domain.RDBMS, error) {
	return newAWSManagedBackend(endpoint, rds.NewFromConfig, setRDSEndpoint,
		func(c *rds.Client) domain.RDBMS { return awsbackend.New(c) })
}

func setRDSEndpoint(opts *rds.Options, endpoint *string) {
	opts.BaseEndpoint = endpoint
}
