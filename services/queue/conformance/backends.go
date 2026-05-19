// Package conformance hosts the backend factories used by the
// parameterised queue conformance tests. Tests live in
// `package conformance_test` (external tests) and import this set.
//
// Each factory either returns a backend ready for use, or calls
// t.Skip when its required infrastructure isn't available. CI
// lights up one backend per lane via env vars without modifying
// test source.
package conformance

import (
	"context"
	"os"
	"testing"

	azservicebusadmin "github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	natsapi "github.com/nats-io/nats.go"
	"google.golang.org/api/option"
	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/queue/domain"
	awsbackend "github.com/e6qu/shimanism/services/queue/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/queue/backends/azure"
	gcpbackend "github.com/e6qu/shimanism/services/queue/backends/gcp"
	"github.com/e6qu/shimanism/services/queue/backends/inmem"
	natsbackend "github.com/e6qu/shimanism/services/queue/backends/nats"
)

// BackendFactory returns a Queues backend ready for use. Each
// factory may call t.Skip if its required infrastructure isn't
// available.
type BackendFactory struct {
	Name string
	Fn   func(t *testing.T) domain.Queues
}

// ActiveBackends returns the set of backend factories the matrix
// tests iterate over. inmem is always available; the others skip
// if their infrastructure or env-var gate is missing.
func ActiveBackends() []BackendFactory {
	return []BackendFactory{
		{Name: "inmem", Fn: NewInmem},
		{Name: "nats", Fn: NewNATS},
		{Name: "aws", Fn: NewAWS},
		{Name: "gcp", Fn: NewGCP},
		{Name: "azure", Fn: NewAzure},
	}
}

// NewInmem is always available — no external dependencies.
func NewInmem(t *testing.T) domain.Queues {
	t.Helper()
	return inmem.New()
}

// NewNATS connects to a NATS JetStream server using NATS_URL. CI's
// conformance-nats lane starts a NATS container + sets NATS_URL.
func NewNATS(t *testing.T) domain.Queues {
	t.Helper()
	url := os.Getenv("NATS_URL")
	if url == "" {
		t.Skip("NATS_URL not set (NATS JetStream backend conformance disabled)")
	}
	nc, err := natsapi.Connect(url)
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	t.Cleanup(nc.Close)
	b, err := natsbackend.New(nc)
	if err != nil {
		t.Fatalf("new NATS backend: %v", err)
	}
	return b
}

// NewAWS connects to real AWS SQS when AWS_SQS_CONFORMANCE=1. CI
// installs no SQS emulator; this lane lights up only with real
// cloud credentials (Track A).
func NewAWS(t *testing.T) domain.Queues {
	t.Helper()
	if os.Getenv("AWS_SQS_CONFORMANCE") != "1" {
		t.Skip("AWS_SQS_CONFORMANCE!=1 (AWS SQS backend conformance disabled)")
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	opts := []func(*awssqs.Options){}
	if endpoint := os.Getenv("AWS_SQS_ENDPOINT"); endpoint != "" {
		opts = append(opts, func(o *awssqs.Options) {
			o.BaseEndpoint = awsapi.String(endpoint)
		})
	}
	return awsbackend.New(awssqs.NewFromConfig(cfg, opts...))
}

// NewGCP connects to real GCP Pub/Sub when GCP_PUBSUB_CONFORMANCE=1
// and GCP_PROJECT_ID are set. CI awaits Track A.
func NewGCP(t *testing.T) domain.Queues {
	t.Helper()
	if os.Getenv("GCP_PUBSUB_CONFORMANCE") != "1" {
		t.Skip("GCP_PUBSUB_CONFORMANCE!=1 (GCP Pub/Sub backend conformance disabled)")
	}
	project := os.Getenv("GCP_PROJECT_ID")
	if project == "" {
		t.Skip("GCP_PROJECT_ID not set")
	}
	svc, err := pubsubraw.NewService(context.Background(), option.WithUserAgent("shimanism-conformance"))
	if err != nil {
		t.Fatalf("new GCP pubsub service: %v", err)
	}
	return gcpbackend.New(svc, gcpbackend.Config{ProjectID: project})
}

// NewAzure connects to real Azure Service Bus when
// AZURE_SERVICEBUS_CONNECTION_STRING is set.
func NewAzure(t *testing.T) domain.Queues {
	t.Helper()
	conn := os.Getenv("AZURE_SERVICEBUS_CONNECTION_STRING")
	if conn == "" {
		t.Skip("AZURE_SERVICEBUS_CONNECTION_STRING not set (Azure Service Bus backend conformance disabled)")
	}
	b, err := azurebackend.New(azurebackend.Config{ConnectionString: conn})
	if err != nil {
		t.Fatalf("new Azure backend: %v", err)
	}
	return b
}

// silence unused-import warning if the admin SDK migrates.
var _ = azservicebusadmin.Client{}
