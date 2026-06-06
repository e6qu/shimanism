package inmem

import (
	"context"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/eventstream/domain"
)

const testCluster = "cluster-a"

func TestTopicLifecycleAndList(t *testing.T) {
	ctx := context.Background()
	b := New()
	createdAt := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return createdAt }

	topic, err := b.CreateTopic(ctx, testCluster, "events.alpha", domain.CreateTopicOptions{
		PartitionCount: 2,
		Retention:      time.Hour,
		Tags:           map[string]string{"owner": "qa"},
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	if topic.ClusterID != testCluster || topic.Name != "events.alpha" || topic.PartitionCount != 2 || topic.Retention != time.Hour || !topic.CreatedAt.Equal(createdAt) {
		t.Fatalf("unexpected topic: %#v", topic)
	}

	if _, err := b.CreateTopic(ctx, testCluster, "events.alpha", domain.CreateTopicOptions{PartitionCount: 1}); !domain.IsAlreadyExists(err) {
		t.Fatalf("CreateTopic duplicate error = %v, want already exists", err)
	}
	otherClusterTopic, err := b.CreateTopic(ctx, "cluster-b", "events.alpha", domain.CreateTopicOptions{PartitionCount: 1})
	if err != nil {
		t.Fatalf("CreateTopic same name in other cluster: %v", err)
	}
	if otherClusterTopic.ClusterID != "cluster-b" {
		t.Fatalf("same-name other-cluster topic = %#v, want cluster-b", otherClusterTopic)
	}
	if _, err := b.CreateTopic(ctx, testCluster, "events.beta", domain.CreateTopicOptions{PartitionCount: 1}); err != nil {
		t.Fatalf("CreateTopic events.beta: %v", err)
	}
	if _, err := b.CreateTopic(ctx, testCluster, "metrics", domain.CreateTopicOptions{PartitionCount: 1}); err != nil {
		t.Fatalf("CreateTopic metrics: %v", err)
	}

	describe, err := b.DescribeTopic(ctx, testCluster, "events.alpha")
	if err != nil {
		t.Fatalf("DescribeTopic: %v", err)
	}
	describe.Tags["owner"] = "mutated"
	describeAgain, err := b.DescribeTopic(ctx, testCluster, "events.alpha")
	if err != nil {
		t.Fatalf("DescribeTopic again: %v", err)
	}
	if got := describeAgain.Tags["owner"]; got != "qa" {
		t.Fatalf("DescribeTopic returned mutable tags, got owner %q", got)
	}

	page1, err := b.ListTopics(ctx, domain.ListTopicsOptions{ClusterID: testCluster, Prefix: "events.", MaxResults: 1})
	if err != nil {
		t.Fatalf("ListTopics page1: %v", err)
	}
	if len(page1.Topics) != 1 || page1.Topics[0].Name != "events.alpha" || page1.NextToken == "" {
		t.Fatalf("unexpected page1: %#v", page1)
	}
	page2, err := b.ListTopics(ctx, domain.ListTopicsOptions{ClusterID: testCluster, Prefix: "events.", NextToken: page1.NextToken})
	if err != nil {
		t.Fatalf("ListTopics page2: %v", err)
	}
	if len(page2.Topics) != 1 || page2.Topics[0].Name != "events.beta" || page2.NextToken != "" {
		t.Fatalf("unexpected page2: %#v", page2)
	}

	if err := b.DeleteTopic(ctx, testCluster, "events.alpha"); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	if _, err := b.DescribeTopic(ctx, testCluster, "events.alpha"); !domain.IsNotFound(err) {
		t.Fatalf("DescribeTopic deleted error = %v, want not found", err)
	}
}

func TestProduceFetchAndOffsets(t *testing.T) {
	ctx := context.Background()
	b := New()
	now := time.Date(2026, 6, 6, 11, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return now }
	if _, err := b.CreateTopic(ctx, testCluster, "events", domain.CreateTopicOptions{PartitionCount: 2}); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	input := []domain.ProducerRecord{
		{Key: []byte("k0"), Value: []byte("v0"), Headers: []domain.Header{{Key: "trace", Value: []byte("a")}}},
		{Key: []byte("k1"), Value: []byte("v1"), Timestamp: now.Add(-time.Second)},
		{Key: []byte("k2"), Value: []byte("v2")},
	}
	meta, err := b.Produce(ctx, testCluster, "events", 1, input)
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	for i, got := range meta {
		if got.Topic != "events" || got.Partition != 1 || got.Offset != int64(i) {
			t.Fatalf("metadata[%d] = %#v", i, got)
		}
	}
	input[0].Key[0] = 'x'
	input[0].Headers[0].Value[0] = 'x'

	records, err := b.Fetch(ctx, testCluster, "events", 1, 1, 2)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(records) != 2 || records[0].Offset != 1 || string(records[0].Value) != "v1" || records[1].Offset != 2 {
		t.Fatalf("unexpected records: %#v", records)
	}

	all, err := b.Fetch(ctx, testCluster, "events", 1, 0, 0)
	if err != nil {
		t.Fatalf("Fetch all: %v", err)
	}
	if string(all[0].Key) != "k0" || string(all[0].Headers[0].Value) != "a" {
		t.Fatalf("stored record changed after producer mutation: %#v", all[0])
	}
	all[0].Value[0] = 'x'
	allAgain, err := b.Fetch(ctx, testCluster, "events", 1, 0, 0)
	if err != nil {
		t.Fatalf("Fetch all again: %v", err)
	}
	if string(allAgain[0].Value) != "v0" {
		t.Fatalf("Fetch returned mutable stored value: %#v", allAgain[0])
	}

	bounds, err := b.ListOffsets(ctx, testCluster, "events", 1)
	if err != nil {
		t.Fatalf("ListOffsets: %v", err)
	}
	if bounds.Earliest != 0 || bounds.Latest != 3 {
		t.Fatalf("offset bounds = %#v, want earliest 0 latest 3", bounds)
	}
}

func TestConsumerOffsets(t *testing.T) {
	ctx := context.Background()
	b := New()
	if _, err := b.CreateTopic(ctx, testCluster, "events", domain.CreateTopicOptions{PartitionCount: 1}); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	if _, err := b.Produce(ctx, testCluster, "events", 0, []domain.ProducerRecord{{Value: []byte("one")}}); err != nil {
		t.Fatalf("Produce: %v", err)
	}

	if _, err := b.FetchCommittedOffset(ctx, testCluster, "workers", "events", 0); !domain.IsInvalidInput(err) {
		t.Fatalf("FetchCommittedOffset missing group error = %v, want invalid input", err)
	}
	if err := b.CommitOffset(ctx, testCluster, "workers", "events", 0, 2); !domain.IsInvalidInput(err) {
		t.Fatalf("CommitOffset beyond latest error = %v, want invalid input", err)
	}
	if err := b.CommitOffset(ctx, testCluster, "workers", "events", 0, 1); err != nil {
		t.Fatalf("CommitOffset: %v", err)
	}
	offset, err := b.FetchCommittedOffset(ctx, testCluster, "workers", "events", 0)
	if err != nil {
		t.Fatalf("FetchCommittedOffset: %v", err)
	}
	if offset != 1 {
		t.Fatalf("committed offset = %d, want 1", offset)
	}

	if err := b.DeleteTopic(ctx, testCluster, "events"); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	if _, err := b.FetchCommittedOffset(ctx, testCluster, "workers", "events", 0); !domain.IsNotFound(err) {
		t.Fatalf("FetchCommittedOffset after delete error = %v, want not found", err)
	}
}

func TestRetentionPrunesOldRecords(t *testing.T) {
	ctx := context.Background()
	b := New()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return now }
	if _, err := b.CreateTopic(ctx, testCluster, "events", domain.CreateTopicOptions{PartitionCount: 1, Retention: 5 * time.Second}); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	meta, err := b.Produce(ctx, testCluster, "events", 0, []domain.ProducerRecord{
		{Value: []byte("old"), Timestamp: now.Add(-10 * time.Second)},
		{Value: []byte("current"), Timestamp: now},
	})
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	if meta[0].Offset != 0 || meta[1].Offset != 1 {
		t.Fatalf("metadata = %#v, want offsets 0 and 1", meta)
	}

	if _, err := b.Fetch(ctx, testCluster, "events", 0, 0, 1); !domain.IsInvalidInput(err) {
		t.Fatalf("Fetch before earliest error = %v, want invalid input", err)
	}
	records, err := b.Fetch(ctx, testCluster, "events", 0, 1, 0)
	if err != nil {
		t.Fatalf("Fetch after prune: %v", err)
	}
	if len(records) != 1 || records[0].Offset != 1 || string(records[0].Value) != "current" {
		t.Fatalf("records after prune = %#v", records)
	}
	bounds, err := b.ListOffsets(ctx, testCluster, "events", 0)
	if err != nil {
		t.Fatalf("ListOffsets: %v", err)
	}
	if bounds.Earliest != 1 || bounds.Latest != 2 {
		t.Fatalf("bounds = %#v, want earliest 1 latest 2", bounds)
	}
	meta, err = b.Produce(ctx, testCluster, "events", 0, []domain.ProducerRecord{{Value: []byte("newer")}})
	if err != nil {
		t.Fatalf("Produce after prune: %v", err)
	}
	if meta[0].Offset != 2 {
		t.Fatalf("offset after prune = %d, want 2", meta[0].Offset)
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	b := New()
	if _, err := b.CreateTopic(ctx, testCluster, "", domain.CreateTopicOptions{PartitionCount: 1}); !domain.IsInvalidInput(err) {
		t.Fatalf("CreateTopic empty name error = %v, want invalid input", err)
	}
	if _, err := b.CreateTopic(ctx, testCluster, "bad topic", domain.CreateTopicOptions{PartitionCount: 1}); !domain.IsInvalidInput(err) {
		t.Fatalf("CreateTopic whitespace name error = %v, want invalid input", err)
	}
	if _, err := b.CreateTopic(ctx, testCluster, "events", domain.CreateTopicOptions{PartitionCount: 0}); !domain.IsInvalidInput(err) {
		t.Fatalf("CreateTopic zero partitions error = %v, want invalid input", err)
	}
	if _, err := b.CreateTopic(ctx, testCluster, "events", domain.CreateTopicOptions{PartitionCount: 1}); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	if _, err := b.Fetch(ctx, testCluster, "events", 1, 0, 1); !domain.IsInvalidInput(err) {
		t.Fatalf("Fetch bad partition error = %v, want invalid input", err)
	}
	if _, err := b.Fetch(ctx, testCluster, "events", 0, -1, 1); !domain.IsInvalidInput(err) {
		t.Fatalf("Fetch negative offset error = %v, want invalid input", err)
	}
	if _, err := b.Fetch(ctx, testCluster, "events", 0, 0, -1); !domain.IsInvalidInput(err) {
		t.Fatalf("Fetch negative max records error = %v, want invalid input", err)
	}
	if _, err := b.ListTopics(ctx, domain.ListTopicsOptions{ClusterID: testCluster, NextToken: "x"}); !domain.IsInvalidInput(err) {
		t.Fatalf("ListTopics bad token error = %v, want invalid input", err)
	}
	if _, err := b.ListTopics(ctx, domain.ListTopicsOptions{ClusterID: testCluster, NextToken: "99"}); !domain.IsInvalidInput(err) {
		t.Fatalf("ListTopics out of range token error = %v, want invalid input", err)
	}
}
