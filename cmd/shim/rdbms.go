// rdbms subcommand wiring.

package main

import "github.com/e6qu/shimanism/internal/rdbms/domain"

var rdbmsControl = peerManagedControl("rdbms", ":9400", "aws_rds",
	"aws_rds, gcp_cloudsql, azure_dbadmin", "inmem, cnpg, aws, gcp, azure",
	"cnpg", "CNPG_NAMESPACE", "cnpg resources", "AWS_RDS_ENDPOINT", "AWS RDS endpoint override")

func runRDBMS(args []string) error {
	return runManagedControl(args, managedControl(rdbmsControl, buildRDBMSBackend, buildRDBMSFrontend))
}

func buildRDBMSBackend(name string, cfg managedBackendConfig) (domain.RDBMS, error) {
	return buildManagedBackend(name, cfg, managedBackendBuilders[domain.RDBMS]{
		valid:    "inmem, cnpg, aws, gcp, azure",
		peerName: "cnpg",
		inmem:    newInmemRDBMSBackend,
		peer:     newCNPGBackend,
		aws:      newAWSRDBMSBackend,
		gcp:      newGCPRDBMSBackend,
		azure:    newAzureRDBMSBackend,
	})
}
