// Phase 4 conformance: GCP Pub/Sub-shaped frontend exercised by
// the official google.golang.org/api/pubsub/v1 SDK against the
// in-mem backend.
package conformance_test

import (
	"context"
	"encoding/base64"
	"testing"

	"google.golang.org/api/option"
	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/pubsub/backends/inmem"
)

func TestGCPSDK_PubsubFanout(t *testing.T) {
	srv := harness.StartPubsubServerGCP(t, inmem.New())
	svc, err := pubsubraw.NewService(context.Background(),
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new pubsub service: %v", err)
	}
	ctx := context.Background()
	const parent = "projects/shim-conformance"

	if _, err := svc.Projects.Topics.Create(parent+"/topics/orders", &pubsubraw.Topic{}).Context(ctx).Do(); err != nil {
		t.Fatalf("Topics.Create: %v", err)
	}

	// Two subscriptions — verify each gets its own copy.
	for _, sub := range []string{"orders-a", "orders-b"} {
		if _, err := svc.Projects.Subscriptions.Create(parent+"/subscriptions/"+sub, &pubsubraw.Subscription{
			Topic:              parent + "/topics/orders",
			AckDeadlineSeconds: 30,
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("Subscriptions.Create %s: %v", sub, err)
		}
	}

	// Publish.
	if _, err := svc.Projects.Topics.Publish(parent+"/topics/orders", &pubsubraw.PublishRequest{
		Messages: []*pubsubraw.PubsubMessage{{
			Data: base64.StdEncoding.EncodeToString([]byte("hello-fanout")),
		}},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Each subscription should receive a copy.
	for _, sub := range []string{"orders-a", "orders-b"} {
		pull, err := svc.Projects.Subscriptions.Pull(parent+"/subscriptions/"+sub, &pubsubraw.PullRequest{
			MaxMessages:       1,
			ReturnImmediately: true,
		}).Context(ctx).Do()
		if err != nil {
			t.Fatalf("Pull %s: %v", sub, err)
		}
		if len(pull.ReceivedMessages) != 1 {
			t.Fatalf("Pull %s count = %d, want 1", sub, len(pull.ReceivedMessages))
		}
		data, _ := base64.StdEncoding.DecodeString(pull.ReceivedMessages[0].Message.Data)
		if string(data) != "hello-fanout" {
			t.Errorf("%s data = %q, want hello-fanout", sub, data)
		}
		if _, err := svc.Projects.Subscriptions.Acknowledge(parent+"/subscriptions/"+sub, &pubsubraw.AcknowledgeRequest{
			AckIds: []string{pull.ReceivedMessages[0].AckId},
		}).Context(ctx).Do(); err != nil {
			t.Errorf("Ack %s: %v", sub, err)
		}
	}

	// Tear down.
	for _, sub := range []string{"orders-a", "orders-b"} {
		if _, err := svc.Projects.Subscriptions.Delete(parent + "/subscriptions/" + sub).Context(ctx).Do(); err != nil {
			t.Errorf("Delete sub %s: %v", sub, err)
		}
	}
	if _, err := svc.Projects.Topics.Delete(parent + "/topics/orders").Context(ctx).Do(); err != nil {
		t.Errorf("Delete topic: %v", err)
	}
}
