package kafkaserver

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/eventstream/kafkawire"
	"github.com/e6qu/shimanism/services/eventstream/backends/inmem"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func TestServerKafkaRoundTrip(t *testing.T) {
	srv := New(inmem.New(), Config{BrokerID: 7, Host: "broker.local", Port: 19092})

	create := kmsg.NewCreateTopicsRequest()
	create.SetVersion(5)
	create.Topics = []kmsg.CreateTopicsRequestTopic{{
		Topic:             "events",
		NumPartitions:     1,
		ReplicationFactor: 1,
	}}
	createResp := request(t, srv, &create, 1, decodeCreateTopicsResponse)
	if got := createResp.Topics[0].ErrorCode; got != 0 {
		t.Fatalf("CreateTopics error = %d, want 0", got)
	}
	if got := createResp.Topics[0].NumPartitions; got != 1 {
		t.Fatalf("created partitions = %d, want 1", got)
	}

	meta := kmsg.NewMetadataRequest()
	meta.SetVersion(8)
	meta.Topics = []kmsg.MetadataRequestTopic{{Topic: kmsg.StringPtr("events")}}
	metaResp := request(t, srv, &meta, 2, decodeMetadataResponse)
	if len(metaResp.Brokers) != 1 || metaResp.Brokers[0].NodeID != 7 {
		t.Fatalf("brokers = %#v, want broker id 7", metaResp.Brokers)
	}
	if len(metaResp.Topics) != 1 || metaResp.Topics[0].ErrorCode != 0 {
		t.Fatalf("metadata topics = %#v, want one successful topic", metaResp.Topics)
	}
	if len(metaResp.Topics[0].Partitions) != 1 || metaResp.Topics[0].Partitions[0].Leader != 7 {
		t.Fatalf("metadata partitions = %#v, want one partition led by 7", metaResp.Topics[0].Partitions)
	}

	produce := kmsg.NewProduceRequest()
	produce.SetVersion(8)
	produce.Acks = 1
	produce.Topics = []kmsg.ProduceRequestTopic{{
		Topic: "events",
		Partitions: []kmsg.ProduceRequestTopicPartition{{
			Partition: 0,
			Records: encodeRecordBatchForTest([]testRecord{
				{Key: []byte("k1"), Value: []byte("v1"), Headers: []kmsg.Header{{Key: "h", Value: []byte("one")}}},
				{Key: []byte("k2"), Value: []byte("v2")},
			}),
		}},
	}}
	produceResp := request(t, srv, &produce, 3, decodeProduceResponse)
	producedPart := produceResp.Topics[0].Partitions[0]
	if producedPart.ErrorCode != 0 || producedPart.BaseOffset != 0 {
		t.Fatalf("produce partition = %#v, want success base offset 0", producedPart)
	}

	listLatest := kmsg.NewListOffsetsRequest()
	listLatest.SetVersion(5)
	listLatest.Topics = []kmsg.ListOffsetsRequestTopic{{
		Topic: "events",
		Partitions: []kmsg.ListOffsetsRequestTopicPartition{{
			Partition: 0,
			Timestamp: -1,
		}},
	}}
	latestResp := request(t, srv, &listLatest, 4, decodeListOffsetsResponse)
	if got := latestResp.Topics[0].Partitions[0].Offset; got != 2 {
		t.Fatalf("latest offset = %d, want 2", got)
	}

	fetch := kmsg.NewFetchRequest()
	fetch.SetVersion(11)
	fetch.ReplicaID = -1
	fetch.MaxBytes = 1 << 20
	fetch.Topics = []kmsg.FetchRequestTopic{{
		Topic: "events",
		Partitions: []kmsg.FetchRequestTopicPartition{{
			Partition:         0,
			FetchOffset:       0,
			PartitionMaxBytes: 1 << 20,
		}},
	}}
	fetchResp := request(t, srv, &fetch, 5, decodeFetchResponse)
	fetchedPart := fetchResp.Topics[0].Partitions[0]
	if fetchedPart.ErrorCode != 0 || fetchedPart.HighWatermark != 2 || fetchedPart.LogStartOffset != 0 {
		t.Fatalf("fetch partition = %#v, want success bounds 0..2", fetchedPart)
	}
	gotRecords := decodeRecordBatchValues(t, fetchedPart.RecordBatches)
	if got := string(gotRecords[0].Value); got != "v1" {
		t.Fatalf("record[0] value = %q, want v1", got)
	}
	if got := string(gotRecords[0].Headers[0].Value); got != "one" {
		t.Fatalf("record[0] header = %q, want one", got)
	}
	if got := string(gotRecords[1].Key); got != "k2" {
		t.Fatalf("record[1] key = %q, want k2", got)
	}

	commit := kmsg.NewOffsetCommitRequest()
	commit.SetVersion(7)
	commit.Group = "workers"
	commit.Generation = -1
	commit.Topics = []kmsg.OffsetCommitRequestTopic{{
		Topic: "events",
		Partitions: []kmsg.OffsetCommitRequestTopicPartition{{
			Partition: 0,
			Offset:    2,
		}},
	}}
	commitResp := request(t, srv, &commit, 6, decodeOffsetCommitResponse)
	if got := commitResp.Topics[0].Partitions[0].ErrorCode; got != 0 {
		t.Fatalf("offset commit error = %d, want 0", got)
	}

	offsetFetch := kmsg.NewOffsetFetchRequest()
	offsetFetch.SetVersion(7)
	offsetFetch.Group = "workers"
	offsetFetch.Topics = []kmsg.OffsetFetchRequestTopic{{
		Topic:      "events",
		Partitions: []int32{0},
	}}
	offsetFetchResp := request(t, srv, &offsetFetch, 7, decodeOffsetFetchResponse)
	if got := offsetFetchResp.Topics[0].Partitions[0].Offset; got != 2 {
		t.Fatalf("committed offset = %d, want 2", got)
	}
}

