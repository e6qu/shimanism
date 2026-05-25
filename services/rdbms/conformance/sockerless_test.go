// Sockerless lane for the rdbms service. See doc/SOCKERLESS_VALIDATION.md.
package conformance_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/e6qu/shimanism/internal/harness"
	gcpbackend "github.com/e6qu/shimanism/services/rdbms/backends/gcp"
)

// TestSockerless_AWSRDSFrontendToGCPBackend_CRUD drives the full
// through-shim E2E path for relational databases:
// aws-sdk-go-v2 RDS client → AWS-shaped shim frontend → GCP Cloud SQL
// backend → sockerless GCP simulator.
func TestSockerless_AWSRDSFrontendToGCPBackend_CRUD(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	ctx := context.Background()
	svc, err := sqladmin.NewService(ctx,
		option.WithEndpoint("http://"+endpoint+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("cloud sql client: %v", err)
	}
	project := os.Getenv("SOCKERLESS_GCP_PROJECT")
	if project == "" {
		project = "shim-sockerless"
	}
	backend := gcpbackend.New(svc, gcpbackend.Config{ProjectID: project, Region: "us-central1"})
	srv := harness.StartRDBMSServerAWS(t, backend)
	client := newSockerlessRDSClient(t, srv.URL)

	name := "shim-sk-db-" + sockerlessHex8()
	create, err := client.CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: awsapi.String(name),
		Engine:               awsapi.String("postgres"),
		EngineVersion:        awsapi.String("15"),
		DBInstanceClass:      awsapi.String("db-perf-optimized-N-2"),
		MasterUsername:       awsapi.String("shimadmin"),
		MasterUserPassword:   awsapi.String("supersecret"),
		AllocatedStorage:     awsapi.Int32(20),
	})
	if err != nil {
		t.Fatalf("CreateDBInstance through shim: %v", err)
	}
	if awsapi.ToString(create.DBInstance.DBInstanceIdentifier) != name {
		t.Errorf("CreateDBInstance identifier = %q, want %q",
			awsapi.ToString(create.DBInstance.DBInstanceIdentifier), name)
	}
	t.Cleanup(func() {
		_, _ = client.DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{
			DBInstanceIdentifier: awsapi.String(name),
			SkipFinalSnapshot:    awsapi.Bool(true),
		})
	})

	describe, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: awsapi.String(name),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances through shim: %v", err)
	}
	if len(describe.DBInstances) != 1 {
		t.Fatalf("DescribeDBInstances count = %d, want 1", len(describe.DBInstances))
	}

	if _, err := client.ModifyDBInstance(ctx, &rds.ModifyDBInstanceInput{
		DBInstanceIdentifier: awsapi.String(name),
		AllocatedStorage:     awsapi.Int32(40),
	}); err != nil {
		t.Fatalf("ModifyDBInstance through shim: %v", err)
	}
}

func newSockerlessRDSClient(t *testing.T, endpoint string) *rds.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return rds.NewFromConfig(cfg, func(o *rds.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
}

func sockerlessHex8() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
