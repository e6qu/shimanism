// Sockerless lane for the queue service. See doc/SOCKERLESS_VALIDATION.md.
package conformance_test

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"os"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"google.golang.org/api/option"
	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/queue/domain"
	awsqueue "github.com/e6qu/shimanism/services/queue/backends/aws"
	gcpqueue "github.com/e6qu/shimanism/services/queue/backends/gcp"
)

func insecureAWSHTTPClient() awsapi.HTTPClient {
	return awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	})
}

// TestSockerless_GCP_Queue_RetentionRoundTrip drives the shim's
// GCP queue backend's full lifecycle including the
// MessageRetentionSeconds attribute round-trip:
// CreateQueue → SetQueueAttributes(MessageRetentionSeconds, VisibilityTimeoutSeconds)
// → HeadQueue, asserts the values survive the shim's PATCH update
// + GCP's `messageRetentionDuration` → shim seconds round-trip.
func TestSockerless_GCP_Queue_RetentionRoundTrip(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	ctx := context.Background()
	svc, err := pubsubraw.NewService(ctx,
		option.WithEndpoint("http://"+endpoint+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("pubsub client: %v", err)
	}
	project := os.Getenv("SOCKERLESS_GCP_PROJECT")
	if project == "" {
		project = "shim-sockerless"
	}
	backend := gcpqueue.New(svc, gcpqueue.Config{ProjectID: project})

	queueName := "shim-sk-q-" + sockHex8()
	if _, err := backend.CreateQueue(ctx, queueName, domain.CreateQueueOptions{
		Attributes: domain.QueueAttributes{VisibilityTimeoutSeconds: 30},
	}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteQueue(ctx, queueName) })

	wantRetention := 604800
	if err := backend.SetQueueAttributes(ctx, queueName, domain.QueueAttributes{
		MessageRetentionSeconds:  wantRetention,
		VisibilityTimeoutSeconds: 30,
	}); err != nil {
		t.Fatalf("SetQueueAttributes: %v", err)
	}

	q, err := backend.HeadQueue(ctx, queueName)
	if err != nil {
		t.Fatalf("HeadQueue: %v", err)
	}
	if q.Name != queueName {
		t.Errorf("HeadQueue.Name = %q, want %q", q.Name, queueName)
	}
	if q.Attributes.VisibilityTimeoutSeconds != 30 {
		t.Errorf("VisibilityTimeoutSeconds = %d, want 30", q.Attributes.VisibilityTimeoutSeconds)
	}
	if q.Attributes.MessageRetentionSeconds != wantRetention {
		t.Errorf("MessageRetentionSeconds = %d, want %d", q.Attributes.MessageRetentionSeconds, wantRetention)
	}
}

// TestSockerless_AWS_Queue_AttributeRoundTrip exercises the shim's
// AWS SQS queue backend: CreateQueue Attributes
// (MessageRetentionPeriod, VisibilityTimeout) → HeadQueue, asserts
// the values survive.
func TestSockerless_AWS_Queue_AttributeRoundTrip(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_SM_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_SM_ENDPOINT not set (the AWS sim shares the port; reuse the SM endpoint)")
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		os.Setenv("AWS_ACCESS_KEY_ID", "test")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		os.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	}
	if os.Getenv("AWS_REGION") == "" {
		os.Setenv("AWS_REGION", "us-east-1")
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	if os.Getenv("AWS_S3_CONFORMANCE_INSECURE_TLS") == "1" {
		cfg.HTTPClient = insecureAWSHTTPClient()
	}
	sqsClient := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
	backend := awsqueue.New(sqsClient)
	ctx := context.Background()

	queueName := "shim-sk-q-" + sockHex8()
	if _, err := backend.CreateQueue(ctx, queueName, domain.CreateQueueOptions{
		Attributes: domain.QueueAttributes{
			VisibilityTimeoutSeconds: 60,
			MessageRetentionSeconds:  86400,
		},
	}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteQueue(ctx, queueName) })

	q, err := backend.HeadQueue(ctx, queueName)
	if err != nil {
		t.Fatalf("HeadQueue: %v", err)
	}
	if q.Attributes.VisibilityTimeoutSeconds != 60 {
		t.Errorf("VisibilityTimeoutSeconds = %d, want 60", q.Attributes.VisibilityTimeoutSeconds)
	}
	if q.Attributes.MessageRetentionSeconds != 86400 {
		t.Errorf("MessageRetentionSeconds = %d, want 86400", q.Attributes.MessageRetentionSeconds)
	}
}

func sockHex8() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