func TestServerReturnsKafkaErrors(t *testing.T) {
	srv := New(inmem.New(), Config{})

	produce := kmsg.NewProduceRequest()
	produce.SetVersion(8)
	produce.Acks = 1
	produce.Topics = []kmsg.ProduceRequestTopic{{
		Topic: "missing",
		Partitions: []kmsg.ProduceRequestTopicPartition{{
			Partition: 0,
			Records:   encodeRecordBatchForTest([]testRecord{{Value: []byte("v")}}),
		}},
	}}
	produceResp := request(t, srv, &produce, 10, decodeProduceResponse)
	if got := produceResp.Topics[0].Partitions[0].ErrorCode; got != errUnknownTopicOrPartition {
		t.Fatalf("unknown topic produce error = %d, want %d", got, errUnknownTopicOrPartition)
	}

	create := kmsg.NewCreateTopicsRequest()
	create.SetVersion(4)
	create.Topics = []kmsg.CreateTopicsRequestTopic{{
		Topic:             "bad",
		NumPartitions:     1,
		ReplicationFactor: 1,
		Configs: []kmsg.CreateTopicsRequestTopicConfig{{
			Name:  "cleanup.policy",
			Value: kmsg.StringPtr("compact"),
		}},
	}}
	createResp := request(t, srv, &create, 11, decodeCreateTopicsResponse)
	if got := createResp.Topics[0].ErrorCode; got != errInvalidConfig {
		t.Fatalf("unsupported config error = %d, want %d", got, errInvalidConfig)
	}

	join := kmsg.NewJoinGroupRequest()
	join.SetVersion(5)
	join.Group = "workers"
	join.MemberID = ""
	join.ProtocolType = "consumer"
	join.Protocols = []kmsg.JoinGroupRequestProtocol{{Name: "range", Metadata: []byte{0}}}
	joinResp := request(t, srv, &join, 12, decodeJoinGroupResponse)
	if got := joinResp.ErrorCode; got != errUnsupportedVersion {
		t.Fatalf("join group error = %d, want %d", got, errUnsupportedVersion)
	}
}

func request[T kmsg.Response](t *testing.T, srv *Server, req kmsg.Request, correlationID int32, decode func([]byte, int16) T) T {
	t.Helper()
	wire := kmsg.NewRequestFormatter(kmsg.FormatterClientID("test-client")).AppendRequest(nil, req, correlationID)
	respWire, err := srv.HandleFrame(t.Context(), wire[4:])
	if err != nil {
		t.Fatalf("HandleFrame: %v", err)
	}
	payload, err := kafkawire.ReadFrame(bytes.NewReader(respWire), kafkawire.DefaultMaxFrameSize)
	if err != nil {
		t.Fatalf("ReadFrame response: %v", err)
	}
	if got := int32(binary.BigEndian.Uint32(payload[:4])); got != correlationID {
		t.Fatalf("correlation ID = %d, want %d", got, correlationID)
	}
	body := payload[4:]
	resp := decode(body, req.GetVersion())
	if resp.IsFlexible() {
		body = body[1:]
	}
	if err := resp.ReadFrom(body); err != nil {
		t.Fatalf("decode response %T: %v", resp, err)
	}
	return resp
}

