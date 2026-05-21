// Matrix conformance for the Pubsub service: every
// (frontend × backend) cell is driven by the matching cloud's
// official client surface through a shared fanout lifecycle of
// CreateTopic → CreateSubscription × 2 → Publish → Receive on each
// sub → Ack → DeleteSubscription × 2 → DeleteTopic.
//
// Frontends:
//   - AWS SNS+SQS  (aws-sdk-go-v2/service/sns + /sqs)
//   - GCP Pub/Sub  (google.golang.org/api/pubsub/v1)
//   - Azure Service Bus topics REST (raw HTTP)
//
// Backends iterate `conformance.ActiveBackends()`; each factory
// internally `t.Skip`s when its infrastructure isn't available.
package conformance_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/pubsub/conformance"
)

func TestPubsubMatrix_AWSFrontend(t *testing.T) {
	ctx := context.Background()
	for _, f := range conformance.ActiveBackends() {
		t.Run(f.Name, func(t *testing.T) {
			be := f.Fn(t)
			srv := harness.StartPubsubServerAWS(t, be)
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
			snsClient := sns.NewFromConfig(cfg, func(o *sns.Options) {
				o.BaseEndpoint = awsapi.String(srv.SnsURL)
			})
			sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
				o.BaseEndpoint = awsapi.String(srv.SqsURL)
			})

			topicName := fmt.Sprintf("matrix-aws-%s", f.Name)
			topicOut, err := snsClient.CreateTopic(ctx, &sns.CreateTopicInput{Name: awsapi.String(topicName)})
			if err != nil {
				t.Fatalf("CreateTopic: %v", err)
			}
			t.Cleanup(func() {
				_, _ = snsClient.DeleteTopic(ctx, &sns.DeleteTopicInput{TopicArn: topicOut.TopicArn})
			})

			subs := []string{topicName + "-a", topicName + "-b"}
			for _, s := range subs {
				queueArn := "arn:aws:sqs:us-east-1:000000000000:" + s
				if _, err := snsClient.Subscribe(ctx, &sns.SubscribeInput{
					TopicArn: topicOut.TopicArn,
					Protocol: awsapi.String("sqs"),
					Endpoint: awsapi.String(queueArn),
				}); err != nil {
					t.Fatalf("Subscribe %s: %v", s, err)
				}
			}

			if _, err := snsClient.Publish(ctx, &sns.PublishInput{
				TopicArn: topicOut.TopicArn,
				Message:  awsapi.String("matrix-fanout"),
			}); err != nil {
				t.Fatalf("Publish: %v", err)
			}

			for _, s := range subs {
				queueURL := srv.SqsURL + "/000000000000/" + s
				rcv, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
					QueueUrl:            awsapi.String(queueURL),
					MaxNumberOfMessages: 1,
					WaitTimeSeconds:     5,
				})
				if err != nil {
					t.Fatalf("Receive %s: %v", s, err)
				}
				if len(rcv.Messages) != 1 {
					t.Fatalf("Receive %s count = %d, want 1", s, len(rcv.Messages))
				}
				if _, err := sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
					QueueUrl:      awsapi.String(queueURL),
					ReceiptHandle: rcv.Messages[0].ReceiptHandle,
				}); err != nil {
					t.Errorf("DeleteMessage %s: %v", s, err)
				}
				if _, err := sqsClient.DeleteQueue(ctx, &sqs.DeleteQueueInput{
					QueueUrl: awsapi.String(queueURL),
				}); err != nil {
					t.Errorf("DeleteQueue %s: %v", s, err)
				}
			}
		})
	}
}

