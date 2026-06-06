// Package aws_msk is the AWS MSK-shaped control-plane frontend for
// shimanism's eventstream service. The Kafka data plane is served by
// internal/eventstream/kafkaserver; GetBootstrapBrokers returns the
// configured real data-plane listener.
package aws_msk

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/shimanism/internal/awsjson"
	"github.com/e6qu/shimanism/internal/eventstream/domain"
	"github.com/e6qu/shimanism/internal/restxml"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
	gen "github.com/e6qu/shimanism/services/eventstream/gen"
)

const (
	region    = "us-east-1"
	accountID = "000000000000"
)

// Options configures the AWS MSK frontend.
type Options struct {
	BootstrapBrokers []string
}

// New returns the generated restJson1 MSK control-plane frontend.
func New(s domain.Streams, opt Options) http.Handler {
	router := &restxml.Router{}
	gen.RegisterKafkaRoutes(router, &Adapter{s: s, bootstrapBrokers: append([]string(nil), opt.BootstrapBrokers...)})
	verifier := sigv4verifier.New(sigv4verifier.StaticStore{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, sigv4verifier.Options{Service: "kafka", Region: region})
	return sigv4verifier.Middleware(verifier, awsjson.WriteError)(router)
}

// Adapter binds generated MSK operations to the eventstream domain.
type Adapter struct {
	s                domain.Streams
	bootstrapBrokers []string
}

func strp(s string) *string { return &s }
func i32p(v int32) *int32   { return &v }

func clusterARN(name string) string {
	return "arn:aws:kafka:" + region + ":" + accountID + ":cluster/" + name + "/00000000-0000-0000-0000-000000000000-1"
}

func topicARN(clusterID, topic string) (string, error) {
	prefix, rest, ok := strings.Cut(clusterID, ":cluster/")
	if !ok || prefix == "" || rest == "" {
		return "", badRequest("clusterArn must be an AWS MSK cluster ARN")
	}
	clusterName, clusterUUID, ok := strings.Cut(rest, "/")
	if !ok || clusterName == "" || clusterUUID == "" {
		return "", badRequest("clusterArn must include cluster name and UUID")
	}
	return prefix + ":topic/" + clusterName + "/" + clusterUUID + "/" + topic, nil
}

func activeClusterState() *gen.ClusterState {
	v := gen.ClusterStateACTIVE
	return &v
}

func activeTopicState() *gen.TopicState {
	v := gen.TopicStateACTIVE
	return &v
}

func clusterToAWS(c domain.Cluster) *gen.ClusterInfo {
	return &gen.ClusterInfo{
		ClusterArn:          strp(c.ID),
		ClusterName:         strp(c.Name),
		CreationTime:        &c.CreatedAt,
		NumberOfBrokerNodes: i32p(int32(c.BrokerCount)),
		State:               activeClusterState(),
	}
}

func topicToAWS(clusterID string, t domain.Topic) (*gen.TopicInfo, error) {
	arn, err := topicARN(clusterID, t.Name)
	if err != nil {
		return nil, err
	}
	return &gen.TopicInfo{
		TopicArn:          strp(arn),
		TopicName:         strp(t.Name),
		PartitionCount:    i32p(int32(t.PartitionCount)),
		ReplicationFactor: i32p(1),
	}, nil
}

func (a *Adapter) CreateCluster(ctx context.Context, in *gen.CreateClusterRequest) (*gen.CreateClusterResponse, error) {
	if in.ClusterName == "" {
		return nil, badRequest("clusterName is required")
	}
	if in.NumberOfBrokerNodes <= 0 {
		return nil, badRequest("numberOfBrokerNodes must be positive")
	}
	if in.BrokerNodeGroupInfo == nil {
		return nil, badRequest("brokerNodeGroupInfo is required")
	}
	if in.KafkaVersion == "" {
		return nil, badRequest("kafkaVersion is required")
	}
	if err := rejectOutOfIntersectionClusterOptions(in); err != nil {
		return nil, err
	}
	arn := clusterARN(in.ClusterName)
	cluster, err := a.s.CreateCluster(ctx, arn, in.ClusterName, domain.CreateClusterOptions{
		BrokerCount:      int(in.NumberOfBrokerNodes),
		BootstrapBrokers: a.bootstrapBrokers,
		Tags:             map[string]string(in.Tags),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &gen.CreateClusterResponse{
		ClusterArn:  strp(cluster.ID),
		ClusterName: strp(cluster.Name),
		State:       activeClusterState(),
	}, nil
}

func (a *Adapter) DescribeCluster(ctx context.Context, in *gen.DescribeClusterRequest) (*gen.DescribeClusterResponse, error) {
	cluster, err := a.s.DescribeCluster(ctx, in.ClusterArn)
	if err != nil {
		return nil, mapErr(err)
	}
	return &gen.DescribeClusterResponse{ClusterInfo: clusterToAWS(cluster)}, nil
}

func (a *Adapter) ListClusters(ctx context.Context, in *gen.ListClustersRequest) (*gen.ListClustersResponse, error) {
	maxResults := int32Value(in.MaxResults)
	res, err := a.s.ListClusters(ctx, domain.ListClustersOptions{
		NameFilter: stringValue(in.ClusterNameFilter),
		MaxResults: int(maxResults),
		NextToken:  stringValue(in.NextToken),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := &gen.ListClustersResponse{NextToken: emptyToNil(res.NextToken)}
	for _, cluster := range res.Clusters {
		out.ClusterInfoList = append(out.ClusterInfoList, *clusterToAWS(cluster))
	}
	return out, nil
}

func (a *Adapter) GetBootstrapBrokers(ctx context.Context, in *gen.GetBootstrapBrokersRequest) (*gen.GetBootstrapBrokersResponse, error) {
	cluster, err := a.s.DescribeCluster(ctx, in.ClusterArn)
	if err != nil {
		return nil, mapErr(err)
	}
	if len(cluster.BootstrapBrokers) == 0 {
		return nil, badRequest("cluster has no configured bootstrap brokers")
	}
	return &gen.GetBootstrapBrokersResponse{BootstrapBrokerString: strp(strings.Join(cluster.BootstrapBrokers, ","))}, nil
}

func (a *Adapter) ListNodes(ctx context.Context, in *gen.ListNodesRequest) (*gen.ListNodesResponse, error) {
	cluster, err := a.s.DescribeCluster(ctx, in.ClusterArn)
	if err != nil {
		return nil, mapErr(err)
	}
	out := &gen.ListNodesResponse{}
	for i := 1; i <= cluster.BrokerCount; i++ {
		brokerID := float64(i)
		nodeType := gen.NodeTypeBROKER
		nodeARN := in.ClusterArn + "/broker/" + strconv.Itoa(i)
		out.NodeInfoList = append(out.NodeInfoList, gen.NodeInfo{
			BrokerNodeInfo: &gen.BrokerNodeInfo{BrokerId: &brokerID},
			NodeARN:        &nodeARN,
			NodeType:       &nodeType,
		})
	}
	return out, nil
}

func (a *Adapter) CreateTopic(ctx context.Context, in *gen.CreateTopicRequest) (*gen.CreateTopicResponse, error) {
	if _, err := a.s.DescribeCluster(ctx, in.ClusterArn); err != nil {
		return nil, mapErr(err)
	}
	if in.TopicName == "" {
		return nil, badRequest("topicName is required")
	}
	if in.PartitionCount <= 0 {
		return nil, badRequest("partitionCount must be positive")
	}
	if in.ReplicationFactor != 1 {
		return nil, badRequest("replicationFactor is not portable in the eventstream intersection")
	}
	if in.Configs != nil && *in.Configs != "" {
		return nil, badRequest("topic configs are not implemented for AWS MSK in this slice")
	}
	topic, err := a.s.CreateTopic(ctx, in.ClusterArn, in.TopicName, domain.CreateTopicOptions{PartitionCount: int(in.PartitionCount)})
	if err != nil {
		return nil, mapErr(err)
	}
	arn, err := topicARN(in.ClusterArn, topic.Name)
	if err != nil {
		return nil, err
	}
	return &gen.CreateTopicResponse{
		Status:    activeTopicState(),
		TopicArn:  strp(arn),
		TopicName: strp(topic.Name),
	}, nil
}

func (a *Adapter) DescribeTopic(ctx context.Context, in *gen.DescribeTopicRequest) (*gen.DescribeTopicResponse, error) {
	topic, err := a.s.DescribeTopic(ctx, in.ClusterArn, in.TopicName)
	if err != nil {
		return nil, mapErr(err)
	}
	arn, err := topicARN(in.ClusterArn, topic.Name)
	if err != nil {
		return nil, err
	}
	return &gen.DescribeTopicResponse{
		Status:            activeTopicState(),
		TopicArn:          strp(arn),
		TopicName:         strp(topic.Name),
		PartitionCount:    i32p(int32(topic.PartitionCount)),
		ReplicationFactor: i32p(1),
	}, nil
}

func (a *Adapter) ListTopics(ctx context.Context, in *gen.ListTopicsRequest) (*gen.ListTopicsResponse, error) {
	if _, err := a.s.DescribeCluster(ctx, in.ClusterArn); err != nil {
		return nil, mapErr(err)
	}
	res, err := a.s.ListTopics(ctx, domain.ListTopicsOptions{
		ClusterID:  in.ClusterArn,
		Prefix:     stringValue(in.TopicNameFilter),
		MaxResults: int(int32Value(in.MaxResults)),
		NextToken:  stringValue(in.NextToken),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := &gen.ListTopicsResponse{NextToken: emptyToNil(res.NextToken)}
	for _, topic := range res.Topics {
		awsTopic, err := topicToAWS(in.ClusterArn, topic)
		if err != nil {
			return nil, err
		}
		out.Topics = append(out.Topics, *awsTopic)
	}
	return out, nil
}

func (a *Adapter) DeleteTopic(ctx context.Context, in *gen.DeleteTopicRequest) (*gen.DeleteTopicResponse, error) {
	if err := a.s.DeleteTopic(ctx, in.ClusterArn, in.TopicName); err != nil {
		return nil, mapErr(err)
	}
	arn, err := topicARN(in.ClusterArn, in.TopicName)
	if err != nil {
		return nil, err
	}
	return &gen.DeleteTopicResponse{
		Status:    activeTopicState(),
		TopicArn:  strp(arn),
		TopicName: strp(in.TopicName),
	}, nil
}

func rejectOutOfIntersectionClusterOptions(in *gen.CreateClusterRequest) error {
	if in.ClientAuthentication != nil {
		return badRequest("clientAuthentication is out of the eventstream intersection")
	}
	if in.ConfigurationInfo != nil {
		return badRequest("configurationInfo is out of the eventstream intersection")
	}
	if in.EncryptionInfo != nil {
		return badRequest("encryptionInfo is out of the eventstream intersection")
	}
	if in.EnhancedMonitoring != nil {
		return badRequest("enhancedMonitoring is out of the eventstream intersection")
	}
	if in.LoggingInfo != nil {
		return badRequest("loggingInfo is out of the eventstream intersection")
	}
	if in.OpenMonitoring != nil {
		return badRequest("openMonitoring is out of the eventstream intersection")
	}
	if in.Rebalancing != nil {
		return badRequest("rebalancing is out of the eventstream intersection")
	}
	if in.StorageMode != nil {
		return badRequest("storageMode is out of the eventstream intersection")
	}
	return nil
}

func mapErr(err error) error {
	switch {
	case domain.IsNotFound(err):
		return &awsjson.BackendError{HTTPStatus: http.StatusNotFound, Type: "NotFoundException", Message: err.Error()}
	case domain.IsAlreadyExists(err):
		return &awsjson.BackendError{HTTPStatus: http.StatusConflict, Type: "ConflictException", Message: err.Error()}
	case domain.IsInvalidInput(err):
		return badRequest(err.Error())
	default:
		return &awsjson.BackendError{HTTPStatus: http.StatusInternalServerError, Type: "InternalServerErrorException", Message: err.Error()}
	}
}

func badRequest(message string) error {
	return &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "BadRequestException", Message: message}
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func int32Value(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func emptyToNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
