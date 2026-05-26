// Sockerless lane for the rdbms service. See docs/sockerless-validation.md.
package conformance_test

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/e6qu/shimanism/internal/harness"
	rdbmsdomain "github.com/e6qu/shimanism/internal/rdbms/domain"
	azurerdbms "github.com/e6qu/shimanism/services/rdbms/backends/azure"
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

// TestSockerless_Azure_RDBMS_PostgreSQL_CRUD exercises the shim's
// Azure PostgreSQL FlexibleServer rdbms backend against sockerless's
// Microsoft.DBforPostgreSQL/flexibleServers ARM control plane:
// CreateInstance → DescribeInstance → ListInstances → DeleteInstance.
// The PG wire protocol is not in scope for sockerless.
func TestSockerless_Azure_RDBMS_PostgreSQL_CRUD(t *testing.T) {
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	backend, err := azurerdbms.New(azurerdbms.Config{
		SubscriptionID: sockerlessAzureSubscription(),
		ResourceGroup:  "shim-sk-rg",
		Location:       "eastus",
		Credential:     sockerlessNoOpCredential{},
		ClientOptions:  sockerlessARMClientOptions(port),
	})
	if err != nil {
		t.Fatalf("azurerdbms.New: %v", err)
	}
	ctx := context.Background()

	name := "shim-sk-pg-" + sockerlessHex8()
	create, err := backend.CreateInstance(ctx, name, rdbmsdomain.CreateInstanceOptions{
		Engine:             rdbmsdomain.EnginePostgres,
		EngineVersion:      "15",
		MasterUsername:     "shimadmin",
		MasterPassword:     "Sup3rs3cret!",
		AllocatedStorageGB: 32,
		InstanceClass:      "Standard_B1ms",
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if create.Instance.Name != name {
		t.Errorf("CreateInstance.Name = %q, want %q", create.Instance.Name, name)
	}
	t.Cleanup(func() { _ = backend.DeleteInstance(ctx, name) })

	got, err := backend.DescribeInstance(ctx, name)
	if err != nil {
		t.Fatalf("DescribeInstance: %v", err)
	}
	if got.Name != name {
		t.Errorf("DescribeInstance.Name = %q, want %q", got.Name, name)
	}

	list, err := backend.ListInstances(ctx, rdbmsdomain.ListInstancesOptions{})
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	found := false
	for _, in := range list.Instances {
		if in.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListInstances did not contain %q", name)
	}
}

// sockerlessARMClientOptions builds an arm.ClientOptions targeting
// sockerless's ARM endpoint on TLS port `port`.
func sockerlessARMClientOptions(port string) *arm.ClientOptions {
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "127.0.0.1:"+port)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://localhost:" + port + "/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {
						Audience: "https://management.azure.com",
						Endpoint: "https://localhost:" + port,
					},
				},
			},
			Transport: &http.Client{Transport: transport},
		},
	}
}

func sockerlessAzureSubscription() string {
	if s := os.Getenv("SOCKERLESS_AZURE_SUBSCRIPTION"); s != "" {
		return s
	}
	return "00000000-0000-0000-0000-000000000000"
}

type sockerlessNoOpCredential struct{}

var sockerlessFarFuture = time.Date(2099, time.December, 31, 23, 59, 59, 0, time.UTC)

func (sockerlessNoOpCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "sockerless-test", ExpiresOn: sockerlessFarFuture}, nil
}