func TestPubsubMatrix_GCPFrontend(t *testing.T) {
	ctx := context.Background()
	for _, f := range conformance.ActiveBackends() {
		t.Run(f.Name, func(t *testing.T) {
			be := f.Fn(t)
			srv := harness.StartPubsubServerGCP(t, be)
			jwt := gcpbearer.TestJWT(
				[]byte("test-key-do-not-use-in-prod"),
				"https://shim.test/",
				"https://pubsub.googleapis.com/",
				15*time.Minute,
			)
			svc, err := pubsubraw.NewService(ctx,
				option.WithEndpoint(srv.URL),
				option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})),
			)
			if err != nil {
				t.Fatalf("new pubsub service: %v", err)
			}
			parent := "projects/shim-matrix"
			short := fmt.Sprintf("matrix-gcp-%s", f.Name)
			topicPath := parent + "/topics/" + short
			subs := []string{short + "-a", short + "-b"}

			if _, err := svc.Projects.Topics.Create(topicPath, &pubsubraw.Topic{}).Context(ctx).Do(); err != nil {
				t.Fatalf("Topics.Create: %v", err)
			}
			t.Cleanup(func() {
				_, _ = svc.Projects.Topics.Delete(topicPath).Context(ctx).Do()
			})
			for _, s := range subs {
				if _, err := svc.Projects.Subscriptions.Create(parent+"/subscriptions/"+s, &pubsubraw.Subscription{
					Topic:              topicPath,
					AckDeadlineSeconds: 30,
				}).Context(ctx).Do(); err != nil {
					t.Fatalf("Subscriptions.Create %s: %v", s, err)
				}
			}

			if _, err := svc.Projects.Topics.Publish(topicPath, &pubsubraw.PublishRequest{
				Messages: []*pubsubraw.PubsubMessage{{
					Data: base64.StdEncoding.EncodeToString([]byte("matrix-fanout")),
				}},
			}).Context(ctx).Do(); err != nil {
				t.Fatalf("Publish: %v", err)
			}

			for _, s := range subs {
				pull, err := svc.Projects.Subscriptions.Pull(parent+"/subscriptions/"+s, &pubsubraw.PullRequest{
					MaxMessages:       1,
					ReturnImmediately: true,
				}).Context(ctx).Do()
				if err != nil {
					t.Fatalf("Pull %s: %v", s, err)
				}
				if len(pull.ReceivedMessages) != 1 {
					t.Fatalf("Pull %s count = %d, want 1", s, len(pull.ReceivedMessages))
				}
				if _, err := svc.Projects.Subscriptions.Acknowledge(parent+"/subscriptions/"+s, &pubsubraw.AcknowledgeRequest{
					AckIds: []string{pull.ReceivedMessages[0].AckId},
				}).Context(ctx).Do(); err != nil {
					t.Errorf("Ack %s: %v", s, err)
				}
				if _, err := svc.Projects.Subscriptions.Delete(parent + "/subscriptions/" + s).Context(ctx).Do(); err != nil {
					t.Errorf("Delete sub %s: %v", s, err)
				}
			}
		})
	}
}

func TestPubsubMatrix_AzureFrontend(t *testing.T) {
	for _, f := range conformance.ActiveBackends() {
		t.Run(f.Name, func(t *testing.T) {
			be := f.Fn(t)
			srv := harness.StartPubsubServerAzure(t, be)
			topic := fmt.Sprintf("matrix-azure-%s", f.Name)
			subs := []string{topic + "-a", topic + "-b"}

			req := func(method, path string, body []byte, expect ...int) (*http.Response, error) {
				r, err := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
				if err != nil {
					return nil, err
				}
				resp, err := http.DefaultClient.Do(r)
				if err != nil {
					return nil, err
				}
				for _, e := range expect {
					if resp.StatusCode == e {
						return resp, nil
					}
				}
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				return nil, fmt.Errorf("%s %s -> %d (want %v) body=%s", method, path, resp.StatusCode, expect, b)
			}

			resp, err := req(http.MethodPut, "/"+topic, nil, http.StatusCreated)
			if err != nil {
				t.Fatalf("PUT topic: %v", err)
			}
			resp.Body.Close()
			t.Cleanup(func() {
				if r, _ := req(http.MethodDelete, "/"+topic, nil, http.StatusNoContent, http.StatusNotFound); r != nil {
					r.Body.Close()
				}
			})
			for _, s := range subs {
				if r, err := req(http.MethodPut, "/"+topic+"/Subscriptions/"+s, nil, http.StatusCreated); err != nil {
					t.Fatalf("PUT sub %s: %v", s, err)
				} else {
					r.Body.Close()
				}
			}

			resp, err = req(http.MethodPost, "/"+topic+"/messages", []byte("matrix-fanout"), http.StatusCreated)
			if err != nil {
				t.Fatalf("Publish: %v", err)
			}
			resp.Body.Close()

			for _, s := range subs {
				r, err := req(http.MethodPost, "/"+topic+"/Subscriptions/"+s+"/messages/head", nil, http.StatusCreated)
				if err != nil {
					t.Fatalf("peek %s: %v", s, err)
				}
				var bp struct {
					MessageId string `json:"MessageId"`
					LockToken string `json:"LockToken"`
				}
				_ = json.Unmarshal([]byte(r.Header.Get("BrokerProperties")), &bp)
				body, _ := io.ReadAll(r.Body)
				r.Body.Close()
				if string(body) != "matrix-fanout" {
					t.Errorf("%s body = %q, want matrix-fanout", s, body)
				}
				if r2, err := req(http.MethodDelete,
					"/"+topic+"/Subscriptions/"+s+"/messages/"+bp.MessageId+"/"+bp.LockToken,
					nil, http.StatusOK); err != nil {
					t.Errorf("ack %s: %v", s, err)
				} else {
					r2.Body.Close()
				}
				if r3, err := req(http.MethodDelete, "/"+topic+"/Subscriptions/"+s, nil, http.StatusNoContent); err != nil {
					t.Errorf("DELETE sub %s: %v", s, err)
				} else {
					r3.Body.Close()
				}
			}
		})
	}
}
