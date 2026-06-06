// Conformance: AWS MSK-shaped frontend exercised by the official
// aws-sdk-go-v2/service/kafka SDK for control plane, plus franz-go/kgo
// for the Kafka data plane returned by GetBootstrapBrokers.
package conformance_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/aws/smithy-go"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/eventstream/backends/inmem"
)

func newAWSMSKClient(t *testing.T, endpoint string) *kafka.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	return kafka.NewFromConfig(cfg, func(o *kafka.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
}

func awsMSKClusterARN(name string) string {
	return "arn:aws:kafka:us-east-1:000000000000:cluster/" + name + "/00000000-0000-0000-0000-000000000000-1"
}

func awsMSKTopicARN(clusterName, topic string) string {
	return "arn:aws:kafka:us-east-1:000000000000:topic/" + clusterName + "/00000000-0000-0000-0000-000000000000-1/" + topic
}

func TestAWSSDK_MSKTopicBacksKafkaClientProduceFetch(t *testing.T) {
	backend := inmem.New()
	clusterName := "cluster-a"
	clusterARN := awsMSKClusterARN(clusterName)
	kafkaSrv := harness.StartEventStreamKafkaServerCluster(t, backend, clusterARN)
	restSrv := harness.StartEventStreamServerAWS(t, backend, []string{kafkaSrv.Address})
	cli := newAWSMSKClient(t, restSrv.URL)
	ctx := context.Background()

	createCluster, err := cli.CreateCluster(ctx, &kafka.CreateClusterInput{
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-123"},
			InstanceType:  awsapi.String("kafka.t3.small"),
		},
		ClusterName:         awsapi.String(clusterName),
		KafkaVersion:        awsapi.String("3.6.0"),
		NumberOfBrokerNodes: awsapi.Int32(1),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	if awsapi.ToString(createCluster.ClusterArn) != clusterARN {
		t.Fatalf("CreateCluster ARN = %q, want %q", awsapi.ToString(createCluster.ClusterArn), clusterARN)
	}
	if createCluster.State != kafkatypes.ClusterStateActive {
		t.Fatalf("CreateCluster state = %v, want ACTIVE", createCluster.State)
	}

	describeCluster, err := cli.DescribeCluster(ctx, &kafka.DescribeClusterInput{ClusterArn: awsapi.String(clusterARN)})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}
	if describeCluster.ClusterInfo == nil || awsapi.ToString(describeCluster.ClusterInfo.ClusterName) != clusterName {
		t.Fatalf("DescribeCluster = %+v, want %q", describeCluster.ClusterInfo, clusterName)
	}

	listClusters, err := cli.ListClusters(ctx, &kafka.ListClustersInput{})
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}
	if len(listClusters.ClusterInfoList) != 1 || awsapi.ToString(listClusters.ClusterInfoList[0].ClusterArn) != clusterARN {
		t.Fatalf("ListClusters = %+v, want %q", listClusters.ClusterInfoList, clusterARN)
	}

	bootstrap, err := cli.GetBootstrapBrokers(ctx, &kafka.GetBootstrapBrokersInput{ClusterArn: awsapi.String(clusterARN)})
	if err != nil {
		t.Fatalf("GetBootstrapBrokers: %v", err)
	}
	brokers := strings.Split(awsapi.ToString(bootstrap.BootstrapBrokerString), ",")
	if len(brokers) != 1 || brokers[0] != kafkaSrv.Address {
		t.Fatalf("GetBootstrapBrokers = %q, want %q", awsapi.ToString(bootstrap.BootstrapBrokerString), kafkaSrv.Address)
	}

	topic := "orders"
	topicARN := awsMSKTopicARN(clusterName, topic)
	createTopic, err := cli.CreateTopic(ctx, &kafka.CreateTopicInput{
		ClusterArn:        awsapi.String(clusterARN),
		PartitionCount:    awsapi.Int32(1),
		ReplicationFactor: awsapi.Int32(1),
		TopicName:         awsapi.String(topic),
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	if awsapi.ToString(createTopic.TopicName) != topic || createTopic.Status != kafkatypes.TopicStateActive {
		t.Fatalf("CreateTopic = %+v, want topic %q ACTIVE", createTopic, topic)
	}
	if awsapi.ToString(createTopic.TopicArn) != topicARN {
		t.Fatalf("CreateTopic TopicArn = %q, want %q", awsapi.ToString(createTopic.TopicArn), topicARN)
	}

	describeTopic, err := cli.DescribeTopic(ctx, &kafka.DescribeTopicInput{
		ClusterArn: awsapi.String(clusterARN),
		TopicName:  awsapi.String(topic),
	})
	if err != nil {
		t.Fatalf("DescribeTopic: %v", err)
	}
	if awsapi.ToString(describeTopic.TopicName) != topic || awsapi.ToInt32(describeTopic.PartitionCount) != 1 {
		t.Fatalf("DescribeTopic = %+v, want topic %q partitions=1", describeTopic, topic)
	}
	if awsapi.ToString(describeTopic.TopicArn) != topicARN {
		t.Fatalf("DescribeTopic TopicArn = %q, want %q", awsapi.ToString(describeTopic.TopicArn), topicARN)
	}

	listTopics, err := cli.ListTopics(ctx, &kafka.ListTopicsInput{ClusterArn: awsapi.String(clusterARN)})
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}
	if len(listTopics.Topics) != 1 || awsapi.ToString(listTopics.Topics[0].TopicName) != topic {
		t.Fatalf("ListTopics = %+v, want %q", listTopics.Topics, topic)
	}
	if awsapi.ToString(listTopics.Topics[0].TopicArn) != topicARN {
		t.Fatalf("ListTopics TopicArn = %q, want %q", awsapi.ToString(listTopics.Topics[0].TopicArn), topicARN)
	}

	kclient, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
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
	t.Cleanup(kclient.Close)

	produceCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := kclient.ProduceSync(produceCtx, &kgo.Record{
		Partition: 0,
		Key:       []byte("order-1"),
		Value:     []byte("created"),
	}).FirstErr(); err != nil {
		t.Fatalf("kgo produce: %v", err)
	}

	fetchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		fetches := kclient.PollFetches(fetchCtx)
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
			goto fetched
		}
	}

