// Phase 5 conformance: AWS RDS-shaped frontend exercised by the
// official `aws-sdk-go-v2/service/rds` SDK against the in-mem
// backend. Verifies the full Create → poll → Modify → Snapshot →
// Restore → Delete lifecycle with the explicit Status transitions
// the domain surfaces.
package conformance_test

import (
	"context"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/rds"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/rdbms/backends/inmem"
)

func TestAWSSDK_RDBMSLifecycle(t *testing.T) {
	srv := harness.StartRDBMSServerAWS(t, inmem.New())
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(awsapi.AnonymousCredentials{}),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	client := rds.NewFromConfig(cfg, func(o *rds.Options) {
		o.BaseEndpoint = awsapi.String(srv.URL)
	})

	// CreateDBInstance.
	cOut, err := client.CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: awsapi.String("shim-test"),
		Engine:               awsapi.String("postgres"),
		EngineVersion:        awsapi.String("15"),
		DBInstanceClass:      awsapi.String("db.t3.micro"),
		MasterUsername:       awsapi.String("shimadmin"),
		MasterUserPassword:   awsapi.String("supersecret"),
		AllocatedStorage:     awsapi.Int32(20),
	})
	if err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}
	if awsapi.ToString(cOut.DBInstance.DBInstanceIdentifier) != "shim-test" {
		t.Fatalf("CreateDBInstance returned identifier = %q",
			awsapi.ToString(cOut.DBInstance.DBInstanceIdentifier))
	}

	// Poll DescribeDBInstances until status flips to available.
	deadline := time.Now().Add(2 * time.Second)
	var available bool
	for time.Now().Before(deadline) {
		dOut, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
			DBInstanceIdentifier: awsapi.String("shim-test"),
		})
		if err != nil {
			t.Fatalf("DescribeDBInstances: %v", err)
		}
		if len(dOut.DBInstances) == 1 &&
			awsapi.ToString(dOut.DBInstances[0].DBInstanceStatus) == "available" {
			available = true
			if dOut.DBInstances[0].Endpoint == nil {
				t.Fatalf("available instance missing Endpoint")
			}
			if awsapi.ToInt32(dOut.DBInstances[0].Endpoint.Port) != 5432 {
				t.Errorf("Endpoint.Port = %d, want 5432",
					awsapi.ToInt32(dOut.DBInstances[0].Endpoint.Port))
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !available {
		t.Fatal("instance never reached available state")
	}

	// ModifyDBInstance.
	if _, err := client.ModifyDBInstance(ctx, &rds.ModifyDBInstanceInput{
		DBInstanceIdentifier: awsapi.String("shim-test"),
		AllocatedStorage:     awsapi.Int32(40),
	}); err != nil {
		t.Errorf("ModifyDBInstance: %v", err)
	}

	// CreateDBSnapshot + DescribeDBSnapshots.
	if _, err := client.CreateDBSnapshot(ctx, &rds.CreateDBSnapshotInput{
		DBInstanceIdentifier: awsapi.String("shim-test"),
		DBSnapshotIdentifier: awsapi.String("shim-test-snap"),
	}); err != nil {
		t.Fatalf("CreateDBSnapshot: %v", err)
	}
	sOut, err := client.DescribeDBSnapshots(ctx, &rds.DescribeDBSnapshotsInput{
		DBSnapshotIdentifier: awsapi.String("shim-test-snap"),
	})
	if err != nil {
		t.Errorf("DescribeDBSnapshots: %v", err)
	}
	if len(sOut.DBSnapshots) != 1 {
		t.Errorf("DescribeDBSnapshots count = %d, want 1", len(sOut.DBSnapshots))
	}

	// RestoreDBInstanceFromDBSnapshot.
	if _, err := client.RestoreDBInstanceFromDBSnapshot(ctx, &rds.RestoreDBInstanceFromDBSnapshotInput{
		DBInstanceIdentifier: awsapi.String("shim-test-restored"),
		DBSnapshotIdentifier: awsapi.String("shim-test-snap"),
	}); err != nil {
		t.Errorf("RestoreDBInstanceFromDBSnapshot: %v", err)
	}

	// Tear down.
	if _, err := client.DeleteDBSnapshot(ctx, &rds.DeleteDBSnapshotInput{
		DBSnapshotIdentifier: awsapi.String("shim-test-snap"),
	}); err != nil {
		t.Errorf("DeleteDBSnapshot: %v", err)
	}
	if _, err := client.DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{
		DBInstanceIdentifier: awsapi.String("shim-test"),
		SkipFinalSnapshot:    awsapi.Bool(true),
	}); err != nil {
		t.Errorf("DeleteDBInstance: %v", err)
	}
	if _, err := client.DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{
		DBInstanceIdentifier: awsapi.String("shim-test-restored"),
		SkipFinalSnapshot:    awsapi.Bool(true),
	}); err != nil {
		t.Errorf("DeleteDBInstance restored: %v", err)
	}
}
