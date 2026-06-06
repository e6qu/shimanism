// Package domain holds shimanism's neutral event-streaming interface.
//
// Phase 20 targets the Kafka-shaped intersection: named topics,
// ordered partitions, append-only records, offset fetches, and
// consumer-group committed offsets. Cloud frontends translate their
// control planes into topic operations; the data plane is Kafka wire
// protocol once implemented. Backends own all log and committed-offset
// state. The shim does not keep topic, offset, or group maps.
package domain

import (
	"context"
	"time"
)

// Topic describes a partitioned event-stream topic.
type Topic struct {
	Name           string
	PartitionCount int
	Retention      time.Duration
	Tags           map[string]string
	CreatedAt      time.Time
}

// CreateTopicOptions controls CreateTopic.
type CreateTopicOptions struct {
	PartitionCount int
	Retention      time.Duration
	Tags           map[string]string
}

// ListTopicsOptions controls ListTopics.
type ListTopicsOptions struct {
	Prefix     string
	MaxResults int
	NextToken  string
}

// ListTopicsResult is the ListTopics response.
type ListTopicsResult struct {
	Topics    []Topic
	NextToken string
}

// Header is a Kafka-style record header.
type Header struct {
	Key   string
	Value []byte
}

// ProducerRecord is one record to append to a partition.
type ProducerRecord struct {
	Key       []byte
	Value     []byte
	Headers   []Header
	Timestamp time.Time
}

// RecordMetadata is assigned by the backend after append.
type RecordMetadata struct {
	Topic     string
	Partition int
	Offset    int64
	Timestamp time.Time
}

// Record is a stored event-stream record.
type Record struct {
	Topic     string
	Partition int
	Offset    int64
	Key       []byte
	Value     []byte
	Headers   []Header
	Timestamp time.Time
}

// OffsetBounds carries the earliest and next append offsets for a
// partition. Latest is the offset clients should use to read only new
// records; it is one past the highest stored offset.
type OffsetBounds struct {
	Earliest int64
	Latest   int64
}

// Streams is the interface every event-stream backend implements.
// Implementations must be safe for concurrent use across goroutines.
type Streams interface {
	CreateTopic(ctx context.Context, name string, opt CreateTopicOptions) (Topic, error)
	DeleteTopic(ctx context.Context, name string) error
	DescribeTopic(ctx context.Context, name string) (Topic, error)
	ListTopics(ctx context.Context, opt ListTopicsOptions) (ListTopicsResult, error)

	Produce(ctx context.Context, topic string, partition int, records []ProducerRecord) ([]RecordMetadata, error)
	Fetch(ctx context.Context, topic string, partition int, offset int64, maxRecords int) ([]Record, error)
	ListOffsets(ctx context.Context, topic string, partition int) (OffsetBounds, error)

	CommitOffset(ctx context.Context, group, topic string, partition int, offset int64) error
	FetchCommittedOffset(ctx context.Context, group, topic string, partition int) (int64, error)
}
