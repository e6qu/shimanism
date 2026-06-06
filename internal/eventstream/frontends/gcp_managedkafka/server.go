// Package gcp_managedkafka is the GCP Managed Service for Apache Kafka
// REST/JSON control-plane frontend for shimanism's eventstream service.
package gcp_managedkafka

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/eventstream/domain"

	"google.golang.org/api/googleapi"
	managedkafkaraw "google.golang.org/api/managedkafka/v1"
)

const retentionConfigKey = "retention.ms"

type Server struct {
	s domain.Streams
}

func New(s domain.Streams) *Server { return &Server{s: s} }

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	rest, ok := strings.CutPrefix(path, "/v1/")
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "no GCP Managed Kafka route matches "+r.Method+" "+path)
		return
	}
	segs := strings.Split(rest, "/")
	// Minimum: projects/{project}/locations/{location}/{resource}
	if len(segs) < 5 || segs[0] != "projects" || segs[2] != "locations" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "no GCP Managed Kafka route matches "+r.Method+" "+path)
		return
	}
	project, location := segs[1], segs[3]

	switch segs[4] {
	case "operations":
		// GET /v1/projects/{project}/locations/{location}/operations/{opId}
		if r.Method == http.MethodGet && len(segs) == 6 {
			srv.getOperation(w, r, project, location, segs[5])
		} else {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "no GCP Managed Kafka route matches "+r.Method+" "+path)
		}
	case "clusters":
		srv.routeCluster(w, r, path, project, location, segs[5:])
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "no GCP Managed Kafka route matches "+r.Method+" "+path)
	}
}

func (srv *Server) routeCluster(w http.ResponseWriter, r *http.Request, path, project, location string, rest []string) {
	// rest = segs[5:], i.e. everything after "clusters"
	// len(rest)==0 → /clusters  (list or create)
	// len(rest)==1 → /clusters/{cluster}  (get or delete)
	// len(rest)==2 → /clusters/{cluster}/topics  (list or create topic)
	// len(rest)==3 → /clusters/{cluster}/topics/{topic}  (get or delete topic)
	parentBase := fmt.Sprintf("projects/%s/locations/%s", project, location)
	switch len(rest) {
	case 0, 1:
		if len(rest) == 0 || rest[0] == "" {
			// /v1/projects/{project}/locations/{location}/clusters
			switch r.Method {
			case http.MethodGet:
				srv.listClusters(w, r, project, location)
			case http.MethodPost:
				srv.createCluster(w, r, project, location, parentBase)
			default:
				writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", r.Method+" not allowed on clusters")
			}
			return
		}
		// /v1/projects/{project}/locations/{location}/clusters/{cluster}
		cluster := rest[0]
		switch r.Method {
		case http.MethodGet:
			srv.getCluster(w, r, project, location, cluster)
		case http.MethodDelete:
			srv.deleteCluster(w, r, project, location, cluster)
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", r.Method+" not allowed on cluster")
		}
	case 2, 3:
		if len(rest) < 2 || rest[1] != "topics" {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "no GCP Managed Kafka route matches "+r.Method+" "+path)
			return
		}
		cluster := rest[0]
		parent := parentBase + "/clusters/" + cluster
		if len(rest) == 2 || (len(rest) == 3 && rest[2] == "") {
			// /clusters/{cluster}/topics
			switch r.Method {
			case http.MethodGet:
				srv.listTopics(w, r, parent, cluster)
			case http.MethodPost:
				srv.createTopic(w, r, parent, cluster)
			default:
				writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", r.Method+" not allowed on topics")
			}
			return
		}
		// /clusters/{cluster}/topics/{topic}
		topic := rest[2]
		fullName := parent + "/topics/" + topic
		switch r.Method {
		case http.MethodGet:
			srv.getTopic(w, r, fullName, cluster, topic)
		case http.MethodDelete:
			srv.deleteTopic(w, r, cluster, topic)
		case http.MethodPatch:
			writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "topic patch is out of the eventstream intersection")
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", r.Method+" not allowed on topic")
		}
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "no GCP Managed Kafka route matches "+r.Method+" "+path)
	}
}

