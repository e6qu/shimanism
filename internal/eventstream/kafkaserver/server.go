// Package kafkaserver maps Kafka TCP requests onto the neutral event-stream
// domain backend.
package kafkaserver

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/e6qu/shimanism/internal/eventstream/domain"
	"github.com/e6qu/shimanism/internal/eventstream/kafkawire"
	"github.com/twmb/franz-go/pkg/kmsg"
)

const (
	errOffsetOutOfRange           int16 = 1
	errCorruptMessage             int16 = 2
	errUnknownTopicOrPartition    int16 = 3
	errInvalidRequiredAcks        int16 = 21
	errInvalidGroupID             int16 = 24
	errUnknownMemberID            int16 = 25
	errUnsupportedVersion         int16 = 35
	errTopicAlreadyExists         int16 = 36
	errInvalidPartitions          int16 = 37
	errInvalidReplicationFactor   int16 = 38
	errInvalidReplicaAssignment   int16 = 39
	errInvalidConfig              int16 = 40
	errInvalidRequest             int16 = 42
	errUnsupportedCompressionType int16 = 76
	errUnknownTopicID             int16 = 100
	defaultBrokerID               int32 = 1
	defaultHost                         = "127.0.0.1"
	defaultPort                   int32 = 9092
	defaultClusterID                    = "shimanism-eventstream"
	defaultPartitions                   = 1
	defaultMaxRecordsPerFetch           = 1024
)

var crc32c = crc32.MakeTable(crc32.Castagnoli)

// Config controls the single-broker Kafka view this handler advertises.
type Config struct {
	BrokerID          int32
	Host              string
	Port              int32
	ClusterID         string
	DefaultPartitions int
	MaxFrameSize      int32
}

// Server serves Kafka request frames against a domain.Streams backend.
type Server struct {
	streams domain.Streams
	cfg     Config
}

// New returns a Kafka protocol server backed by streams.
func New(streams domain.Streams, cfg Config) *Server {
	if cfg.BrokerID == 0 {
		cfg.BrokerID = defaultBrokerID
	}
	if cfg.Host == "" {
		cfg.Host = defaultHost
	}
	if cfg.Port == 0 {
		cfg.Port = defaultPort
	}
	if cfg.ClusterID == "" {
		cfg.ClusterID = defaultClusterID
	}
	if cfg.DefaultPartitions <= 0 {
		cfg.DefaultPartitions = defaultPartitions
	}
	return &Server{streams: streams, cfg: cfg}
}

// ServeConn reads Kafka request frames from conn until EOF, context
// cancellation, or a malformed request.
func (s *Server) ServeConn(ctx context.Context, conn net.Conn) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		payload, err := kafkawire.ReadFrame(conn, s.cfg.MaxFrameSize)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		resp, err := s.HandleFrame(ctx, payload)
		if err != nil {
			return err
		}
		if len(resp) == 0 {
			continue
		}
		if _, err := conn.Write(resp); err != nil {
			return err
		}
	}
}

// HandleFrame decodes and handles one Kafka request payload, excluding the
// frame length prefix.
func (s *Server) HandleFrame(ctx context.Context, payload []byte) ([]byte, error) {
	frame, err := kafkawire.DecodeRequest(payload)
	if err != nil {
		return nil, err
	}
	resp, noResponse, err := s.dispatch(ctx, frame)
	if err != nil {
		return nil, err
	}
	if noResponse {
		return nil, nil
	}
	return kafkawire.AppendResponse(nil, frame.CorrelationID, resp), nil
}

func (s *Server) dispatch(ctx context.Context, frame kafkawire.RequestFrame) (kmsg.Response, bool, error) {
	switch req := frame.Request.(type) {
	case *kmsg.ApiVersionsRequest:
		return s.apiVersions(req), false, nil
	case *kmsg.MetadataRequest:
		return s.metadata(ctx, req), false, nil
	case *kmsg.CreateTopicsRequest:
		return s.createTopics(ctx, req), false, nil
	case *kmsg.DeleteTopicsRequest:
		return s.deleteTopics(ctx, req), false, nil
	case *kmsg.ProduceRequest:
		resp := s.produce(ctx, req)
		return resp, req.Acks == 0 && !produceHasError(resp), nil
	case *kmsg.FetchRequest:
		return s.fetch(ctx, req), false, nil
	case *kmsg.ListOffsetsRequest:
		return s.listOffsets(ctx, req), false, nil
	case *kmsg.FindCoordinatorRequest:
		return s.findCoordinator(req), false, nil
	case *kmsg.OffsetCommitRequest:
		return s.offsetCommit(ctx, req), false, nil
	case *kmsg.OffsetFetchRequest:
		return s.offsetFetch(ctx, req), false, nil
	case *kmsg.JoinGroupRequest:
		return unsupportedJoinGroup(req), false, nil
	case *kmsg.HeartbeatRequest:
		return unsupportedHeartbeat(req), false, nil
	case *kmsg.LeaveGroupRequest:
		return unsupportedLeaveGroup(req), false, nil
	case *kmsg.SyncGroupRequest:
		return unsupportedSyncGroup(req), false, nil
	default:
		return nil, false, fmt.Errorf("decoded unsupported kafka request %T: %w", req, kafkawire.ErrUnsupportedAPI)
	}
}

