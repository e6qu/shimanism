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
	// /v1/projects/{project}/locations/{location}/clusters/{cluster}/topics[/{topic}]
	if len(segs) < 6 || segs[0] != "projects" || segs[2] != "locations" || segs[4] != "clusters" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "no GCP Managed Kafka route matches "+r.Method+" "+path)
		return
	}
	if len(segs) < 7 || segs[6] != "topics" {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "only topic lifecycle routes are implemented in this Phase 20 slice")
		return
	}
	project, location, cluster := segs[1], segs[3], segs[5]
	parent := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", project, location, cluster)
	if len(segs) == 7 || (len(segs) == 8 && segs[7] == "") {
		switch r.Method {
		case http.MethodGet:
			srv.listTopics(w, r, parent)
		case http.MethodPost:
			srv.createTopic(w, r, parent)
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", r.Method+" not allowed on topics")
		}
		return
	}
	if len(segs) == 8 {
		topic := segs[7]
		fullName := parent + "/topics/" + topic
		switch r.Method {
		case http.MethodGet:
			srv.getTopic(w, r, fullName, topic)
		case http.MethodDelete:
			srv.deleteTopic(w, r, topic)
		case http.MethodPatch:
			writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "topic patch is out of the first eventstream frontend slice")
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", r.Method+" not allowed on topic")
		}
		return
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND", "no GCP Managed Kafka route matches "+r.Method+" "+path)
}

func (srv *Server) createTopic(w http.ResponseWriter, r *http.Request, parent string) {
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
	topic, err := srv.s.CreateTopic(r.Context(), topicID, opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, topicToGCP(parent, topic))
}

func (srv *Server) getTopic(w http.ResponseWriter, r *http.Request, fullName, topicID string) {
	topic, err := srv.s.DescribeTopic(r.Context(), topicID)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, topicToGCP(parentFromTopicName(fullName), topic))
}

func (srv *Server) listTopics(w http.ResponseWriter, r *http.Request, parent string) {
	pageSize, ok := parsePageSize(w, r.URL.Query().Get("pageSize"))
	if !ok {
		return
	}
	res, err := srv.s.ListTopics(r.Context(), domain.ListTopicsOptions{
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

func (srv *Server) deleteTopic(w http.ResponseWriter, r *http.Request, topicID string) {
	if err := srv.s.DeleteTopic(r.Context(), topicID); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &managedkafkaraw.Empty{})
}

func createOptionsFromGCP(w http.ResponseWriter, topic *managedkafkaraw.Topic) (domain.CreateTopicOptions, bool) {
	if topic.PartitionCount <= 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "partitionCount must be positive")
		return domain.CreateTopicOptions{}, false
	}
	if topic.ReplicationFactor != 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "replicationFactor is not portable in the eventstream intersection")
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