func (srv *Server) createCluster(w http.ResponseWriter, r *http.Request, project, location, parentBase string) {
	clusterID := r.URL.Query().Get("clusterId")
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "clusterId is required")
		return
	}
	var body managedkafkaraw.Cluster
	if !decodeJSON(w, r, &body) {
		return
	}
	brokerCount := 1
	if body.CapacityConfig != nil && body.CapacityConfig.VcpuCount > 0 {
		brokerCount = int(body.CapacityConfig.VcpuCount / 3)
		if brokerCount < 1 {
			brokerCount = 1
		}
	}
	// Use clusterID (short name) as the domain key — same convention as topic ops.
	cluster, err := srv.s.CreateCluster(r.Context(), clusterID, clusterID, domain.CreateClusterOptions{
		BrokerCount: brokerCount,
	})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	op := terminalOperation(
		fmt.Sprintf("%s/operations/create-%s", parentBase, clusterID),
		clusterToGCP(project, location, clusterID, cluster),
	)
	writeJSON(w, http.StatusOK, op)
}

func (srv *Server) getCluster(w http.ResponseWriter, r *http.Request, project, location, clusterID string) {
	cluster, err := srv.s.DescribeCluster(r.Context(), clusterID)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, clusterToGCP(project, location, clusterID, cluster))
}

func (srv *Server) listClusters(w http.ResponseWriter, r *http.Request, project, location string) {
	pageSize, ok := parsePageSize(w, r.URL.Query().Get("pageSize"))
	if !ok {
		return
	}
	res, err := srv.s.ListClusters(r.Context(), domain.ListClustersOptions{
		MaxResults: pageSize,
		NextToken:  r.URL.Query().Get("pageToken"),
	})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := managedkafkaraw.ListClustersResponse{NextPageToken: res.NextToken}
	for _, c := range res.Clusters {
		clust := c
		// Cluster.Name holds the short ID set at creation time.
		resp.Clusters = append(resp.Clusters, clusterToGCP(project, location, clust.Name, clust))
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) deleteCluster(w http.ResponseWriter, r *http.Request, project, location, clusterID string) {
	if err := srv.s.DeleteCluster(r.Context(), clusterID); err != nil {
		mapDomainError(w, err)
		return
	}
	parentBase := fmt.Sprintf("projects/%s/locations/%s", project, location)
	op := emptyOperation(fmt.Sprintf("%s/operations/delete-%s", parentBase, clusterID))
	writeJSON(w, http.StatusOK, op)
}

func (srv *Server) getOperation(w http.ResponseWriter, r *http.Request, project, location, opID string) {
	parentBase := fmt.Sprintf("projects/%s/locations/%s", project, location)
	op := emptyOperation(fmt.Sprintf("%s/operations/%s", parentBase, opID))
	writeJSON(w, http.StatusOK, op)
}

func (srv *Server) createTopic(w http.ResponseWriter, r *http.Request, parent, cluster string) {
	topicID := r.URL.Query().Get("topicId")
	if topicID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "topicId is required")
		return
	}
	var body managedkafkaraw.Topic
	if !decodeJSON(w, r, &body) {
		return
	}
	opt, ok := createOptionsFromGCP(w, &body)
	if !ok {
		return
	}
	topic, err := srv.s.CreateTopic(r.Context(), cluster, topicID, opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, topicToGCP(parent, topic))
}

func (srv *Server) getTopic(w http.ResponseWriter, r *http.Request, fullName, cluster, topicID string) {
	topic, err := srv.s.DescribeTopic(r.Context(), cluster, topicID)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, topicToGCP(parentFromTopicName(fullName), topic))
}

func (srv *Server) listTopics(w http.ResponseWriter, r *http.Request, parent, cluster string) {
	pageSize, ok := parsePageSize(w, r.URL.Query().Get("pageSize"))
	if !ok {
		return
	}
	res, err := srv.s.ListTopics(r.Context(), domain.ListTopicsOptions{
		ClusterID:  cluster,
		MaxResults: pageSize,
		NextToken:  r.URL.Query().Get("pageToken"),
	})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := managedkafkaraw.ListTopicsResponse{NextPageToken: res.NextToken}
	for _, topic := range res.Topics {
		t := topic
		resp.Topics = append(resp.Topics, topicToGCP(parent, t))
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) deleteTopic(w http.ResponseWriter, r *http.Request, cluster, topicID string) {
	if err := srv.s.DeleteTopic(r.Context(), cluster, topicID); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &managedkafkaraw.Empty{})
}

