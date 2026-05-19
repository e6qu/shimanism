// Phase 4 conformance: AWS SNS+SQS fanout exercised by the
// official aws-sdk-go-v2 SNS + SQS clients. The shim spins up two
// HTTP endpoints (SNS publish, SQS receive) wired to the same
// pubsub backend; the test verifies the canonical fanout flow
// CreateTopic → Subscribe → Publish → Receive → DeleteMessage.
package conformance_test

import (
	"context"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/pubsub/backends/inmem"
)

func TestAWSSDK_PubsubFanout(t *testing.T) {
	srv := harness.StartPubsubServerAWS(t, inmem.New())
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(awsapi.AnonymousCredentials{}),
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

	// Create topic.
	topicOut, err := snsClient.CreateTopic(ctx, &sns.CreateTopicInput{
		Name: awsapi.String("fanout-orders"),
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	if awsapi.ToString(topicOut.TopicArn) == "" {
		t.Fatal("CreateTopic returned empty TopicArn")
	}

	// Subscribe an SQS endpoint. The shim derives the subscription
	// name from the endpoint ARN's last segment.
	queueArn := "arn:aws:sqs:us-east-1:000000000000:fanout-orders-sub-a"
	subOut, err := snsClient.Subscribe(ctx, &sns.SubscribeInput{
		TopicArn: topicOut.TopicArn,
		Protocol: awsapi.String("sqs"),
		Endpoint: awsapi.String(queueArn),
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if awsapi.ToString(subOut.SubscriptionArn) == "" {
		t.Fatal("Subscribe returned empty SubscriptionArn")
	}

	// Publish.
	if _, err := snsClient.Publish(ctx, &sns.PublishInput{
		TopicArn: topicOut.TopicArn,
		Message:  awsapi.String("hello-fanout"),
		MessageAttributes: map[string]snstypes.MessageAttributeValue{
			"env": {DataType: awsapi.String("String"), StringValue: awsapi.String("test")},
		},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Receive from the subscription's SQS surface.
	queueURL := srv.SqsURL + "/000000000000/fanout-orders-sub-a"
	rcv, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            awsapi.String(queueURL),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     5,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if len(rcv.Messages) != 1 {
		t.Fatalf("Receive count = %d, want 1", len(rcv.Messages))
	}
	body := awsapi.ToString(rcv.Messages[0].Body)
	if body != "hello-fanout" {
		t.Errorf("Body = %q, want hello-fanout", body)
	}

	// Ack.
	if _, err := sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      awsapi.String(queueURL),
		ReceiptHandle: rcv.Messages[0].ReceiptHandle,
	}); err != nil {
		t.Errorf("DeleteMessage: %v", err)
	}

	// Tear down. Unsubscribe → DeleteTopic.
	if _, err := snsClient.Unsubscribe(ctx, &sns.UnsubscribeInput{
		SubscriptionArn: subOut.SubscriptionArn,
	}); err != nil {
		t.Errorf("Unsubscribe: %v", err)
	}
	if _, err := snsClient.DeleteTopic(ctx, &sns.DeleteTopicInput{
		TopicArn: topicOut.TopicArn,
	}); err != nil {
		t.Errorf("DeleteTopic: %v", err)
	}
}