func (s *Server) apiVersions(req *kmsg.ApiVersionsRequest) *kmsg.ApiVersionsResponse {
	resp := kmsg.NewApiVersionsResponse()
	resp.SetVersion(minInt16(req.Version, resp.MaxVersion()))
	if req.Version > resp.MaxVersion() {
		resp.ErrorCode = errUnsupportedVersion
	}
	resp.ApiKeys = []kmsg.ApiVersionsResponseApiKey{
		{ApiKey: 0, MinVersion: 3, MaxVersion: 8},
		{ApiKey: 1, MinVersion: 4, MaxVersion: 11},
		{ApiKey: 2, MinVersion: 1, MaxVersion: 5},
		{ApiKey: 3, MinVersion: 1, MaxVersion: 8},
		{ApiKey: 8, MinVersion: 0, MaxVersion: 7},
		{ApiKey: 9, MinVersion: 0, MaxVersion: 7},
		{ApiKey: 10, MinVersion: 0, MaxVersion: 4},
		{ApiKey: 11, MinVersion: 0, MaxVersion: 5},
		{ApiKey: 12, MinVersion: 0, MaxVersion: 3},
		{ApiKey: 13, MinVersion: 0, MaxVersion: 3},
		{ApiKey: 14, MinVersion: 0, MaxVersion: 3},
		{ApiKey: 18, MinVersion: 0, MaxVersion: 3},
		{ApiKey: 19, MinVersion: 0, MaxVersion: 5},
		{ApiKey: 20, MinVersion: 0, MaxVersion: 3},
	}
	return &resp
}

func (s *Server) metadata(ctx context.Context, req *kmsg.MetadataRequest) *kmsg.MetadataResponse {
	resp := kmsg.NewMetadataResponse()
	resp.SetVersion(req.Version)
	resp.Brokers = []kmsg.MetadataResponseBroker{s.broker()}
	resp.ClusterID = &s.cfg.ClusterID
	resp.ControllerID = s.cfg.BrokerID

	allTopics := req.Version >= 1 && req.Topics == nil
	var topics []domain.Topic
	if allTopics || req.Version == 0 && len(req.Topics) == 0 {
		list, err := s.streams.ListTopics(ctx, domain.ListTopicsOptions{})
		if err != nil {
			resp.ErrorCode = errInvalidRequest
			return &resp
		}
		topics = list.Topics
	}
	if len(topics) > 0 {
		resp.Topics = make([]kmsg.MetadataResponseTopic, 0, len(topics))
		for _, topic := range topics {
			resp.Topics = append(resp.Topics, s.metadataTopic(topic, 0))
		}
		return &resp
	}

	resp.Topics = make([]kmsg.MetadataResponseTopic, 0, len(req.Topics))
	for _, requested := range req.Topics {
		if req.Version >= 10 && requested.Topic == nil {
			resp.Topics = append(resp.Topics, metadataUnknownTopicID(requested.TopicID))
			continue
		}
		name := topicName(requested.Topic)
		topic, err := s.streams.DescribeTopic(ctx, name)
		if err != nil {
			code := domainErrCode(err)
			resp.Topics = append(resp.Topics, metadataUnknownTopic(name, code))
			continue
		}
		resp.Topics = append(resp.Topics, s.metadataTopic(topic, 0))
	}
	return &resp
}

func (s *Server) metadataTopic(topic domain.Topic, code int16) kmsg.MetadataResponseTopic {
	out := kmsg.NewMetadataResponseTopic()
	out.Topic = &topic.Name
	out.ErrorCode = code
	out.Partitions = make([]kmsg.MetadataResponseTopicPartition, 0, topic.PartitionCount)
	for i := 0; i < topic.PartitionCount; i++ {
		part := kmsg.NewMetadataResponseTopicPartition()
		part.Partition = int32(i)
		part.Leader = s.cfg.BrokerID
		part.Replicas = []int32{s.cfg.BrokerID}
		part.ISR = []int32{s.cfg.BrokerID}
		out.Partitions = append(out.Partitions, part)
	}
	return out
}

