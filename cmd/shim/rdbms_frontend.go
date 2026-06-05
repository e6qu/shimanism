package main

import (
	"net/http"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
	awsrdsfront "github.com/e6qu/shimanism/internal/rdbms/frontends/aws_rds"
	azuredbadminfront "github.com/e6qu/shimanism/internal/rdbms/frontends/azure_dbadmin"
	gcpcloudsqlfront "github.com/e6qu/shimanism/internal/rdbms/frontends/gcp_cloudsql"
)

func buildRDBMSFrontend(name string, backend domain.RDBMS) (http.Handler, error) {
	return selectedFrontend(name, "aws_rds, gcp_cloudsql, azure_dbadmin", backend, map[string]func(domain.RDBMS) http.Handler{
		"aws_rds":       awsrdsfront.New,
		"gcp_cloudsql":  func(b domain.RDBMS) http.Handler { return gcpcloudsqlfront.New(b) },
		"azure_dbadmin": func(b domain.RDBMS) http.Handler { return azuredbadminfront.New(b) },
	})
}
