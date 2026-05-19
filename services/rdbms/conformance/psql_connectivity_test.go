// Phase 5 exit criterion (sub-phase 5.15): the shim provisions a
// CloudNativePG cluster via the AWS RDS frontend, then the test
// opens a *real* PostgreSQL connection to the returned host:port
// and runs SELECT 1. This is what makes Phase 5 honest — the shim
// is a control plane only; the data plane is the actual Postgres
// the cluster brings up, not a translated wire protocol.
//
// Gated on CNPG_CONFORMANCE=1 + KUBECONFIG so the test is opt-in.
// CI's conformance-cnpg lane sets both. Locally requires a running
// K8s cluster with CloudNativePG operator installed.
package conformance_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/rdbms/conformance"
)

func TestPsqlConnectivity_CNPG(t *testing.T) {
	if os.Getenv("CNPG_CONFORMANCE") != "1" {
		t.Skip("CNPG_CONFORMANCE!=1 (psql connectivity test against CloudNativePG disabled)")
	}
	ctx := context.Background()
	be := conformance.NewCNPG(t)
	srv := harness.StartRDBMSServerAWS(t, be)
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(awsapi.AnonymousCredentials{}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	client := rds.NewFromConfig(cfg, func(o *rds.Options) {
		o.BaseEndpoint = awsapi.String(srv.URL)
	})

	const name = "psql-test"
	const password = "shim-conformance-pw"
	if _, err := client.CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: awsapi.String(name),
		Engine:               awsapi.String("postgres"),
		EngineVersion:        awsapi.String("16"),
		DBInstanceClass:      awsapi.String("db.t3.micro"),
		MasterUsername:       awsapi.String("shimadmin"),
		MasterUserPassword:   awsapi.String(password),
		AllocatedStorage:     awsapi.Int32(1),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}
	t.Cleanup(func() {
		_, _ = client.DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{
			DBInstanceIdentifier: awsapi.String(name),
			SkipFinalSnapshot:    awsapi.Bool(true),
		})
	})

	// Poll until the cluster reports available + Endpoint populated.
	deadline := time.Now().Add(10 * time.Minute)
	var endpoint string
	for time.Now().Before(deadline) {
		out, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
			DBInstanceIdentifier: awsapi.String(name),
		})
		if err != nil {
			t.Logf("DescribeDBInstances (will retry): %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if len(out.DBInstances) == 1 &&
			awsapi.ToString(out.DBInstances[0].DBInstanceStatus) == "available" &&
			out.DBInstances[0].Endpoint != nil {
			endpoint = awsapi.ToString(out.DBInstances[0].Endpoint.Address)
			break
		}
		time.Sleep(5 * time.Second)
	}
	if endpoint == "" {
		t.Fatal("CloudNativePG cluster never reached available state")
	}
	t.Logf("cluster ready, endpoint=%s", endpoint)

	// Open a *real* PostgreSQL connection through the returned
	// Connection block. The endpoint host is the cnpg-operator-
	// emitted Service DNS (<name>-rw.<ns>.svc.cluster.local). In CI,
	// the test runs inside the kind cluster (sidecar pod) so this
	// DNS resolves; locally, the kubectl-port-forward dance is
	// the caller's responsibility.
	dsn := fmt.Sprintf("host=%s port=5432 user=shimadmin password=%s dbname=postgres sslmode=require",
		endpoint, password)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		t.Fatalf("db.Ping: %v", err)
	}

	var got int
	if err := db.QueryRowContext(pingCtx, "SELECT 1").Scan(&got); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if got != 1 {
		t.Errorf("SELECT 1 returned %d, want 1", got)
	}
}