func (s *Server) createTopics(ctx context.Context, req *kmsg.CreateTopicsRequest) *kmsg.CreateTopicsResponse {
	resp := kmsg.NewCreateTopicsResponse()
	resp.SetVersion(req.Version)
	resp.Topics = make([]kmsg.CreateTopicsResponseTopic, 0, len(req.Topics))
	seen := map[string]struct{}{}
	for _, in := range req.Topics {
		out := kmsg.NewCreateTopicsResponseTopic()
		out.Topic = in.Topic
		if _, ok := seen[in.Topic]; ok {
			out.ErrorCode = errInvalidRequest
			out.ErrorMessage = stringPtr("duplicate topic in request")
			resp.Topics = append(resp.Topics, out)
			continue
		}
		seen[in.Topic] = struct{}{}
		partitions, retention, code, msg := s.createTopicOptions(in)
		if code != 0 {
			out.ErrorCode = code
			out.ErrorMessage = &msg
			resp.Topics = append(resp.Topics, out)
			continue
		}
		if req.ValidateOnly {
			out.NumPartitions = int32(partitions)
			out.ReplicationFactor = 1
			resp.Topics = append(resp.Topics, out)
			continue
		}
		topic, err := s.streams.CreateTopic(ctx, in.Topic, domain.CreateTopicOptions{
			PartitionCount: partitions,
			Retention:      retention,
		})
		if err != nil {
			out.ErrorCode = createTopicErrCode(err)
			out.ErrorMessage = stringPtr(err.Error())
		} else {
			out.NumPartitions = int32(topic.PartitionCount)
			out.ReplicationFactor = 1
		}
		resp.Topics = append(resp.Topics, out)
	}
	return &resp
}

func (s *Server) createTopicOptions(topic kmsg.CreateTopicsRequestTopic) (int, time.Duration, int16, string) {
	if len(topic.ReplicaAssignment) > 0 {
		return 0, 0, errInvalidReplicaAssignment, "manual replica assignment is outside the single-broker intersection"
	}
	partitions := int(topic.NumPartitions)
	if topic.NumPartitions == -1 {
		partitions = s.cfg.DefaultPartitions
	}
	if partitions <= 0 {
		return 0, 0, errInvalidPartitions, "partition count must be positive"
	}
	if topic.ReplicationFactor != -1 && topic.ReplicationFactor != 1 {
		return 0, 0, errInvalidReplicationFactor, "replication factor must be 1 for the portable single-broker view"
	}
	var retention time.Duration
	for _, cfg := range topic.Configs {
		switch cfg.Name {
		case "retention.ms":
			if cfg.Value == nil {
				continue
			}
			ms, err := strconv.ParseInt(*cfg.Value, 10, 64)
			if err != nil || ms < -1 {
				return 0, 0, errInvalidConfig, "retention.ms must be -1 or a non-negative integer"
			}
			if ms >= 0 {
				retention = time.Duration(ms) * time.Millisecond
			}
		default:
			return 0, 0, errInvalidConfig, "unsupported topic config " + cfg.Name
		}
	}
	return partitions, retention, 0, ""
}

func (s *Server) deleteTopics(ctx context.Context, req *kmsg.DeleteTopicsRequest) *kmsg.DeleteTopicsResponse {
	resp := kmsg.NewDeleteTopicsResponse()
	resp.SetVersion(req.Version)
	if req.Version <= 5 {
		resp.Topics = make([]kmsg.DeleteTopicsResponseTopic, 0, len(req.TopicNames))
		for _, name := range req.TopicNames {
			resp.Topics = append(resp.Topics, s.deleteTopic(ctx, name))
		}
		return &resp
	}
	resp.Topics = make([]kmsg.DeleteTopicsResponseTopic, 0, len(req.Topics))
	for _, topic := range req.Topics {
		if topic.Topic == nil {
			out := kmsg.NewDeleteTopicsResponseTopic()
			out.TopicID = topic.TopicID
			out.ErrorCode = errUnknownTopicID
			out.ErrorMessage = stringPtr("topic IDs are not supported by this stateless Kafka frontend")
			resp.Topics = append(resp.Topics, out)
			continue
		}
		resp.Topics = append(resp.Topics, s.deleteTopic(ctx, *topic.Topic))
	}
	return &resp
}

