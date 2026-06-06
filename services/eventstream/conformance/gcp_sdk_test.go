// Conformance: GCP Managed Service for Apache Kafka topic control plane
// exercised by the official google.golang.org/api/managedkafka/v1 REST SDK.
package conformance_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
	managedkafkaraw "google.golang.org/api/managedkafka/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/eventstream/backends/inmem"
)

func managedKafkaTokenSource() oauth2.TokenSource {
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://managedkafka.googleapis.com/",
		15*time.Minute,
	)
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
}

func newManagedKafkaService(t *testing.T, endpoint string) *managedkafkaraw.Service {
	t.Helper()
	svc, err := managedkafkaraw.NewService(context.Background(),
		option.WithEndpoint(strings.TrimSuffix(endpoint, "/")+"/"),
		option.WithTokenSource(managedKafkaTokenSource()),
	)
	if err != nil {
		t.Fatalf("managedkafka service: %v", err)
	}
	return svc
}

func TestGCPSDK_ManagedKafkaTopicLifecycle(t *testing.T) {
	srv := harness.StartEventStreamServerGCP(t, inmem.New())
	svc := newManagedKafkaService(t, srv.URL)
	ctx := context.Background()
	parent := "projects/shim-conformance/locations/us-central1/clusters/cluster-a"
	topicName := parent + "/topics/orders"

	created, err := svc.Projects.Locations.Clusters.Topics.Create(parent, &managedkafkaraw.Topic{
		PartitionCount: 2,
		Configs:        map[string]string{"retention.ms": "60000"},
	}).TopicId("orders").Context(ctx).Do()
	if err != nil {
		t.Fatalf("topics.create: %v", err)
	}
	if created.Name != topicName {
		t.Errorf("created name = %q, want %q", created.Name, topicName)
	}
	if created.PartitionCount != 2 {
		t.Errorf("created partitionCount = %d, want 2", created.PartitionCount)
	}
	if created.Configs["retention.ms"] != "60000" {
		t.Errorf("created configs = %v, want retention.ms=60000", created.Configs)
	}

	got, err := svc.Projects.Locations.Clusters.Topics.Get(topicName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("topics.get: %v", err)
	}
	if got.Name != topicName {
		t.Errorf("get name = %q, want %q", got.Name, topicName)
	}

	if _, err := svc.Projects.Locations.Clusters.Topics.Create(parent, &managedkafkaraw.Topic{
		PartitionCount: 1,
	}).TopicId("metrics").Context(ctx).Do(); err != nil {
		t.Fatalf("topics.create metrics: %v", err)
	}
	list, err := svc.Projects.Locations.Clusters.Topics.List(parent).PageSize(1).Context(ctx).Do()
	if err != nil {
		t.Fatalf("topics.list page 1: %v", err)
	}
	if len(list.Topics) != 1 || list.NextPageToken == "" {
		t.Fatalf("topics.list page 1 = %+v, want one topic and nextPageToken", list)
	}
	list2, err := svc.Projects.Locations.Clusters.Topics.List(parent).PageToken(list.NextPageToken).Context(ctx).Do()
	if err != nil {
		t.Fatalf("topics.list page 2: %v", err)
	}
	if len(list2.Topics) != 1 || list2.NextPageToken != "" {
		t.Fatalf("topics.list page 2 = %+v, want one final topic", list2)
	}

	if _, err := svc.Projects.Locations.Clusters.Topics.Delete(topicName).Context(ctx).Do(); err != nil {
		t.Fatalf("topics.delete: %v", err)
	}
	if _, err := svc.Projects.Locations.Clusters.Topics.Get(topicName).Context(ctx).Do(); err == nil {
		t.Fatal("topics.get after delete: got nil, want 404")
	} else {
		var gerr *googleapi.Error
		if !errors.As(err, &gerr) || gerr.Code != 404 {
			t.Fatalf("topics.get after delete error = %v, want googleapi 404", err)
		}
	}
}

func TestGCPSDK_ManagedKafkaRejectsOutOfIntersectionReplication(t *testing.T) {
	srv := harness.StartEventStreamServerGCP(t, inmem.New())
	svc := newManagedKafkaService(t, srv.URL)
	ctx := context.Background()
	parent := "projects/shim-conformance/locations/us-central1/clusters/cluster-a"

	_, err := svc.Projects.Locations.Clusters.Topics.Create(parent, &managedkafkaraw.Topic{
		PartitionCount:    1,
		ReplicationFactor: 3,
	}).TopicId("replicated").Context(ctx).Do()
	if err == nil {
		t.Fatal("topics.create with replicationFactor: got nil, want error")
	}
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 400 {
		t.Fatalf("topics.create replicationFactor error = %v, want googleapi 400", err)
	}
}
