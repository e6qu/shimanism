// Phase 3 conformance: AWS SQS-shaped frontend exercised by the
// official `aws-sdk-go-v2/service/sqs` SDK against the in-mem
// backend.
package conformance_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/queue/backends/inmem"
)

func newSQSClient(t *testing.T, endpoint string) *sqs.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		// Verifier's trusted test credentials (matches sigv4verifier.StaticStore
		// wired in internal/queue/frontends/aws_sqs/adapter.go).
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	return sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

func TestAWSSDK_QueueLifecycle(t *testing.T) {
	srv := harness.StartQueueServerAWS(t, inmem.New())
	cli := newSQSClient(t, srv.URL)
	ctx := context.Background()

	// CreateQueue
	cq, err := cli.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String("orders"),
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if aws.ToString(cq.QueueUrl) == "" {
		t.Fatalf("CreateQueue returned empty QueueUrl")
	}

	// GetQueueUrl
	urlResp, err := cli.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{
		QueueName: aws.String("orders"),
	})
	if err != nil {
		t.Fatalf("GetQueueUrl: %v", err)
	}
	if aws.ToString(urlResp.QueueUrl) != aws.ToString(cq.QueueUrl) {
		t.Errorf("GetQueueUrl = %q, want %q", aws.ToString(urlResp.QueueUrl), aws.ToString(cq.QueueUrl))
	}

	// SendMessage
	sm, err := cli.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    cq.QueueUrl,
		MessageBody: aws.String("hello-shim"),
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			"env": {StringValue: aws.String("test"), DataType: aws.String("String")},
		},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if aws.ToString(sm.MessageId) == "" {
		t.Errorf("SendMessage returned empty MessageId")
	}

	// ReceiveMessage
	rm, err := cli.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:              cq.QueueUrl,
		MaxNumberOfMessages:   1,
		MessageAttributeNames: []string{"All"},
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if len(rm.Messages) != 1 {
		t.Fatalf("ReceiveMessage count = %d, want 1", len(rm.Messages))
	}
	got := rm.Messages[0]
	if aws.ToString(got.Body) != "hello-shim" {
		t.Errorf("Body = %q, want hello-shim", aws.ToString(got.Body))
	}
	if got.MessageAttributes["env"].StringValue == nil || *got.MessageAttributes["env"].StringValue != "test" {
		t.Errorf("message attribute env = %v, want test", got.MessageAttributes["env"])
	}

	// ChangeMessageVisibility
	if _, err := cli.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          cq.QueueUrl,
		ReceiptHandle:     got.ReceiptHandle,
		VisibilityTimeout: 60,
	}); err != nil {
		t.Errorf("ChangeMessageVisibility: %v", err)
	}

	// DeleteMessage
	if _, err := cli.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      cq.QueueUrl,
		ReceiptHandle: got.ReceiptHandle,
	}); err != nil {
		t.Errorf("DeleteMessage: %v", err)
	}

	// GetQueueAttributes
	attrs, err := cli.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       cq.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{"All"},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}
	if attrs.Attributes["VisibilityTimeout"] == "" {
		t.Errorf("GetQueueAttributes missing VisibilityTimeout")
	}

	// ListQueues
	lq, err := cli.ListQueues(ctx, &sqs.ListQueuesInput{})
	if err != nil {
		t.Fatalf("ListQueues: %v", err)
	}
	if len(lq.QueueUrls) != 1 {
		t.Errorf("ListQueues count = %d, want 1", len(lq.QueueUrls))
	}

	// DeleteQueue
	if _, err := cli.DeleteQueue(ctx, &sqs.DeleteQueueInput{
		QueueUrl: cq.QueueUrl,
	}); err != nil {
		t.Errorf("DeleteQueue: %v", err)
	}

	// ReceiveMessage on deleted queue should return QueueDoesNotExist.
	_, err = cli.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl: cq.QueueUrl,
	})
	if err == nil {
		t.Fatalf("expected QueueDoesNotExist after delete, got nil")
	}
	var notExist *sqstypes.QueueDoesNotExist
	if !errors.As(err, &notExist) {
		t.Errorf("error after delete = %v, want *QueueDoesNotExist", err)
	}
}