func (s *Server) deleteTopic(ctx context.Context, name string) kmsg.DeleteTopicsResponseTopic {
	out := kmsg.NewDeleteTopicsResponseTopic()
	out.Topic = &name
	if err := s.streams.DeleteTopic(ctx, name); err != nil {
		out.ErrorCode = domainErrCode(err)
		out.ErrorMessage = stringPtr(err.Error())
	}
	return out
}

func (s *Server) produce(ctx context.Context, req *kmsg.ProduceRequest) *kmsg.ProduceResponse {
	resp := kmsg.NewProduceResponse()
	resp.SetVersion(req.Version)
	resp.Topics = make([]kmsg.ProduceResponseTopic, 0, len(req.Topics))
	if req.Acks != -1 && req.Acks != 0 && req.Acks != 1 {
		for _, topic := range req.Topics {
			out := kmsg.NewProduceResponseTopic()
			out.Topic = topic.Topic
			out.Partitions = producePartitionErrors(topic.Partitions, errInvalidRequiredAcks, "invalid required acks")
			resp.Topics = append(resp.Topics, out)
		}
		return &resp
	}
	for _, topic := range req.Topics {
		out := kmsg.NewProduceResponseTopic()
		out.Topic = topic.Topic
		out.TopicID = topic.TopicID
		out.Partitions = make([]kmsg.ProduceResponseTopicPartition, 0, len(topic.Partitions))
		for _, part := range topic.Partitions {
			pout := kmsg.NewProduceResponseTopicPartition()
			pout.Partition = part.Partition
			name := topic.Topic
			if req.Version >= 13 {
				pout.ErrorCode = errUnknownTopicID
				pout.ErrorMessage = stringPtr("topic IDs are not supported by this stateless Kafka frontend")
				out.Partitions = append(out.Partitions, pout)
				continue
			}
			if req.TransactionID != nil {
				pout.ErrorCode = errInvalidRequest
				pout.ErrorMessage = stringPtr("transactions are outside the event-stream intersection")
				out.Partitions = append(out.Partitions, pout)
				continue
			}
			records, code, msg := decodeRecordBatches(part.Records)
			if code != 0 {
				pout.ErrorCode = code
				pout.ErrorMessage = stringPtr(msg)
				out.Partitions = append(out.Partitions, pout)
				continue
			}
			meta, err := s.streams.Produce(ctx, name, int(part.Partition), records)
			if err != nil {
				pout.ErrorCode = domainErrCode(err)
				pout.ErrorMessage = stringPtr(err.Error())
			} else if len(meta) > 0 {
				pout.BaseOffset = meta[0].Offset
			} else {
				pout.BaseOffset = -1
			}
			out.Partitions = append(out.Partitions, pout)
		}
		resp.Topics = append(resp.Topics, out)
	}
	return &resp
}

func (s *Server) fetch(ctx context.Context, req *kmsg.FetchRequest) *kmsg.FetchResponse {
	resp := kmsg.NewFetchResponse()
	resp.SetVersion(req.Version)
	resp.Topics = make([]kmsg.FetchResponseTopic, 0, len(req.Topics))
	for _, topic := range req.Topics {
		out := kmsg.NewFetchResponseTopic()
		out.Topic = topic.Topic
		out.TopicID = topic.TopicID
		out.Partitions = make([]kmsg.FetchResponseTopicPartition, 0, len(topic.Partitions))
		for _, part := range topic.Partitions {
			pout := kmsg.NewFetchResponseTopicPartition()
			pout.Partition = part.Partition
			if req.Version >= 13 {
				pout.ErrorCode = errUnknownTopicID
				out.Partitions = append(out.Partitions, pout)
				continue
			}
			bounds, err := s.streams.ListOffsets(ctx, topic.Topic, int(part.Partition))
			if err != nil {
				pout.ErrorCode = domainErrCode(err)
				out.Partitions = append(out.Partitions, pout)
				continue
			}
			pout.HighWatermark = bounds.Latest
			pout.LastStableOffset = bounds.Latest
			pout.LogStartOffset = bounds.Earliest
			records, err := s.streams.Fetch(ctx, topic.Topic, int(part.Partition), part.FetchOffset, defaultMaxRecordsPerFetch)
			if err != nil {
				pout.ErrorCode = fetchErrCode(err)
				out.Partitions = append(out.Partitions, pout)
				continue
			}
			pout.RecordBatches = encodeRecordBatches(records)
			out.Partitions = append(out.Partitions, pout)
		}
		resp.Topics = append(resp.Topics, out)
	}
	return &resp
}

