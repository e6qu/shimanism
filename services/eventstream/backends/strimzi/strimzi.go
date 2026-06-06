// Package strimzi is the Strimzi Kafka Operator K8s peer for shimanism's
// eventstream service (Phase 20.D).
//
// K8s primitives used:
//   - Kafka CRD (kafka.strimzi.io/v1beta2) → domain.Cluster
//   - KafkaTopic CRD (kafka.strimzi.io/v1beta2) → domain.Topic
//
// Architecture note: the Kafka data-plane (Produce, Fetch, etc.) bypasses
// the shim entirely — clients connect directly to the Strimzi bootstrap
// address. The data-plane methods on this backend return ErrDataPlane so
// callers can surface the right cloud error ("not routed through shim").
// The only data-plane method that is implemented is ListOffsets, which
// derives the bootstrap address from the Kafka CR status so callers can
// hand it to the real Kafka client.
//
// Stateless: every Describe/List re-reads the K8s API; no per-request
// state is stored in the shim.
package strimzi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/e6qu/shimanism/internal/eventstream/domain"
)

// ErrDataPlane is returned for Kafka data-plane operations that must bypass
// the shim and go directly to the Strimzi bootstrap address.
var ErrDataPlane = errors.New("data-plane operations are not routed through the shim for Strimzi; clients connect directly to the Strimzi bootstrap address")

var (
	kafkaGVR = schema.GroupVersionResource{
		Group:    "kafka.strimzi.io",
		Version:  "v1beta2",
		Resource: "kafkas",
	}
	topicGVR = schema.GroupVersionResource{
		Group:    "kafka.strimzi.io",
		Version:  "v1beta2",
		Resource: "kafkatopics",
	}
)

// Config configures the Strimzi backend.
type Config struct {
	// Namespace is the K8s namespace where Strimzi CRs live.
	// Defaults to "default".
	Namespace string
	// KafkaReplicas is the default broker replica count for new Kafka CRs.
	// Defaults to 1.
	KafkaReplicas int
	// ZookeeperReplicas is the default ZooKeeper replica count.
	// Defaults to 1.
	ZookeeperReplicas int
}

// Backend implements domain.Streams via the Strimzi Kafka Operator CRDs.
type Backend struct {
	dyn               dynamic.Interface
	namespace         string
	kafkaReplicas     int
	zookeeperReplicas int
}

// New returns a Backend bound to the given dynamic Kubernetes client.
func New(dyn dynamic.Interface, cfg Config) *Backend {
	ns := cfg.Namespace
	if ns == "" {
		ns = "default"
	}
	kr := cfg.KafkaReplicas
	if kr <= 0 {
		kr = 1
	}
	zr := cfg.ZookeeperReplicas
	if zr <= 0 {
		zr = 1
	}
	return &Backend{dyn: dyn, namespace: ns, kafkaReplicas: kr, zookeeperReplicas: zr}
}

var _ domain.Streams = (*Backend)(nil)

// ─── Cluster (Kafka CR) ───────────────────────────────────────────────────

func (b *Backend) CreateCluster(ctx context.Context, id, name string, opt domain.CreateClusterOptions) (domain.Cluster, error) {
	replicas := b.kafkaReplicas
	if opt.BrokerCount > 0 {
		replicas = opt.BrokerCount
	}
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "kafka.strimzi.io/v1beta2",
			"kind":       "Kafka",
			"metadata": map[string]any{
				"name":      name,
				"namespace": b.namespace,
				"labels":    map[string]any{"shimanism.io/id": id},
			},
			"spec": map[string]any{
				"kafka": map[string]any{
					"replicas": int64(replicas),
					"listeners": []any{
						map[string]any{
							"name": "plain",
							"port": int64(9092),
							"type": "internal",
							"tls":  false,
						},
					},
					"storage": map[string]any{"type": "ephemeral"},
				},
				"zookeeper": map[string]any{
					"replicas": int64(b.zookeeperReplicas),
					"storage":  map[string]any{"type": "ephemeral"},
				},
				"entityOperator": map[string]any{
					"topicOperator": map[string]any{},
				},
			},
		},
	}
	created, err := b.dyn.Resource(kafkaGVR).Namespace(b.namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.Cluster{}, domain.ClusterAlreadyExists(name)
		}
		return domain.Cluster{}, fmt.Errorf("create Kafka CR %q: %w", name, err)
	}
	return kafkaCRToCluster(id, created), nil
}

