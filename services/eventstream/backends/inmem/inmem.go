// Package inmem is an in-process event-stream backend for tests. It is
// a real append-only partition log: records are stored per topic and
// partition, offsets are monotonic, fetch returns stored records, and
// committed offsets live in backend state.
package inmem

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/eventstream/domain"
)

const defaultRetention = 7 * 24 * time.Hour

// Backend implements domain.Streams entirely in memory.
type Backend struct {
	mu      sync.Mutex
	now     func() time.Time
	topics  map[string]*topicState
	offsets map[offsetKey]int64
}

type topicState struct {
	topic      domain.Topic
	partitions []partitionState
}

type partitionState struct {
	records    []domain.Record
	nextOffset int64
}

type offsetKey struct {
	group     string
	topic     string
	partition int
}

// New constructs an empty backend.
func New() *Backend {
	return &Backend{
		now:     func() time.Time { return time.Now().UTC() },
		topics:  map[string]*topicState{},
		offsets: map[offsetKey]int64{},
	}
}

var _ domain.Streams = (*Backend)(nil)

func (b *Backend) CreateTopic(ctx context.Context, name string, opt domain.CreateTopicOptions) (domain.Topic, error) {
	if err := validateTopicName(name); err != nil {
		return domain.Topic{}, err
	}
	if opt.PartitionCount <= 0 {
		return domain.Topic{}, domain.InvalidArgument("partition count must be positive")
	}
	retention := opt.Retention
	if retention == 0 {
		retention = defaultRetention
	}
	if retention < 0 {
		return domain.Topic{}, domain.InvalidArgument("retention must be non-negative")
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.topics[name]; ok {
		return domain.Topic{}, domain.TopicAlreadyExists(name)
	}
	topic := domain.Topic{
		Name:           name,
		PartitionCount: opt.PartitionCount,
		Retention:      retention,
		Tags:           copyStrMap(opt.Tags),
		CreatedAt:      b.now(),
	}
	st := &topicState{
		topic:      topic,
		partitions: make([]partitionState, opt.PartitionCount),
	}
	b.topics[name] = st
	return copyTopic(topic), nil
}

func (b *Backend) DeleteTopic(ctx context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.topics[name]; !ok {
		return domain.TopicNotFound(name)
	}
	delete(b.topics, name)
	for k := range b.offsets {
		if k.topic == name {
			delete(b.offsets, k)
		}
	}
	return nil
}

func (b *Backend) DescribeTopic(ctx context.Context, name string) (domain.Topic, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.topics[name]
	if !ok {
		return domain.Topic{}, domain.TopicNotFound(name)
	}
	return copyTopic(st.topic), nil
}

func (b *Backend) ListTopics(ctx context.Context, opt domain.ListTopicsOptions) (domain.ListTopicsResult, error) {
	start := 0
	if opt.NextToken != "" {
		n, err := strconv.Atoi(opt.NextToken)
		if err != nil || n < 0 {
			return domain.ListTopicsResult{}, domain.InvalidArgument("invalid next token %q", opt.NextToken)
		}
		start = n
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	names := make([]string, 0, len(b.topics))
	for name := range b.topics {
		if opt.Prefix == "" || strings.HasPrefix(name, opt.Prefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if start > len(names) {
		return domain.ListTopicsResult{}, domain.InvalidArgument("next token %q is out of range", opt.NextToken)
	}
	limit := len(names)
	next := ""
	if opt.MaxResults > 0 && start+opt.MaxResults < limit {
		limit = start + opt.MaxResults
		next = strconv.Itoa(limit)
	}
	out := domain.ListTopicsResult{Topics: make([]domain.Topic, 0, limit-start), NextToken: next}
	for _, name := range names[start:limit] {
		out.Topics = append(out.Topics, copyTopic(b.topics[name].topic))
	}
	return out, nil
}

func (b *Backend) Produce(ctx context.Context, topic string, partition int, records []domain.ProducerRecord) ([]domain.RecordMetadata, error) {
	if len(records) == 0 {
		return nil, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, part, err := b.partition(topic, partition)
	if err != nil {
		return nil, err
	}
	now := b.now()
	prunePartition(part, st.topic.Retention, now)
	out := make([]domain.RecordMetadata, 0, len(records))
	for _, in := range records {
		ts := in.Timestamp
		if ts.IsZero() {
			ts = now
		}
		rec := domain.Record{
			Topic:     topic,
			Partition: partition,
			Offset:    part.nextOffset,
			Key:       copyBytes(in.Key),
			Value:     copyBytes(in.Value),
			Headers:   copyHeaders(in.Headers),
			Timestamp: ts.UTC(),
		}
		part.records = append(part.records, rec)
		part.nextOffset++
		out = append(out, domain.RecordMetadata{
			Topic:     rec.Topic,
			Partition: rec.Partition,
			Offset:    rec.Offset,
			Timestamp: rec.Timestamp,
		})
	}
	return out, nil
}

func (b *Backend) Fetch(ctx context.Context, topic string, partition int, offset int64, maxRecords int) ([]domain.Record, error) {
	if offset < 0 {
		return nil, domain.InvalidArgument("offset must be non-negative")
	}
	if maxRecords < 0 {
		return nil, domain.InvalidArgument("max records must be non-negative")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, part, err := b.partition(topic, partition)
	if err != nil {
		return nil, err
	}
	prunePartition(part, st.topic.Retention, b.now())
	if len(part.records) > 0 && offset < part.records[0].Offset {
		return nil, domain.InvalidArgument("offset %d is before earliest retained offset %d", offset, part.records[0].Offset)
	}
	if offset > part.nextOffset {
		return nil, domain.InvalidArgument("offset %d is after latest offset %d", offset, part.nextOffset)
	}
	out := make([]domain.Record, 0, len(part.records))
	for _, rec := range part.records {
		if rec.Offset < offset {
			continue
		}
		if maxRecords > 0 && len(out) >= maxRecords {
			break
		}
		out = append(out, copyRecord(rec))
	}
	return out, nil
}

func (b *Backend) ListOffsets(ctx context.Context, topic string, partition int) (domain.OffsetBounds, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, part, err := b.partition(topic, partition)
	if err != nil {
		return domain.OffsetBounds{}, err
	}
	prunePartition(part, st.topic.Retention, b.now())
	earliest := part.nextOffset
	if len(part.records) > 0 {
		earliest = part.records[0].Offset
	}
	return domain.OffsetBounds{Earliest: earliest, Latest: part.nextOffset}, nil
}

func (b *Backend) CommitOffset(ctx context.Context, group, topic string, partition int, offset int64) error {
	if group == "" {
		return domain.InvalidArgument("consumer group is required")
	}
	if offset < 0 {
		return domain.InvalidArgument("offset must be non-negative")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_, part, err := b.partition(topic, partition)
	if err != nil {
		return err
	}
	if offset > part.nextOffset {
		return domain.InvalidArgument("offset %d is after latest offset %d", offset, part.nextOffset)
	}
	b.offsets[offsetKey{group: group, topic: topic, partition: partition}] = offset
	return nil
}

func (b *Backend) FetchCommittedOffset(ctx context.Context, group, topic string, partition int) (int64, error) {
	if group == "" {
		return 0, domain.InvalidArgument("consumer group is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, _, err := b.partition(topic, partition); err != nil {
		return 0, err
	}
	offset, ok := b.offsets[offsetKey{group: group, topic: topic, partition: partition}]
	if !ok {
		return 0, domain.InvalidArgument("consumer group %q has no committed offset for %s partition %d", group, topic, partition)
	}
	return offset, nil
}

func (b *Backend) partition(topic string, partition int) (*topicState, *partitionState, error) {
	st, ok := b.topics[topic]
	if !ok {
		return nil, nil, domain.TopicNotFound(topic)
	}
	if partition < 0 || partition >= len(st.partitions) {
		return nil, nil, domain.InvalidArgument("partition %d out of range for topic %q", partition, topic)
	}
	return st, &st.partitions[partition], nil
}

func validateTopicName(name string) error {
	if name == "" {
		return domain.InvalidArgument("topic name is required")
	}
	if strings.ContainsAny(name, " \t\r\n") {
		return domain.InvalidArgument("topic name %q contains whitespace", name)
	}
	return nil
}

func prunePartition(part *partitionState, retention time.Duration, now time.Time) {
	if retention <= 0 || len(part.records) == 0 {
		return
	}
	cutoff := now.Add(-retention)
	first := 0
	for first < len(part.records) && part.records[first].Timestamp.Before(cutoff) {
		first++
	}
	if first > 0 {
		part.records = append([]domain.Record(nil), part.records[first:]...)
	}
}

func copyTopic(in domain.Topic) domain.Topic {
	in.Tags = copyStrMap(in.Tags)
	return in
}

func copyRecord(in domain.Record) domain.Record {
	in.Key = copyBytes(in.Key)
	in.Value = copyBytes(in.Value)
	in.Headers = copyHeaders(in.Headers)
	return in
}

func copyHeaders(in []domain.Header) []domain.Header {
	if in == nil {
		return nil
	}
	out := make([]domain.Header, len(in))
	for i, h := range in {
		out[i] = domain.Header{Key: h.Key, Value: copyBytes(h.Value)}
	}
	return out
}

func copyStrMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