func clusterToGCP(project, location, clusterID string, c domain.Cluster) *managedkafkaraw.Cluster {
	vcpu := int64(c.BrokerCount * 3)
	if vcpu < 3 {
		vcpu = 3
	}
	memBytes := vcpu * 1024 * 1024 * 1024
	return &managedkafkaraw.Cluster{
		Name:  fmt.Sprintf("projects/%s/locations/%s/clusters/%s", project, location, clusterID),
		State: "ACTIVE",
		CapacityConfig: &managedkafkaraw.CapacityConfig{
			VcpuCount:       vcpu,
			MemoryBytes:     memBytes,
			ForceSendFields: []string{"VcpuCount", "MemoryBytes"},
		},
		GcpConfig: &managedkafkaraw.GcpConfig{
			AccessConfig: &managedkafkaraw.AccessConfig{
				NetworkConfigs: []*managedkafkaraw.NetworkConfig{
					{Subnet: "projects/" + project + "/regions/" + location + "/subnetworks/default"},
				},
			},
		},
	}
}

// terminalOperation wraps a resource as a done=true GCP LRO response.
func terminalOperation(name string, resource any) *managedkafkaraw.Operation {
	raw, _ := json.Marshal(resource)
	return &managedkafkaraw.Operation{
		Name:            name,
		Done:            true,
		Response:        googleapi.RawMessage(raw),
		ForceSendFields: []string{"Done"},
	}
}

// emptyOperation returns a done=true GCP LRO with an empty response payload.
func emptyOperation(name string) *managedkafkaraw.Operation {
	return &managedkafkaraw.Operation{
		Name:            name,
		Done:            true,
		Response:        googleapi.RawMessage(`{}`),
		ForceSendFields: []string{"Done"},
	}
}

func createOptionsFromGCP(w http.ResponseWriter, topic *managedkafkaraw.Topic) (domain.CreateTopicOptions, bool) {
	if topic.PartitionCount <= 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "partitionCount must be positive")
		return domain.CreateTopicOptions{}, false
	}
	if topic.ReplicationFactor != 0 && topic.ReplicationFactor != 1 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "replicationFactor must be 1 (the only portable value in the eventstream intersection)")
		return domain.CreateTopicOptions{}, false
	}
	retention, ok := retentionFromConfigs(w, topic.Configs)
	if !ok {
		return domain.CreateTopicOptions{}, false
	}
	return domain.CreateTopicOptions{
		PartitionCount: int(topic.PartitionCount),
		Retention:      retention,
	}, true
}

func retentionFromConfigs(w http.ResponseWriter, configs map[string]string) (time.Duration, bool) {
	var retention time.Duration
	for key, value := range configs {
		if key != retentionConfigKey {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unsupported topic config "+key)
			return 0, false
		}
		ms, err := strconv.ParseInt(value, 10, 64)
		if err != nil || ms < 0 {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "retention.ms must be a non-negative integer")
			return 0, false
		}
		retention = time.Duration(ms) * time.Millisecond
	}
	return retention, true
}

func topicToGCP(parent string, topic domain.Topic) *managedkafkaraw.Topic {
	out := &managedkafkaraw.Topic{
		Name:           parent + "/topics/" + topic.Name,
		PartitionCount: int64(topic.PartitionCount),
	}
	if topic.Retention > 0 {
		out.Configs = map[string]string{retentionConfigKey: strconv.FormatInt(topic.Retention.Milliseconds(), 10)}
	}
	return out
}

func parentFromTopicName(name string) string {
	parent, _, ok := strings.Cut(name, "/topics/")
	if !ok {
		return ""
	}
	return parent
}

func parsePageSize(w http.ResponseWriter, raw string) (int, bool) {
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "pageSize must be a non-negative integer")
		return 0, false
	}
	return n, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "read request body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
