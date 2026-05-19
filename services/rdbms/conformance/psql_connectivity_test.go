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
	"net"
	"os"
	"os/exec"
	"strings"
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

	// The endpoint is the cnpg-operator-emitted Service DNS
	// (<name>-rw.<ns>.svc.cluster.local), which is what an
	// in-cluster client would consume. Since the test runs OUTSIDE
	// the kind cluster (on the GitHub runner), we use kubectl
	// port-forward to expose the Service on a local port. In a
	// production deployment the shim runs inside the cluster too,
	// so the Connection block stays correct for the real consumer.
	host, port := portForwardService(t, ctx, endpoint)

	dsn := fmt.Sprintf("host=%s port=%d user=shimadmin password=%s dbname=postgres sslmode=disable",
		host, port, password)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// Give the cluster a beat to actually accept connections — the
	// "available" status flips as soon as the primary pod reports
	// Ready, but the network can still be a bit racy.
	var pingErr error
	for i := 0; i < 30; i++ {
		if err := db.PingContext(pingCtx); err == nil {
			pingErr = nil
			break
		} else {
			pingErr = err
			time.Sleep(2 * time.Second)
		}
	}
	if pingErr != nil {
		t.Fatalf("db.Ping (after retries): %v", pingErr)
	}

	var got int
	if err := db.QueryRowContext(pingCtx, "SELECT 1").Scan(&got); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if got != 1 {
		t.Errorf("SELECT 1 returned %d, want 1", got)
	}
}

// portForwardService spawns `kubectl port-forward` against the
// cnpg-operator's `<cluster>-rw` Service so the test (running on
// the host outside the kind cluster) can dial Postgres. Returns
// ("localhost", localPort) once the port is accepting connections.
//
// Parses cluster name + namespace from the in-cluster DNS:
// <cluster>-rw.<namespace>.svc.cluster.local
func portForwardService(t *testing.T, ctx context.Context, endpoint string) (string, int) {
	t.Helper()
	parts := strings.Split(endpoint, ".")
	if len(parts) < 2 || !strings.HasSuffix(parts[0], "-rw") {
		t.Fatalf("unexpected endpoint shape %q (want <cluster>-rw.<ns>.svc...)", endpoint)
	}
	svcName := parts[0]
	namespace := parts[1]

	// Pick a free local port so parallel runs don't collide.
	lst, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	localPort := lst.Addr().(*net.TCPAddr).Port
	lst.Close()

	cmd := exec.CommandContext(ctx, "kubectl",
		"-n", namespace,
		"port-forward",
		"svc/"+svcName,
		fmt.Sprintf("%d:5432", localPort),
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start kubectl port-forward: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
	})

	// Poll until the local port is accepting connections.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), time.Second)
		if err == nil {
			conn.Close()
			return "127.0.0.1", localPort
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("kubectl port-forward never opened localhost:%d", localPort)
	return "", 0
}
