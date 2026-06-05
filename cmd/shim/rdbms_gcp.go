package main

import (
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
	gcpbackend "github.com/e6qu/shimanism/services/rdbms/backends/gcp"
)

func newGCPRDBMSBackend(project, region string) (domain.RDBMS, error) {
	return newGCPManagedBackend(project, region, "shimanism-rdbms", "connect to Cloud SQL Admin",
		sqladmin.NewService, rdbmsGCPConfig, rdbmsGCPBackend)
}

func rdbmsGCPConfig(project, region string) gcpbackend.Config {
	return gcpbackend.Config{ProjectID: project, Region: region}
}

func rdbmsGCPBackend(svc *sqladmin.Service, cfg gcpbackend.Config) domain.RDBMS {
	return gcpbackend.New(svc, cfg)
}