func (b *Backend) DescribeCluster(ctx context.Context, id string) (domain.Cluster, error) {
	name := clusterNameFromID(id)
	obj, err := b.dyn.Resource(kafkaGVR).Namespace(b.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.Cluster{}, domain.ClusterNotFound(id)
		}
		return domain.Cluster{}, fmt.Errorf("get Kafka CR %q: %w", name, err)
	}
	return kafkaCRToCluster(id, obj), nil
}

func (b *Backend) DeleteCluster(ctx context.Context, id string) error {
	name := clusterNameFromID(id)
	err := b.dyn.Resource(kafkaGVR).Namespace(b.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.ClusterNotFound(id)
		}
		return fmt.Errorf("delete Kafka CR %q: %w", name, err)
	}
	return nil
}

func (b *Backend) ListClusters(ctx context.Context, opt domain.ListClustersOptions) (domain.ListClustersResult, error) {
	list, err := b.dyn.Resource(kafkaGVR).Namespace(b.namespace).List(ctx, metav1.ListOptions{
		Limit:    int64(opt.MaxResults),
		Continue: opt.NextToken,
	})
	if err != nil {
		return domain.ListClustersResult{}, fmt.Errorf("list Kafka CRs: %w", err)
	}
	var out domain.ListClustersResult
	out.NextToken = list.GetContinue()
	for i := range list.Items {
		obj := &list.Items[i]
		labels := obj.GetLabels()
		clusterID := labels["shimanism.io/id"]
		if clusterID == "" {
			clusterID = obj.GetName()
		}
		if opt.NameFilter != "" && !strings.Contains(obj.GetName(), opt.NameFilter) {
			continue
		}
		out.Clusters = append(out.Clusters, kafkaCRToCluster(clusterID, obj))
	}
	return out, nil
}

// ─── Topic (KafkaTopic CR) ────────────────────────────────────────────────

func (b *Backend) CreateTopic(ctx context.Context, clusterID, name string, opt domain.CreateTopicOptions) (domain.Topic, error) {
	clusterName := clusterNameFromID(clusterID)
	partitions := opt.PartitionCount
	if partitions <= 0 {
		partitions = 1
	}
	spec := map[string]any{
		"partitions": int64(partitions),
		"replicas":   int64(1),
	}
	if opt.Retention > 0 {
		spec["config"] = map[string]any{
			"retention.ms": fmt.Sprintf("%d", opt.Retention.Milliseconds()),
		}
	}
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "kafka.strimzi.io/v1beta2",
			"kind":       "KafkaTopic",
			"metadata": map[string]any{
				"name":      name,
				"namespace": b.namespace,
				"labels": map[string]any{
					"strimzi.io/cluster":   clusterName,
					"shimanism.io/cluster": clusterID,
				},
			},
			"spec": spec,
		},
	}
	created, err := b.dyn.Resource(topicGVR).Namespace(b.namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.Topic{}, domain.TopicAlreadyExists(name)
		}
		return domain.Topic{}, fmt.Errorf("create KafkaTopic CR %q: %w", name, err)
	}
	return topicCRToTopic(clusterID, created), nil
}

func (b *Backend) DescribeTopic(ctx context.Context, clusterID, name string) (domain.Topic, error) {
	obj, err := b.dyn.Resource(topicGVR).Namespace(b.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.Topic{}, domain.TopicNotFound(name)
		}
		return domain.Topic{}, fmt.Errorf("get KafkaTopic CR %q: %w", name, err)
	}
	return topicCRToTopic(clusterID, obj), nil
}

func (b *Backend) DeleteTopic(ctx context.Context, clusterID, name string) error {
	err := b.dyn.Resource(topicGVR).Namespace(b.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.TopicNotFound(name)
		}
		return fmt.Errorf("delete KafkaTopic CR %q: %w", name, err)
	}
	return nil
}