func (s *Server) listOffsets(ctx context.Context, req *kmsg.ListOffsetsRequest) *kmsg.ListOffsetsResponse {
	resp := kmsg.NewListOffsetsResponse()
	resp.SetVersion(req.Version)
	resp.Topics = make([]kmsg.ListOffsetsResponseTopic, 0, len(req.Topics))
	for _, topic := range req.Topics {
		out := kmsg.NewListOffsetsResponseTopic()
		out.Topic = topic.Topic
		out.Partitions = make([]kmsg.ListOffsetsResponseTopicPartition, 0, len(topic.Partitions))
		for _, part := range topic.Partitions {
			pout := kmsg.NewListOffsetsResponseTopicPartition()
			pout.Partition = part.Partition
			bounds, err := s.streams.ListOffsets(ctx, topic.Topic, int(part.Partition))
			if err != nil {
				pout.ErrorCode = domainErrCode(err)
				out.Partitions = append(out.Partitions, pout)
				continue
			}
			switch part.Timestamp {
			case -2:
				pout.Offset = bounds.Earliest
			case -1:
				pout.Offset = bounds.Latest
			default:
				pout.ErrorCode = errInvalidRequest
			}
			out.Partitions = append(out.Partitions, pout)
		}
		resp.Topics = append(resp.Topics, out)
	}
	return &resp
}

func (s *Server) findCoordinator(req *kmsg.FindCoordinatorRequest) *kmsg.FindCoordinatorResponse {
	resp := kmsg.NewFindCoordinatorResponse()
	resp.SetVersion(req.Version)
	if req.Version <= 3 {
		if req.Version >= 1 && req.CoordinatorType != 0 {
			resp.ErrorCode = errInvalidRequest
			resp.ErrorMessage = stringPtr("only group coordinators are in the event-stream intersection")
			return &resp
		}
		resp.NodeID = s.cfg.BrokerID
		resp.Host = s.cfg.Host
		resp.Port = s.cfg.Port
		return &resp
	}
	resp.Coordinators = make([]kmsg.FindCoordinatorResponseCoordinator, 0, len(req.CoordinatorKeys))
	for _, key := range req.CoordinatorKeys {
		out := kmsg.NewFindCoordinatorResponseCoordinator()
		out.Key = key
		if req.CoordinatorType != 0 {
			out.ErrorCode = errInvalidRequest
			out.ErrorMessage = stringPtr("only group coordinators are in the event-stream intersection")
		} else {
			out.NodeID = s.cfg.BrokerID
			out.Host = s.cfg.Host
			out.Port = s.cfg.Port
		}
		resp.Coordinators = append(resp.Coordinators, out)
	}
	return &resp
}

func (s *Server) offsetCommit(ctx context.Context, req *kmsg.OffsetCommitRequest) *kmsg.OffsetCommitResponse {
	resp := kmsg.NewOffsetCommitResponse()
	resp.SetVersion(req.Version)
	resp.Topics = make([]kmsg.OffsetCommitResponseTopic, 0, len(req.Topics))
	groupErr := groupOffsetRequestErr(req.Group, req.Generation, req.MemberID)
	for _, topic := range req.Topics {
		out := kmsg.NewOffsetCommitResponseTopic()
		out.Topic = topic.Topic
		out.TopicID = topic.TopicID
		out.Partitions = make([]kmsg.OffsetCommitResponseTopicPartition, 0, len(topic.Partitions))
		for _, part := range topic.Partitions {
			pout := kmsg.NewOffsetCommitResponseTopicPartition()
			pout.Partition = part.Partition
			if req.Version >= 10 {
				pout.ErrorCode = errUnknownTopicID
			} else if groupErr != 0 {
				pout.ErrorCode = groupErr
			} else if err := s.streams.CommitOffset(ctx, req.Group, topic.Topic, int(part.Partition), part.Offset); err != nil {
				pout.ErrorCode = commitErrCode(err)
			}
			out.Partitions = append(out.Partitions, pout)
		}
		resp.Topics = append(resp.Topics, out)
	}
	return &resp
}

