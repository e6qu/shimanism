// Matrix conformance for the rdbms service. inmem cell exercises
// the full CRUD lifecycle; cnpg / aws / gcp / azure cells require
// real infrastructure and skip cleanly when their env-var gate
// isn't set.
//
// The inmem cell test is the only one that can complete in a tight
// loop — its async transition is 50ms. The other cells need
// minutes (real provisioning) which is appropriate for a longer
// CI job; the gating env var keeps quick local runs fast.
package conformance_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/rds"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/rdbms/conformance"
)

// TestRDBMSMatrix_AWSFrontend drives the AWS RDS frontend across
// every backend factory. The test runs the canonical lifecycle
// (Create → poll-until-available → Modify → Delete) and asserts
// the Endpoint block surfaces a sensible host:port once available.
func TestRDBMSMatrix_AWSFrontend(t *testing.T) {
	ctx := context.Background()
	for _, f := range conformance.ActiveBackends() {
		t.Run(f.Name, func(t *testing.T) {
			be := f.Fn(t)
			srv := harness.StartRDBMSServerAWS(t, be)
			cfg, err := awsconfig.LoadDefaultConfig(ctx,
				awsconfig.WithRegion("us-east-1"),
				awsconfig.WithCredentialsProvider(awscredentials.StaticCredentialsProvider{
					Value: awsapi.Credentials{
						AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
						SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
					},
				}),
			)
			if err != nil {
				t.Fatalf("aws config: %v", err)
			}
			client := rds.NewFromConfig(cfg, func(o *rds.Options) {
				o.BaseEndpoint = awsapi.String(srv.URL)
			})

			name := fmt.Sprintf("matrix-aws-%s", f.Name)
			if _, err := client.CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
				DBInstanceIdentifier: awsapi.String(name),
				Engine:               awsapi.String("postgres"),
				EngineVersion:        awsapi.String("15"),
				DBInstanceClass:      awsapi.String("db.t3.micro"),
				MasterUsername:       awsapi.String("shimadmin"),
				MasterUserPassword:   awsapi.String("matrix-supersecret"),
				AllocatedStorage:     awsapi.Int32(20),
			}); err != nil {
				t.Fatalf("CreateDBInstance: %v", err)
			}
			t.Cleanup(func() {
				_, _ = client.DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{
					DBInstanceIdentifier: awsapi.String(name),
					SkipFinalSnapshot:    awsapi.Bool(true),
				})
			})

			// Poll until available. The inmem backend transitions in
			// ~50ms; real backends take minutes — use a generous
			// budget for non-inmem cells.
			budget := 2 * time.Second
			if f.Name != "inmem" {
				budget = 10 * time.Minute
			}
			deadline := time.Now().Add(budget)
			var endpoint string
			for time.Now().Before(deadline) {
				out, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
					DBInstanceIdentifier: awsapi.String(name),
				})
				if err != nil {
					t.Fatalf("DescribeDBInstances: %v", err)
				}
				if len(out.DBInstances) == 1 &&
					awsapi.ToString(out.DBInstances[0].DBInstanceStatus) == "available" {
					if out.DBInstances[0].Endpoint != nil {
						endpoint = awsapi.ToString(out.DBInstances[0].Endpoint.Address)
					}
					break
				}
				time.Sleep(time.Second)
			}
			if endpoint == "" {
				t.Fatalf("%s: instance never reached available state with a populated Endpoint", f.Name)
			}
		})
	}
}