func (b *Backend) ListTopics(ctx context.Context, opt domain.ListTopicsOptions) (domain.ListTopicsResult, error) {
	clusterName := clusterNameFromID(opt.ClusterID)
	list, err := b.dyn.Resource(topicGVR).Namespace(b.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "strimzi.io/cluster=" + clusterName,
		Limit:         int64(opt.MaxResults),
		Continue:      opt.NextToken,
	})
	if err != nil {
		return domain.ListTopicsResult{}, fmt.Errorf("list KafkaTopic CRs: %w", err)
	}
	var out domain.ListTopicsResult
	out.NextToken = list.GetContinue()
	for i := range list.Items {
		obj := &list.Items[i]
		if opt.Prefix != "" && !strings.HasPrefix(obj.GetName(), opt.Prefix) {
			continue
		}
		out.Topics = append(out.Topics, topicCRToTopic(opt.ClusterID, obj))
	}
	return out, nil
}

// ─── Data-plane (not routed through shim) ────────────────────────────────

func (b *Backend) Produce(_ context.Context, _, _ string, _ int, _ []domain.ProducerRecord) ([]domain.RecordMetadata, error) {
	return nil, ErrDataPlane
}

func (b *Backend) Fetch(_ context.Context, _, _ string, _ int, _ int64, _ int) ([]domain.Record, error) {
	return nil, ErrDataPlane
}

func (b *Backend) ListOffsets(_ context.Context, _, _ string, _ int) (domain.OffsetBounds, error) {
	return domain.OffsetBounds{}, ErrDataPlane
}

func (b *Backend) CommitOffset(_ context.Context, _, _, _ string, _ int, _ int64) error {
	return ErrDataPlane
}

func (b *Backend) FetchCommittedOffset(_ context.Context, _, _, _ string, _ int) (int64, error) {
	return 0, ErrDataPlane
}

// ─── Helpers ──────────────────────────────────────────────────────────────

// clusterNameFromID extracts the Kafka CR name from an opaque cluster ID.
// IDs may be the short name, a full ARN, or a GCP resource path; we use
// the last path/slash segment in all cases so the CR name is always
// a valid K8s name.
func clusterNameFromID(id string) string {
	parts := strings.Split(strings.TrimRight(id, "/"), "/")
	return parts[len(parts)-1]
}

func kafkaCRToCluster(id string, obj *unstructured.Unstructured) domain.Cluster {
	name := obj.GetName()
	brokers := bootstrapFromStatus(obj, name)
	brokerCount := 1
	if spec, ok := obj.Object["spec"].(map[string]any); ok {
		if kafka, ok := spec["kafka"].(map[string]any); ok {
			if r, ok := kafka["replicas"].(int64); ok {
				brokerCount = int(r)
			}
		}
	}
	var createdAt time.Time
	if ts := obj.GetCreationTimestamp(); !ts.IsZero() {
		createdAt = ts.Time
	}
	return domain.Cluster{
		ID:               id,
		Name:             name,
		BrokerCount:      brokerCount,
		BootstrapBrokers: brokers,
		CreatedAt:        createdAt,
	}
}

func bootstrapFromStatus(obj *unstructured.Unstructured, clusterName string) []string {
	status, ok := obj.Object["status"].(map[string]any)
	if !ok {
		return []string{clusterName + "-kafka-bootstrap:9092"}
	}
	listeners, ok := status["listeners"].([]any)
	if !ok || len(listeners) == 0 {
		return []string{clusterName + "-kafka-bootstrap:9092"}
	}
	for _, l := range listeners {
		lm, ok := l.(map[string]any)
		if !ok {
			continue
		}
		bs, ok := lm["bootstrapServers"].(string)
		if ok && bs != "" {
			return []string{bs}
		}
	}
	return []string{clusterName + "-kafka-bootstrap:9092"}
}

func topicCRToTopic(clusterID string, obj *unstructured.Unstructured) domain.Topic {
	name := obj.GetName()
	var partitionCount int
	var retention time.Duration
	if spec, ok := obj.Object["spec"].(map[string]any); ok {
		if p, ok := spec["partitions"].(int64); ok {
			partitionCount = int(p)
		}
		if cfg, ok := spec["config"].(map[string]any); ok {
			if ms, ok := cfg["retention.ms"].(string); ok {
				var ms64 int64
				fmt.Sscanf(ms, "%d", &ms64)
				if ms64 > 0 {
					retention = time.Duration(ms64) * time.Millisecond
				}
			}
		}
	}
	var createdAt time.Time
	if ts := obj.GetCreationTimestamp(); !ts.IsZero() {
		createdAt = ts.Time
	}
	return domain.Topic{
		ClusterID:      clusterID,
		Name:           name,
		PartitionCount: partitionCount,
		Retention:      retention,
		CreatedAt:      createdAt,
	}
}
