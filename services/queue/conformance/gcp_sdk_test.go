// Phase 3 conformance: GCP Pub/Sub-shaped frontend exercised by
// the official `google.golang.org/api/pubsub/v1` REST SDK against
// the in-mem backend.
package conformance_test

import (
	"context"
	"encoding/base64"
	"testing"

	pubsubraw "google.golang.org/api/pubsub/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/queue/backends/inmem"
)

func TestGCPSDK_QueueLifecycle(t *testing.T) {
	srv := harness.StartQueueServerGCP(t, inmem.New())
	svc, err := pubsubraw.NewService(context.Background(),
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new pubsub service: %v", err)
	}
	ctx := context.Background()
	const parent = "projects/shim-conformance"

	// Create topic.
	if _, err := svc.Projects.Topics.Create(parent+"/topics/orders", &pubsubraw.Topic{}).Context(ctx).Do(); err != nil {
		t.Fatalf("Topics.Create: %v", err)
	}

	// Create subscription (same name — maps to the same domain queue).
	if _, err := svc.Projects.Subscriptions.Create(parent+"/subscriptions/orders", &pubsubraw.Subscription{
		Topic:              parent + "/topics/orders",
		AckDeadlineSeconds: 30,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Subscriptions.Create: %v", err)
	}

	// Publish a message.
	pub, err := svc.Projects.Topics.Publish(parent+"/topics/orders", &pubsubraw.PublishRequest{
		Messages: []*pubsubraw.PubsubMessage{{
			Data:       base64.StdEncoding.EncodeToString([]byte("hello-shim")),
			Attributes: map[string]string{"env": "test"},
		}},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(pub.MessageIds) != 1 {
		t.Fatalf("Publish returned %d ids, want 1", len(pub.MessageIds))
	}

	// Pull.
	pull, err := svc.Projects.Subscriptions.Pull(parent+"/subscriptions/orders", &pubsubraw.PullRequest{
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
	if string(data) != "hello-shim" {
		t.Errorf("data = %q, want hello-shim", data)
	}
	if rm.Message.Attributes["env"] != "test" {
		t.Errorf("attr env = %q, want test", rm.Message.Attributes["env"])
	}

	// ModifyAckDeadline.
	if _, err := svc.Projects.Subscriptions.ModifyAckDeadline(parent+"/subscriptions/orders", &pubsubraw.ModifyAckDeadlineRequest{
		AckIds:             []string{rm.AckId},
		AckDeadlineSeconds: 60,
	}).Context(ctx).Do(); err != nil {
		t.Errorf("ModifyAckDeadline: %v", err)
	}

	// Acknowledge.
	if _, err := svc.Projects.Subscriptions.Acknowledge(parent+"/subscriptions/orders", &pubsubraw.AcknowledgeRequest{
		AckIds: []string{rm.AckId},
	}).Context(ctx).Do(); err != nil {
		t.Errorf("Acknowledge: %v", err)
	}

	// Delete subscription + topic.
	if _, err := svc.Projects.Subscriptions.Delete(parent + "/subscriptions/orders").Context(ctx).Do(); err != nil {
		t.Errorf("Subscriptions.Delete: %v", err)
	}
	if _, err := svc.Projects.Topics.Delete(parent + "/topics/orders").Context(ctx).Do(); err != nil {
		t.Errorf("Topics.Delete: %v", err)
	}
}
