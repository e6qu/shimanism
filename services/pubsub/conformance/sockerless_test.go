// Sockerless lane for the pubsub service. See docs/sockerless-validation.md.
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
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"google.golang.org/api/option"
	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/internal/pubsub/domain"
	gcpbackend "github.com/e6qu/shimanism/services/pubsub/backends/gcp"
)

// TestSockerless_GCP_Pubsub_RoundTrip drives the shim's GCP Pub/Sub
// pubsub backend end-to-end against a running sockerless GCP sim:
// Topic + Subscription + Publish + Receive + Ack + cleanup.
//
// Set SOCKERLESS_GCP_ENDPOINT (e.g. localhost:14567) to opt in.
func TestSockerless_GCP_Pubsub_RoundTrip(t *testing.T) {
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
	backend := gcpbackend.New(svc, gcpbackend.Config{ProjectID: project})

	topic := "shim-sk-" + randomHex8()
	sub := "shim-sk-sub-" + randomHex8()

	if _, err := backend.CreateTopic(ctx, topic, domain.CreateTopicOptions{}); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteTopic(ctx, topic) })

	if _, err := backend.CreateSubscription(ctx, topic, sub, domain.CreateSubscriptionOptions{}); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteSubscription(ctx, sub) })

	if _, err := backend.Publish(ctx, topic, domain.PublishOptions{
		Body: []byte("hello sockerless pubsub"),
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msgs, err := backend.Receive(ctx, sub, domain.ReceiveOptions{MaxMessages: 10})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatalf("Receive returned no messages")
	}
	if got := string(msgs[0].Body); got != "hello sockerless pubsub" {
		t.Errorf("message body = %q, want %q", got, "hello sockerless pubsub")
	}
	for _, m := range msgs {
		if err := backend.Ack(ctx, sub, m.ReceiptHandle); err != nil {
			t.Errorf("Ack: %v", err)
		}
	}
}

// TestSockerless_AWSSNSFrontendToGCPBackend_Fanout drives the full
// through-shim E2E path for pub/sub:
// aws-sdk-go-v2 SNS/SQS clients → AWS-shaped shim frontends → GCP
// Pub/Sub backend → sockerless GCP simulator.
func TestSockerless_AWSSNSFrontendToGCPBackend_Fanout(t *testing.T) {
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
	backend := gcpbackend.New(svc, gcpbackend.Config{ProjectID: project})
	srv := harness.StartPubsubServerAWS(t, backend)

	cfg, err := config.LoadDefaultConfig(ctx,
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
	snsClient := sns.NewFromConfig(cfg, func(o *sns.Options) {
		o.BaseEndpoint = awsapi.String(srv.SnsURL)
	})
	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.BaseEndpoint = awsapi.String(srv.SqsURL)
	})

	topicName := "shim-sk-xtopic-" + randomHex8()
	subName := "shim-sk-xsub-" + randomHex8()
	topicOut, err := snsClient.CreateTopic(ctx, &sns.CreateTopicInput{
		Name: awsapi.String(topicName),
	})
	if err != nil {
		t.Fatalf("CreateTopic through shim: %v", err)
	}
	t.Cleanup(func() {
		_, _ = snsClient.DeleteTopic(ctx, &sns.DeleteTopicInput{TopicArn: topicOut.TopicArn})
	})

	subOut, err := snsClient.Subscribe(ctx, &sns.SubscribeInput{
		TopicArn: topicOut.TopicArn,
		Protocol: awsapi.String("sqs"),
		Endpoint: awsapi.String("arn:aws:sqs:us-east-1:000000000000:" + subName),
	})
	if err != nil {
		t.Fatalf("Subscribe through shim: %v", err)
	}
	t.Cleanup(func() {
		_, _ = snsClient.Unsubscribe(ctx, &sns.UnsubscribeInput{SubscriptionArn: subOut.SubscriptionArn})
	})

	if _, err := snsClient.Publish(ctx, &sns.PublishInput{
		TopicArn: topicOut.TopicArn,
		Message:  awsapi.String("through-shim pubsub body"),
		MessageAttributes: map[string]snstypes.MessageAttributeValue{
			"env": {DataType: awsapi.String("String"), StringValue: awsapi.String("test")},
		},
	}); err != nil {
		t.Fatalf("Publish through shim: %v", err)
	}

	queueURL := srv.SqsURL + "/000000000000/" + subName
	recv, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            awsapi.String(queueURL),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     5,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage through shim: %v", err)
	}
	if len(recv.Messages) != 1 {
		t.Fatalf("ReceiveMessage count = %d, want 1", len(recv.Messages))
	}
	if got := awsapi.ToString(recv.Messages[0].Body); got != "through-shim pubsub body" {
		t.Errorf("Body = %q, want through-shim pubsub body", got)
	}
	if _, err := sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      awsapi.String(queueURL),
		ReceiptHandle: recv.Messages[0].ReceiptHandle,
	}); err != nil {
		t.Fatalf("DeleteMessage through shim: %v", err)
	}
}

func randomHex8() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