func decodeCreateTopicsResponse(_ []byte, version int16) *kmsg.CreateTopicsResponse {
	resp := kmsg.NewCreateTopicsResponse()
	resp.SetVersion(version)
	return &resp
}

func decodeMetadataResponse(_ []byte, version int16) *kmsg.MetadataResponse {
	resp := kmsg.NewMetadataResponse()
	resp.SetVersion(version)
	return &resp
}

func decodeProduceResponse(_ []byte, version int16) *kmsg.ProduceResponse {
	resp := kmsg.NewProduceResponse()
	resp.SetVersion(version)
	return &resp
}

func decodeListOffsetsResponse(_ []byte, version int16) *kmsg.ListOffsetsResponse {
	resp := kmsg.NewListOffsetsResponse()
	resp.SetVersion(version)
	return &resp
}

func decodeFetchResponse(_ []byte, version int16) *kmsg.FetchResponse {
	resp := kmsg.NewFetchResponse()
	resp.SetVersion(version)
	return &resp
}

func decodeOffsetCommitResponse(_ []byte, version int16) *kmsg.OffsetCommitResponse {
	resp := kmsg.NewOffsetCommitResponse()
	resp.SetVersion(version)
	return &resp
}

func decodeOffsetFetchResponse(_ []byte, version int16) *kmsg.OffsetFetchResponse {
	resp := kmsg.NewOffsetFetchResponse()
	resp.SetVersion(version)
	return &resp
}

func decodeJoinGroupResponse(_ []byte, version int16) *kmsg.JoinGroupResponse {
	resp := kmsg.NewJoinGroupResponse()
	resp.SetVersion(version)
	return &resp
}

type testRecord struct {
	Key     []byte
	Value   []byte
	Headers []kmsg.Header
}

func encodeRecordBatchForTest(records []testRecord) []byte {
	var stored []testStoredRecord
	for i, rec := range records {
		stored = append(stored, testStoredRecord{
			offset:  int64(i),
			key:     rec.Key,
			value:   rec.Value,
			headers: rec.Headers,
		})
	}
	return encodeTestBatch(stored)
}

func decodeRecordBatchValues(t *testing.T, wire []byte) []kmsg.Record {
	t.Helper()
	if len(wire) < 12 {
		t.Fatalf("record batch is truncated")
	}
	var batch kmsg.RecordBatch
	if err := batch.ReadFrom(wire); err != nil {
		t.Fatalf("RecordBatch.ReadFrom: %v", err)
	}
	src := batch.Records
	var records []kmsg.Record
	for i := int32(0); i < batch.NumRecords; i++ {
		length, n := readVarint(src)
		if n <= 0 {
			t.Fatalf("record %d length decode failed", i)
		}
		total := n + int(length)
		var rec kmsg.Record
		if err := rec.ReadFrom(src[:total]); err != nil {
			t.Fatalf("Record.ReadFrom: %v", err)
		}
		records = append(records, rec)
		src = src[total:]
	}
	return records
}

type testStoredRecord struct {
	offset  int64
	key     []byte
	value   []byte
	headers []kmsg.Header
}

func encodeTestBatch(records []testStoredRecord) []byte {
	var recordBytes []byte
	nowMillis := time.Now().UTC().UnixMilli()
	for i, rec := range records {
		recordBytes = append(recordBytes, encodeRecord(kmsg.Record{
			OffsetDelta: int32(i),
			Key:         rec.key,
			Value:       rec.value,
			Headers:     rec.headers,
		})...)
	}
	batch := kmsg.RecordBatch{
		FirstOffset:          records[0].offset,
		Length:               int32(49 + len(recordBytes)),
		PartitionLeaderEpoch: 0,
		Magic:                2,
		LastOffsetDelta:      int32(len(records) - 1),
		FirstTimestamp:       nowMillis,
		MaxTimestamp:         nowMillis,
		ProducerID:           -1,
		ProducerEpoch:        -1,
		FirstSequence:        -1,
		NumRecords:           int32(len(records)),
		Records:              recordBytes,
	}
	wire := batch.AppendTo(nil)
	binary.BigEndian.PutUint32(wire[17:21], crc32Checksum(wire[21:]))
	return wire
}

func crc32Checksum(src []byte) uint32 {
	return crc32.Checksum(src, crc32c)
}