func (s *Server) offsetFetch(ctx context.Context, req *kmsg.OffsetFetchRequest) *kmsg.OffsetFetchResponse {
	resp := kmsg.NewOffsetFetchResponse()
	resp.SetVersion(req.Version)
	if req.Version <= 7 {
		if req.Group == "" {
			resp.ErrorCode = errInvalidGroupID
			return &resp
		}
		if req.Topics == nil {
			resp.ErrorCode = errInvalidRequest
			return &resp
		}
		resp.Topics = make([]kmsg.OffsetFetchResponseTopic, 0, len(req.Topics))
		for _, topic := range req.Topics {
			out := kmsg.NewOffsetFetchResponseTopic()
			out.Topic = topic.Topic
			out.Partitions = make([]kmsg.OffsetFetchResponseTopicPartition, 0, len(topic.Partitions))
			for _, partition := range topic.Partitions {
				pout := kmsg.NewOffsetFetchResponseTopicPartition()
				pout.Partition = partition
				offset, err := s.streams.FetchCommittedOffset(ctx, req.Group, topic.Topic, int(partition))
				if err != nil {
					pout.ErrorCode = fetchOffsetErrCode(err)
					pout.Offset = -1
				} else {
					pout.Offset = offset
				}
				out.Partitions = append(out.Partitions, pout)
			}
			resp.Topics = append(resp.Topics, out)
		}
		return &resp
	}
	resp.Groups = make([]kmsg.OffsetFetchResponseGroup, 0, len(req.Groups))
	for _, group := range req.Groups {
		gout := kmsg.NewOffsetFetchResponseGroup()
		gout.Group = group.Group
		if group.Group == "" {
			gout.ErrorCode = errInvalidGroupID
			resp.Groups = append(resp.Groups, gout)
			continue
		}
		if group.Topics == nil {
			gout.ErrorCode = errInvalidRequest
			resp.Groups = append(resp.Groups, gout)
			continue
		}
		gout.Topics = make([]kmsg.OffsetFetchResponseGroupTopic, 0, len(group.Topics))
		for _, topic := range group.Topics {
			tout := kmsg.NewOffsetFetchResponseGroupTopic()
			tout.Topic = topic.Topic
			tout.TopicID = topic.TopicID
			tout.Partitions = make([]kmsg.OffsetFetchResponseGroupTopicPartition, 0, len(topic.Partitions))
			for _, partition := range topic.Partitions {
				pout := kmsg.NewOffsetFetchResponseGroupTopicPartition()
				pout.Partition = partition
				if req.Version >= 10 {
					pout.ErrorCode = errUnknownTopicID
					pout.Offset = -1
				} else {
					offset, err := s.streams.FetchCommittedOffset(ctx, group.Group, topic.Topic, int(partition))
					if err != nil {
						pout.ErrorCode = fetchOffsetErrCode(err)
						pout.Offset = -1
					} else {
						pout.Offset = offset
					}
				}
				tout.Partitions = append(tout.Partitions, pout)
			}
			gout.Topics = append(gout.Topics, tout)
		}
		resp.Groups = append(resp.Groups, gout)
	}
	return &resp
}

func unsupportedJoinGroup(req *kmsg.JoinGroupRequest) *kmsg.JoinGroupResponse {
	resp := kmsg.NewJoinGroupResponse()
	resp.SetVersion(req.Version)
	resp.ErrorCode = errUnsupportedVersion
	resp.Generation = -1
	resp.MemberID = req.MemberID
	return &resp
}

func unsupportedHeartbeat(req *kmsg.HeartbeatRequest) *kmsg.HeartbeatResponse {
	resp := kmsg.NewHeartbeatResponse()
	resp.SetVersion(req.Version)
	resp.ErrorCode = errUnknownMemberID
	return &resp
}

func unsupportedLeaveGroup(req *kmsg.LeaveGroupRequest) *kmsg.LeaveGroupResponse {
	resp := kmsg.NewLeaveGroupResponse()
	resp.SetVersion(req.Version)
	resp.ErrorCode = errUnknownMemberID
	for _, member := range req.Members {
		out := kmsg.NewLeaveGroupResponseMember()
		out.MemberID = member.MemberID
		out.InstanceID = member.InstanceID
		out.ErrorCode = errUnknownMemberID
		resp.Members = append(resp.Members, out)
	}
	return &resp
}

func unsupportedSyncGroup(req *kmsg.SyncGroupRequest) *kmsg.SyncGroupResponse {
	resp := kmsg.NewSyncGroupResponse()
	resp.SetVersion(req.Version)
	resp.ErrorCode = errUnknownMemberID
	return &resp
}

