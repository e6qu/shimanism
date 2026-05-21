// Matrix conformance for the Queue service: every (frontend × backend)
// cell is driven by the matching cloud's official client surface
// through a shared lifecycle of CreateQueue → SendMessage → Receive →
// ChangeVisibility → DeleteMessage → DeleteQueue.
//
// Frontends:
//   - AWS SQS               (aws-sdk-go-v2/service/sqs)
//   - GCP Pub/Sub           (google.golang.org/api/pubsub/v1)
//   - Azure Service Bus REST (raw HTTP — SDK uses AMQP, deferred per
//     PLAN.md open question)
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

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"google.golang.org/api/option"
	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/queue/conformance"
)

func TestQueueMatrix_AWSFrontend(t *testing.T) {
	ctx := context.Background()
	for _, f := range conformance.ActiveBackends() {
		t.Run(f.Name, func(t *testing.T) {
			be := f.Fn(t)
			srv := harness.StartQueueServerAWS(t, be)
			cfg, err := awsconfig.LoadDefaultConfig(ctx,
				awsconfig.WithRegion("us-east-1"),
				// Verifier's trusted test credentials so requests are
				// signed with a key the shim's SigV4 middleware accepts.
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
			client := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) {
				o.BaseEndpoint = awsapi.String(srv.URL)
			})

			name := fmt.Sprintf("matrix-aws-%s", f.Name)
			cq, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
				QueueName: awsapi.String(name),
			})
			if err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}
			t.Cleanup(func() {
				_, _ = client.DeleteQueue(ctx, &awssqs.DeleteQueueInput{QueueUrl: cq.QueueUrl})
			})

			if _, err := client.SendMessage(ctx, &awssqs.SendMessageInput{
				QueueUrl:    cq.QueueUrl,
				MessageBody: awsapi.String("matrix-payload"),
			}); err != nil {
				t.Fatalf("SendMessage: %v", err)
			}

			rcv, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
				QueueUrl:            cq.QueueUrl,
				MaxNumberOfMessages: 1,
				WaitTimeSeconds:     5,
			})
			if err != nil {
				t.Fatalf("ReceiveMessage: %v", err)
			}
			if len(rcv.Messages) != 1 {
				t.Fatalf("Receive count = %d, want 1", len(rcv.Messages))
			}
			if got := awsapi.ToString(rcv.Messages[0].Body); got != "matrix-payload" {
				t.Errorf("Body = %q, want matrix-payload", got)
			}

			if _, err := client.ChangeMessageVisibility(ctx, &awssqs.ChangeMessageVisibilityInput{
				QueueUrl:          cq.QueueUrl,
				ReceiptHandle:     rcv.Messages[0].ReceiptHandle,
				VisibilityTimeout: 60,
			}); err != nil {
				t.Errorf("ChangeMessageVisibility: %v", err)
			}

			if _, err := client.DeleteMessage(ctx, &awssqs.DeleteMessageInput{
				QueueUrl:      cq.QueueUrl,
				ReceiptHandle: rcv.Messages[0].ReceiptHandle,
			}); err != nil {
				t.Errorf("DeleteMessage: %v", err)
			}

			if _, err := client.DeleteQueue(ctx, &awssqs.DeleteQueueInput{
				QueueUrl: cq.QueueUrl,
			}); err != nil {
				t.Errorf("DeleteQueue: %v", err)
			}
		})
	}
}

