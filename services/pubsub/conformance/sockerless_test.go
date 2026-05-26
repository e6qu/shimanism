// Sockerless lane for the pubsub service. See docs/sockerless-validation.md.
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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"
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
	azurepubsub "github.com/e6qu/shimanism/services/pubsub/backends/azure"
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

// TestSockerless_Azure_ServiceBus_Topic_CRUD exercises the shim's
// Azure Service Bus pubsub backend's admin surface against
// sockerless's namespace-level ATOM XML admin protocol (added in
// sockerless PR #225, 2026-05-26). CreateTopic → CreateSubscription
// → ListTopics → ListSubscriptions → DeleteSubscription → DeleteTopic.
//
// The AMQP data plane (Publish / Receive / Ack) is not exercised —
// sockerless implements the REST data plane but not AMQP, and the
// shim's azservicebus data client speaks AMQP. Admin-only.
func TestSockerless_Azure_ServiceBus_Topic_CRUD(t *testing.T) {
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	backend, err := azurepubsub.New(azurepubsub.Config{
		ConnectionString:   sockerlessAzureSBConnectionStringPubsub(),
		AdminClientOptions: sockerlessSBAdminClientOptionsPubsub(port),
	})
	if err != nil {
		t.Fatalf("azurepubsub.New: %v", err)
	}
	ctx := context.Background()

	topic := "shim-sk-sbt-" + randomHex8()
	if _, err := backend.CreateTopic(ctx, topic, domain.CreateTopicOptions{}); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteTopic(ctx, topic) })

	sub := "shim-sk-sbs-" + randomHex8()
	if _, err := backend.CreateSubscription(ctx, topic, sub, domain.CreateSubscriptionOptions{
		AckDeadlineSeconds: 30,
	}); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteSubscription(ctx, sub) })

	topics, err := backend.ListTopics(ctx, domain.ListTopicsOptions{})
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}
	foundTopic := false
	for _, top := range topics.Topics {
		if top.Name == topic {
			foundTopic = true
			break
		}
	}
	if !foundTopic {
		t.Errorf("ListTopics did not contain %q", topic)
	}

	subs, err := backend.ListSubscriptions(ctx, domain.ListSubscriptionsOptions{
		Topic: topic,
	})
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	foundSub := false
	for _, s := range subs.Subscriptions {
		if s.Name == sub {
			foundSub = true
			break
		}
	}
	if !foundSub {
		t.Errorf("ListSubscriptions did not contain %q under topic %q", sub, topic)
	}
}

func sockerlessAzureSBConnectionStringPubsub() string {
	return "Endpoint=sb://test-ns.servicebus.windows.net/;" +
		"SharedAccessKeyName=RootManageSharedAccessKey;" +
		"SharedAccessKey=c29ja2VybGVzcy10ZXN0LWtleS1ub3QtdmVyaWZpZWQK"
}

func sockerlessSBAdminClientOptionsPubsub(port string) *admin.ClientOptions {
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "127.0.0.1:"+port)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &admin.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: &http.Client{Transport: transport},
		},
	}
}
