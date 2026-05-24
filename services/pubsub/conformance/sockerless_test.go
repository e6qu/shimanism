// Sockerless lane for the pubsub service. See doc/SOCKERLESS_VALIDATION.md.
package conformance_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"google.golang.org/api/option"
	pubsubraw "google.golang.org/api/pubsub/v1"

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

func randomHex8() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