fetched:
	deleteTopic, err := cli.DeleteTopic(ctx, &kafka.DeleteTopicInput{
		ClusterArn: awsapi.String(clusterARN),
		TopicName:  awsapi.String(topic),
	})
	if err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	if awsapi.ToString(deleteTopic.TopicArn) != topicARN {
		t.Fatalf("DeleteTopic TopicArn = %q, want %q", awsapi.ToString(deleteTopic.TopicArn), topicARN)
	}
	if _, err := cli.DescribeTopic(ctx, &kafka.DescribeTopicInput{
		ClusterArn: awsapi.String(clusterARN),
		TopicName:  awsapi.String(topic),
	}); err == nil {
		t.Fatal("DescribeTopic after delete: got nil, want not found")
	}
}

func TestAWSSDK_MSKRejectsOutOfIntersectionReplication(t *testing.T) {
	backend := inmem.New()
	clusterName := "cluster-a"
	clusterARN := awsMSKClusterARN(clusterName)
	kafkaSrv := harness.StartEventStreamKafkaServerCluster(t, backend, clusterARN)
	restSrv := harness.StartEventStreamServerAWS(t, backend, []string{kafkaSrv.Address})
	cli := newAWSMSKClient(t, restSrv.URL)
	ctx := context.Background()

	if _, err := cli.CreateCluster(ctx, &kafka.CreateClusterInput{
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-123"},
			InstanceType:  awsapi.String("kafka.t3.small"),
		},
		ClusterName:         awsapi.String(clusterName),
		KafkaVersion:        awsapi.String("3.6.0"),
		NumberOfBrokerNodes: awsapi.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	_, err := cli.CreateTopic(ctx, &kafka.CreateTopicInput{
		ClusterArn:        awsapi.String(clusterARN),
		PartitionCount:    awsapi.Int32(1),
		ReplicationFactor: awsapi.Int32(3),
		TopicName:         awsapi.String("replicated"),
	})
	if err == nil {
		t.Fatal("CreateTopic replicationFactor=3: got nil, want error")
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "BadRequestException" {
		t.Fatalf("CreateTopic replicationFactor=3 error = %v, want BadRequestException", err)
	}
}
