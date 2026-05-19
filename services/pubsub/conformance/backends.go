// Package conformance hosts the backend factories used by the
// parameterised pubsub conformance tests. Tests live in
// `package conformance_test` (external tests) and import this set.
package conformance

import (
	"context"
	"os"
	"testing"

	azservicebusadmin "github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"
	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	natsapi "github.com/nats-io/nats.go"
	"google.golang.org/api/option"
	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/pubsub/domain"
	awsbackend "github.com/e6qu/shimanism/services/pubsub/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/pubsub/backends/azure"
	gcpbackend "github.com/e6qu/shimanism/services/pubsub/backends/gcp"
	"github.com/e6qu/shimanism/services/pubsub/backends/inmem"
	natsbackend "github.com/e6qu/shimanism/services/pubsub/backends/nats"
)

type BackendFactory struct {
	Name string
	Fn   func(t *testing.T) domain.Pubsub
}

func ActiveBackends() []BackendFactory {
	return []BackendFactory{
		{Name: "inmem", Fn: NewInmem},
		{Name: "nats", Fn: NewNATS},
		{Name: "aws", Fn: NewAWS},
		{Name: "gcp", Fn: NewGCP},
		{Name: "azure", Fn: NewAzure},
	}
}

func NewInmem(t *testing.T) domain.Pubsub {
	t.Helper()
	return inmem.New()
}

func NewNATS(t *testing.T) domain.Pubsub {
	t.Helper()
	url := os.Getenv("NATS_URL")
	if url == "" {
		t.Skip("NATS_URL not set (NATS JetStream pubsub backend conformance disabled)")
	}
	nc, err := natsapi.Connect(url)
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	t.Cleanup(nc.Close)
	b, err := natsbackend.New(nc)
	if err != nil {
		t.Fatalf("new NATS pubsub backend: %v", err)
	}
	return b
}

func NewAWS(t *testing.T) domain.Pubsub {
	t.Helper()
	if os.Getenv("AWS_SNS_CONFORMANCE") != "1" {
		t.Skip("AWS_SNS_CONFORMANCE!=1 (AWS SNS+SQS pubsub backend conformance disabled)")
	}
	cfg, err := awscfg.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	snsOpts := []func(*sns.Options){}
	if endpoint := os.Getenv("AWS_SNS_ENDPOINT"); endpoint != "" {
		snsOpts = append(snsOpts, func(o *sns.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}
	sqsOpts := []func(*sqs.Options){}
	if endpoint := os.Getenv("AWS_SQS_ENDPOINT"); endpoint != "" {
		sqsOpts = append(sqsOpts, func(o *sqs.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}
	return awsbackend.New(
		sns.NewFromConfig(cfg, snsOpts...),
		sqs.NewFromConfig(cfg, sqsOpts...),
		awsbackend.Config{Region: "us-east-1"},
	)
}

func NewGCP(t *testing.T) domain.Pubsub {
	t.Helper()
	if os.Getenv("GCP_PUBSUB_CONFORMANCE") != "1" {
		t.Skip("GCP_PUBSUB_CONFORMANCE!=1 (GCP Pub/Sub pubsub backend conformance disabled)")
	}
	project := os.Getenv("GCP_PROJECT_ID")
	if project == "" {
		t.Skip("GCP_PROJECT_ID not set")
	}
	svc, err := pubsubraw.NewService(context.Background(), option.WithUserAgent("shimanism-pubsub-conformance"))
	if err != nil {
		t.Fatalf("new GCP pubsub service: %v", err)
	}
	return gcpbackend.New(svc, gcpbackend.Config{ProjectID: project})
}

func NewAzure(t *testing.T) domain.Pubsub {
	t.Helper()
	conn := os.Getenv("AZURE_SERVICEBUS_CONNECTION_STRING")
	if conn == "" {
		t.Skip("AZURE_SERVICEBUS_CONNECTION_STRING not set (Azure Service Bus pubsub backend conformance disabled)")
	}
	b, err := azurebackend.New(azurebackend.Config{ConnectionString: conn})
	if err != nil {
		t.Fatalf("new Azure pubsub backend: %v", err)
	}
	return b
}

var _ = azservicebusadmin.Client{}
