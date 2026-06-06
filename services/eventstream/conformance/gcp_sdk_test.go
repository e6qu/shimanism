// Conformance: GCP Managed Service for Apache Kafka topic control plane
// exercised by the official google.golang.org/api/managedkafka/v1 REST SDK.
package conformance_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
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

func TestGCPSDK_ManagedKafkaTopicBacksKafkaClientProduceFetch(t *testing.T) {
	backend := inmem.New()
	restSrv := harness.StartEventStreamServerGCP(t, backend)
	kafkaSrv := harness.StartEventStreamKafkaServer(t, backend)
	svc := newManagedKafkaService(t, restSrv.URL)
	ctx := context.Background()
	parent := "projects/shim-conformance/locations/us-central1/clusters/cluster-a"
	topic := "client-events"

	if _, err := svc.Projects.Locations.Clusters.Topics.Create(parent, &managedkafkaraw.Topic{
		PartitionCount: 1,
		Configs:        map[string]string{"retention.ms": "60000"},
	}).TopicId(topic).Context(ctx).Do(); err != nil {
		t.Fatalf("topics.create: %v", err)
	}

	client, err := kgo.NewClient(
		kgo.SeedBrokers(kafkaSrv.Address),
		kgo.WithLogger(kgoTestLogger{t: t}),
		kgo.DefaultProduceTopic(topic),
		kgo.RecordPartitioner(kgo.ManualPartitioner()),
		kgo.DisableIdempotentWrite(),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
			topic: {0: kgo.NewOffset().At(0)},
		}),
	)
	if err != nil {
		t.Fatalf("kgo client: %v", err)
	}
	t.Cleanup(client.Close)

	produceCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.ProduceSync(produceCtx, &kgo.Record{
		Partition: 0,
		Key:       []byte("order-1"),
		Value:     []byte("created"),
	}).FirstErr(); err != nil {
		t.Fatalf("kgo produce: %v", err)
	}

	fetchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		fetches := client.PollFetches(fetchCtx)
		if err := fetches.Err0(); err != nil {
			t.Fatalf("kgo fetch: %v", err)
		}
		for _, record := range fetches.Records() {
			if record.Topic != topic || record.Partition != 0 {
				continue
			}
			if string(record.Key) != "order-1" || string(record.Value) != "created" {
				t.Fatalf("fetched record = key %q value %q, want order-1/created", record.Key, record.Value)
			}
			if record.Offset != 0 {
				t.Fatalf("fetched record offset = %d, want 0", record.Offset)
			}
			return
		}
	}
}

type kgoTestLogger struct {
	t *testing.T
}

func (l kgoTestLogger) Level() kgo.LogLevel {
	return kgo.LogLevelDebug
}

func (l kgoTestLogger) Log(level kgo.LogLevel, msg string, keyvals ...any) {
	l.t.Helper()
	l.t.Logf("kgo %s: %s %v", level, msg, keyvals)
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