func decodeRecordBatches(src []byte) ([]domain.ProducerRecord, int16, string) {
	var out []domain.ProducerRecord
	for len(src) > 0 {
		if len(src) < 12 {
			return nil, errCorruptMessage, "record batch header is truncated"
		}
		length := int32(binary.BigEndian.Uint32(src[8:12]))
		if length < 49 {
			return nil, errCorruptMessage, "record batch length is invalid"
		}
		total := 12 + int(length)
		if total > len(src) {
			return nil, errCorruptMessage, "record batch length exceeds produce payload"
		}
		wire := src[:total]
		if got, want := crc32.Checksum(wire[21:], crc32c), binary.BigEndian.Uint32(wire[17:21]); got != want {
			return nil, errCorruptMessage, "record batch CRC is invalid"
		}
		var batch kmsg.RecordBatch
		if err := batch.ReadFrom(wire); err != nil {
			return nil, errCorruptMessage, err.Error()
		}
		if batch.Magic != 2 {
			return nil, errCorruptMessage, "only Kafka record batch magic 2 is supported"
		}
		if batch.Attributes&0x07 != 0 {
			return nil, errUnsupportedCompressionType, "compressed record batches are outside the event-stream intersection"
		}
		if batch.Attributes&0x30 != 0 {
			return nil, errInvalidRequest, "transactional and control batches are outside the event-stream intersection"
		}
		records, code, msg := decodeRecords(batch)
		if code != 0 {
			return nil, code, msg
		}
		out = append(out, records...)
		src = src[total:]
	}
	return out, 0, ""
}

func decodeRecords(batch kmsg.RecordBatch) ([]domain.ProducerRecord, int16, string) {
	src := batch.Records
	out := make([]domain.ProducerRecord, 0, maxInt32(batch.NumRecords, 0))
	for i := int32(0); i < batch.NumRecords; i++ {
		length, n := readVarint(src)
		if n <= 0 || length < 0 {
			return nil, errCorruptMessage, "record length is invalid"
		}
		total := n + int(length)
		if total > len(src) {
			return nil, errCorruptMessage, "record length exceeds batch payload"
		}
		var rec kmsg.Record
		if err := rec.ReadFrom(src[:total]); err != nil {
			return nil, errCorruptMessage, err.Error()
		}
		headers := make([]domain.Header, 0, len(rec.Headers))
		for _, h := range rec.Headers {
			headers = append(headers, domain.Header{Key: h.Key, Value: copyBytes(h.Value)})
		}
		out = append(out, domain.ProducerRecord{
			Key:       copyBytes(rec.Key),
			Value:     copyBytes(rec.Value),
			Headers:   headers,
			Timestamp: time.UnixMilli(batch.FirstTimestamp + rec.TimestampDelta64).UTC(),
		})
		src = src[total:]
	}
	if len(src) != 0 {
		return nil, errCorruptMessage, "record batch has trailing bytes"
	}
	return out, 0, ""
}

func encodeRecordBatches(records []domain.Record) []byte {
	if len(records) == 0 {
		return nil
	}
	var out []byte
	for _, group := range contiguousRecordGroups(records) {
		out = append(out, encodeRecordBatch(group)...)
	}
	return out
}

func encodeRecordBatch(records []domain.Record) []byte {
	firstTS := records[0].Timestamp.UnixMilli()
	maxTS := firstTS
	var recordBytes []byte
	for i, rec := range records {
		ts := rec.Timestamp.UnixMilli()
		if ts > maxTS {
			maxTS = ts
		}
		headers := make([]kmsg.Header, 0, len(rec.Headers))
		for _, h := range rec.Headers {
			headers = append(headers, kmsg.Header{Key: h.Key, Value: copyBytes(h.Value)})
		}
		record := kmsg.Record{
			Attributes:       0,
			TimestampDelta64: ts - firstTS,
			OffsetDelta:      int32(i),
			Key:              copyBytes(rec.Key),
			Value:            copyBytes(rec.Value),
			Headers:          headers,
		}
		recordBytes = append(recordBytes, encodeRecord(record)...)
	}
	batch := kmsg.RecordBatch{
		FirstOffset:          records[0].Offset,
		Length:               int32(49 + len(recordBytes)),
		PartitionLeaderEpoch: 0,
		Magic:                2,
		Attributes:           0,
		LastOffsetDelta:      int32(len(records) - 1),
		FirstTimestamp:       firstTS,
		MaxTimestamp:         maxTS,
		ProducerID:           -1,
		ProducerEpoch:        -1,
		FirstSequence:        -1,
		NumRecords:           int32(len(records)),
		Records:              recordBytes,
	}
	wire := batch.AppendTo(nil)
	binary.BigEndian.PutUint32(wire[17:21], crc32.Checksum(wire[21:], crc32c))
	return wire
}