func TestQueueMatrix_GCPFrontend(t *testing.T) {
	ctx := context.Background()
	for _, f := range conformance.ActiveBackends() {
		t.Run(f.Name, func(t *testing.T) {
			be := f.Fn(t)
			srv := harness.StartQueueServerGCP(t, be)
			svc, err := pubsubraw.NewService(ctx,
				option.WithEndpoint(srv.URL),
				option.WithoutAuthentication(),
			)
			if err != nil {
				t.Fatalf("new pubsub service: %v", err)
			}

			parent := "projects/shim-matrix"
			short := fmt.Sprintf("matrix-gcp-%s", f.Name)
			topicPath := parent + "/topics/" + short
			subPath := parent + "/subscriptions/" + short

			if _, err := svc.Projects.Topics.Create(topicPath, &pubsubraw.Topic{}).Context(ctx).Do(); err != nil {
				t.Fatalf("Topics.Create: %v", err)
			}
			t.Cleanup(func() {
				_, _ = svc.Projects.Topics.Delete(topicPath).Context(ctx).Do()
			})
			if _, err := svc.Projects.Subscriptions.Create(subPath, &pubsubraw.Subscription{
				Topic:              topicPath,
				AckDeadlineSeconds: 30,
			}).Context(ctx).Do(); err != nil {
				t.Fatalf("Subscriptions.Create: %v", err)
			}

			if _, err := svc.Projects.Topics.Publish(topicPath, &pubsubraw.PublishRequest{
				Messages: []*pubsubraw.PubsubMessage{{
					Data: base64.StdEncoding.EncodeToString([]byte("matrix-payload")),
				}},
			}).Context(ctx).Do(); err != nil {
				t.Fatalf("Publish: %v", err)
			}

			pull, err := svc.Projects.Subscriptions.Pull(subPath, &pubsubraw.PullRequest{
				MaxMessages:       1,
				ReturnImmediately: true,
			}).Context(ctx).Do()
			if err != nil {
				t.Fatalf("Pull: %v", err)
			}
			if len(pull.ReceivedMessages) != 1 {
				t.Fatalf("Pull count = %d, want 1", len(pull.ReceivedMessages))
			}
			rm := pull.ReceivedMessages[0]
			data, _ := base64.StdEncoding.DecodeString(rm.Message.Data)
			if string(data) != "matrix-payload" {
				t.Errorf("data = %q, want matrix-payload", data)
			}

			if _, err := svc.Projects.Subscriptions.ModifyAckDeadline(subPath, &pubsubraw.ModifyAckDeadlineRequest{
				AckIds:             []string{rm.AckId},
				AckDeadlineSeconds: 60,
			}).Context(ctx).Do(); err != nil {
				t.Errorf("ModifyAckDeadline: %v", err)
			}

			if _, err := svc.Projects.Subscriptions.Acknowledge(subPath, &pubsubraw.AcknowledgeRequest{
				AckIds: []string{rm.AckId},
			}).Context(ctx).Do(); err != nil {
				t.Errorf("Acknowledge: %v", err)
			}

			if _, err := svc.Projects.Subscriptions.Delete(subPath).Context(ctx).Do(); err != nil {
				t.Errorf("Subscriptions.Delete: %v", err)
			}
			if _, err := svc.Projects.Topics.Delete(topicPath).Context(ctx).Do(); err != nil {
				t.Errorf("Topics.Delete: %v", err)
			}
		})
	}
}

func TestQueueMatrix_AzureFrontend(t *testing.T) {
	// Azure Service Bus REST frontend, exercised via raw HTTP — the
	// official azservicebus SDK speaks AMQP and is deferred per the
	// PLAN.md open question. AMQP fidelity tier is a future phase.
	for _, f := range conformance.ActiveBackends() {
		t.Run(f.Name, func(t *testing.T) {
			be := f.Fn(t)
			srv := harness.StartQueueServerAzure(t, be)
			name := fmt.Sprintf("matrix-azure-%s", f.Name)
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

			resp, err := req(http.MethodPut, "/"+name, nil, http.StatusCreated)
			if err != nil {
				t.Fatalf("PUT queue: %v", err)
			}
			resp.Body.Close()
			t.Cleanup(func() {
				if r, _ := req(http.MethodDelete, "/"+name, nil, http.StatusNoContent, http.StatusNotFound); r != nil {
					r.Body.Close()
				}
			})

			resp, err = req(http.MethodPost, "/"+name+"/messages", []byte("matrix-payload"), http.StatusCreated)
			if err != nil {
				t.Fatalf("POST send: %v", err)
			}
			resp.Body.Close()

			resp, err = req(http.MethodPost, "/"+name+"/messages/head", nil, http.StatusCreated)
			if err != nil {
				t.Fatalf("POST peek-lock: %v", err)
			}
			var bp struct {
				MessageId string `json:"MessageId"`
				LockToken string `json:"LockToken"`
			}
			_ = json.Unmarshal([]byte(resp.Header.Get("BrokerProperties")), &bp)
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "matrix-payload" {
				t.Errorf("body = %q, want matrix-payload", body)
			}

			r2, err := req(http.MethodPost,
				"/"+name+"/messages/"+bp.MessageId+"/"+bp.LockToken,
				nil, http.StatusOK)
			if err != nil {
				t.Errorf("renew lock: %v", err)
			} else {
				r2.Body.Close()
			}

			r3, err := req(http.MethodDelete,
				"/"+name+"/messages/"+bp.MessageId+"/"+bp.LockToken,
				nil, http.StatusOK)
			if err != nil {
				t.Errorf("complete: %v", err)
			} else {
				r3.Body.Close()
			}

			r4, err := req(http.MethodDelete, "/"+name, nil, http.StatusNoContent)
			if err != nil {
				t.Errorf("DELETE queue: %v", err)
			} else {
				r4.Body.Close()
			}
		})
	}
}