func encodeRecord(record kmsg.Record) []byte {
	for {
		wire := record.AppendTo(nil)
		prefixLen := len(appendVarint(nil, record.Length))
		bodyLen := int32(len(wire) - prefixLen)
		if record.Length == bodyLen {
			return wire
		}
		record.Length = bodyLen
	}
}

func contiguousRecordGroups(records []domain.Record) [][]domain.Record {
	var groups [][]domain.Record
	start := 0
	for i := 1; i < len(records); i++ {
		if records[i].Offset != records[i-1].Offset+1 {
			groups = append(groups, records[start:i])
			start = i
		}
	}
	return append(groups, records[start:])
}

func readVarint(src []byte) (int32, int) {
	var u uint32
	for shift := uint(0); shift <= 28; shift += 7 {
		if int(shift/7) >= len(src) {
			return 0, 0
		}
		b := src[shift/7]
		u |= uint32(b&0x7f) << shift
		if b&0x80 == 0 {
			return int32(u>>1) ^ -int32(u&1), int(shift/7) + 1
		}
	}
	return 0, -1
}

func appendVarint(dst []byte, i int32) []byte {
	u := uint32(i)<<1 ^ uint32(i>>31)
	for u >= 0x80 {
		dst = append(dst, byte(u)|0x80)
		u >>= 7
	}
	return append(dst, byte(u))
}

func produceHasError(resp *kmsg.ProduceResponse) bool {
	for _, topic := range resp.Topics {
		for _, part := range topic.Partitions {
			if part.ErrorCode != 0 {
				return true
			}
		}
	}
	return false
}

func producePartitionErrors(parts []kmsg.ProduceRequestTopicPartition, code int16, msg string) []kmsg.ProduceResponseTopicPartition {
	out := make([]kmsg.ProduceResponseTopicPartition, 0, len(parts))
	for _, part := range parts {
		p := kmsg.NewProduceResponseTopicPartition()
		p.Partition = part.Partition
		p.ErrorCode = code
		p.ErrorMessage = &msg
		out = append(out, p)
	}
	return out
}

func (s *Server) broker() kmsg.MetadataResponseBroker {
	return kmsg.MetadataResponseBroker{
		NodeID: s.cfg.BrokerID,
		Host:   s.cfg.Host,
		Port:   s.cfg.Port,
	}
}

func metadataUnknownTopic(name string, code int16) kmsg.MetadataResponseTopic {
	out := kmsg.NewMetadataResponseTopic()
	out.Topic = &name
	out.ErrorCode = code
	return out
}

func metadataUnknownTopicID(id [16]byte) kmsg.MetadataResponseTopic {
	out := kmsg.NewMetadataResponseTopic()
	out.TopicID = id
	out.ErrorCode = errUnknownTopicID
	return out
}

func topicName(name *string) string {
	if name == nil {
		return ""
	}
	return *name
}

func domainErrCode(err error) int16 {
	switch {
	case domain.IsNotFound(err):
		return errUnknownTopicOrPartition
	case domain.IsInvalidInput(err):
		return errInvalidRequest
	default:
		return errInvalidRequest
	}
}

func createTopicErrCode(err error) int16 {
	switch {
	case domain.IsAlreadyExists(err):
		return errTopicAlreadyExists
	case domain.IsInvalidInput(err):
		return errInvalidConfig
	default:
		return errInvalidRequest
	}
}

func fetchErrCode(err error) int16 {
	if domain.IsInvalidInput(err) {
		return errOffsetOutOfRange
	}
	return domainErrCode(err)
}

func commitErrCode(err error) int16 {
	if domain.IsInvalidInput(err) {
		return errInvalidRequest
	}
	return domainErrCode(err)
}

func fetchOffsetErrCode(err error) int16 {
	if domain.IsNotFound(err) {
		return errUnknownTopicOrPartition
	}
	if domain.IsInvalidInput(err) {
		return errInvalidRequest
	}
	return errUnknownTopicOrPartition
}

func groupOffsetRequestErr(group string, generation int32, memberID string) int16 {
	if group == "" {
		return errInvalidGroupID
	}
	if generation >= 0 || memberID != "" {
		return errUnknownMemberID
	}
	return 0
}

func copyBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func stringPtr(s string) *string {
	return &s
}

func minInt16(a, b int16) int16 {
	if a < b {
		return a
	}
	return b
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
