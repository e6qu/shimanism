// Package azure_eventhubs is the Azure Event Hubs ARM control-plane frontend
// for shimanism's eventstream service. The Kafka data plane is served by
// internal/eventstream/kafkaserver; the bootstrap broker address is passed
// through Options and stored in the cluster state at namespace creation time.
//
// Wire protocol: ARM REST/JSON — same shape as armnetwork/armcompute frontends.
// Auth: Azure Bearer verifier (audience "https://management.azure.com/").
//
// ARM path:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/
//	  Microsoft.EventHub/namespaces[/{ns}[/eventhubs[/{eh}]]]
//
// Namespace → domain.Cluster  (namespace name = cluster ID)
// EventHub  → domain.Topic
//
// Out of intersection: CaptureDescription, RetentionDescription (complex),
// geo-disaster recovery, network rules, BYOK encryption, dedicated clusters,
// Basic SKU (no Kafka support), replication factor.
package azure_eventhubs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/eventstream/domain"
)

const (
	subscriptionID = "00000000-0000-0000-0000-000000000001"
	resourceGroup  = "shim-rg"
	location       = "eastus"
	armType        = "Microsoft.EventHub/Namespaces"
	ehArmType      = "Microsoft.EventHub/Namespaces/EventHubs"
)

// Options configures the Azure Event Hubs frontend.
type Options struct {
	// BootstrapBrokers is the list of Kafka TCP addresses this shim's
	// data-plane server listens on. Stored in the cluster state at
	// namespace creation so the Kafka server can bind them to a cluster.
	BootstrapBrokers []string
}

// Server is an Azure-Event-Hubs-shaped HTTP frontend dispatching to
// a domain.Streams backend.
type Server struct {
	s                domain.Streams
	bootstrapBrokers []string
}

// New returns a frontend bound to the given backend.
func New(s domain.Streams, opt Options) *Server {
	return &Server{s: s, bootstrapBrokers: append([]string(nil), opt.BootstrapBrokers...)}
}

// Handler wraps Server with the Azure bearer verifier middleware.
func Handler(s domain.Streams, opt Options) http.Handler {
	verifier := azurebearer.New(azurebearer.Options{
		Audience: "https://management.azure.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	mw := azurebearer.Middleware(verifier, azurebearer.WithChallenge("https://management.azure.com/"))
	return mw(New(s, opt))
}

// ServeHTTP dispatches ARM paths for Microsoft.EventHub resources.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	lower := strings.ToLower(path)

	const providerSuffix = "/providers/microsoft.eventhub/namespaces"
	idx := strings.Index(lower, providerSuffix)
	if idx < 0 {
		writeARMError(w, http.StatusNotFound, "ResourceNotFound", "no Azure Event Hubs route matches "+r.Method+" "+path)
		return
	}

	rest := path[idx+len(providerSuffix):]
	// Normalise: remove leading slash, then split.
	rest = strings.TrimPrefix(rest, "/")

	if rest == "" {
		// .../namespaces — list all namespaces in the resource group
		if r.Method != http.MethodGet {
			writeARMError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method+" not allowed on namespaces")
			return
		}
		srv.listNamespaces(w, r)
		return
	}

	segs := strings.SplitN(rest, "/", 3)
	nsName := segs[0]

	if len(segs) == 1 {
		// .../namespaces/{ns}
		switch r.Method {
		case http.MethodPut:
			srv.createOrUpdateNamespace(w, r, nsName)
		case http.MethodGet:
			srv.getNamespace(w, r, nsName)
		case http.MethodDelete:
			srv.deleteNamespace(w, r, nsName)
		default:
			writeARMError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method+" not allowed on namespace")
		}
		return
	}

	// segs[1] must be "eventhubs"
	if strings.ToLower(segs[1]) != "eventhubs" {
		writeARMError(w, http.StatusNotImplemented, "OperationNotSupported",
			"only eventhubs sub-resource is implemented; "+segs[1]+" is out of the eventstream intersection")
		return
	}

	if len(segs) == 2 || segs[2] == "" {
		// .../namespaces/{ns}/eventhubs — list
		if r.Method != http.MethodGet {
			writeARMError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method+" not allowed on eventhubs")
			return
		}
		srv.listEventHubs(w, r, nsName)
		return
	}

	// segs[2] may be "{eh}" or "{eh}/..."
	ehName := strings.SplitN(segs[2], "/", 2)[0]
	if len(strings.SplitN(segs[2], "/", 2)) > 1 {
		writeARMError(w, http.StatusNotImplemented, "OperationNotSupported",
			"eventhub sub-resources are out of the eventstream intersection")
		return
	}

	switch r.Method {
	case http.MethodPut:
		srv.createOrUpdateEventHub(w, r, nsName, ehName)
	case http.MethodGet:
		srv.getEventHub(w, r, nsName, ehName)
	case http.MethodDelete:
		srv.deleteEventHub(w, r, nsName, ehName)
	default:
		writeARMError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method+" not allowed on eventhub")
	}
}

// ── Namespace (Cluster) operations ──────────────────────────────────────────

func (srv *Server) createOrUpdateNamespace(w http.ResponseWriter, r *http.Request, nsName string) {
	var req armeventhub.EHNamespace
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeARMError(w, http.StatusBadRequest, "InvalidRequestContent", "invalid request body: "+err.Error())
		return
	}
	if err := rejectOutOfIntersectionNamespaceOptions(&req); err != nil {
		writeARMError(w, http.StatusBadRequest, "OperationNotSupported", err.Error())
		return
	}

	cluster, err := srv.s.CreateCluster(r.Context(), nsName, nsName, domain.CreateClusterOptions{
		BrokerCount:      1,
		BootstrapBrokers: srv.bootstrapBrokers,
		Tags:             toStringMap(req.Tags),
	})
	if err != nil && !domain.IsAlreadyExists(err) {
		writeARMDomainError(w, err)
		return
	}
	if domain.IsAlreadyExists(err) {
		// Update idempotency: re-read and return existing.
		cluster, err = srv.s.DescribeCluster(r.Context(), nsName)
		if err != nil {
			writeARMDomainError(w, err)
			return
		}
	}
	// BeginCreateOrUpdate: 201 with final resource = terminal success (no LRO headers).
	writeJSON(w, http.StatusCreated, clusterToARM(cluster))
}

func (srv *Server) getNamespace(w http.ResponseWriter, r *http.Request, nsName string) {
	cluster, err := srv.s.DescribeCluster(r.Context(), nsName)
	if err != nil {
		writeARMDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, clusterToARM(cluster))
}

func (srv *Server) listNamespaces(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListClusters(r.Context(), domain.ListClustersOptions{})
	if err != nil {
		writeARMDomainError(w, err)
		return
	}
	type nsListResult struct {
		Value []*armeventhub.EHNamespace `json:"value"`
	}
	out := nsListResult{Value: []*armeventhub.EHNamespace{}}
	for _, c := range res.Clusters {
		c := c
		ns := clusterToARM(c)
		out.Value = append(out.Value, ns)
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) deleteNamespace(w http.ResponseWriter, r *http.Request, nsName string) {
	if err := srv.s.DeleteCluster(r.Context(), nsName); err != nil {
		writeARMDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ── EventHub (Topic) operations ─────────────────────────────────────────────

func (srv *Server) createOrUpdateEventHub(w http.ResponseWriter, r *http.Request, nsName, ehName string) {
	var req armeventhub.Eventhub
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeARMError(w, http.StatusBadRequest, "InvalidRequestContent", "invalid request body: "+err.Error())
		return
	}
	if err := rejectOutOfIntersectionEventHubOptions(&req); err != nil {
		writeARMError(w, http.StatusBadRequest, "OperationNotSupported", err.Error())
		return
	}

	// Validate cluster exists.
	if _, err := srv.s.DescribeCluster(r.Context(), nsName); err != nil {
		writeARMDomainError(w, err)
		return
	}

	partitions := int64(1)
	if req.Properties != nil && req.Properties.PartitionCount != nil && *req.Properties.PartitionCount > 0 {
		partitions = *req.Properties.PartitionCount
	}
	var retention time.Duration
	if req.Properties != nil && req.Properties.MessageRetentionInDays != nil {
		retention = time.Duration(*req.Properties.MessageRetentionInDays) * 24 * time.Hour
	}

	topic, err := srv.s.CreateTopic(r.Context(), nsName, ehName, domain.CreateTopicOptions{
		PartitionCount: int(partitions),
		Retention:      retention,
	})
	if err != nil && !domain.IsAlreadyExists(err) {
		writeARMDomainError(w, err)
		return
	}
	if domain.IsAlreadyExists(err) {
		topic, err = srv.s.DescribeTopic(r.Context(), nsName, ehName)
		if err != nil {
			writeARMDomainError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, topicToARM(nsName, topic))
}

func (srv *Server) getEventHub(w http.ResponseWriter, r *http.Request, nsName, ehName string) {
	topic, err := srv.s.DescribeTopic(r.Context(), nsName, ehName)
	if err != nil {
		writeARMDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, topicToARM(nsName, topic))
}

func (srv *Server) listEventHubs(w http.ResponseWriter, r *http.Request, nsName string) {
	if _, err := srv.s.DescribeCluster(r.Context(), nsName); err != nil {
		writeARMDomainError(w, err)
		return
	}
	res, err := srv.s.ListTopics(r.Context(), domain.ListTopicsOptions{ClusterID: nsName})
	if err != nil {
		writeARMDomainError(w, err)
		return
	}
	type ehListResult struct {
		Value []*armeventhub.Eventhub `json:"value"`
	}
	out := ehListResult{Value: []*armeventhub.Eventhub{}}
	for _, t := range res.Topics {
		t := t
		out.Value = append(out.Value, topicToARM(nsName, t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) deleteEventHub(w http.ResponseWriter, r *http.Request, nsName, ehName string) {
	if err := srv.s.DeleteTopic(r.Context(), nsName, ehName); err != nil {
		writeARMDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ── Shape translations ───────────────────────────────────────────────────────

func nsResourceID(name string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.EventHub/namespaces/%s",
		subscriptionID, resourceGroup, name)
}

func ehResourceID(nsName, ehName string) string {
	return fmt.Sprintf("%s/eventhubs/%s", nsResourceID(nsName), ehName)
}

func clusterToARM(c domain.Cluster) *armeventhub.EHNamespace {
	provState := armeventhub.EndPointProvisioningStateSucceeded
	tags := make(map[string]*string, len(c.Tags))
	for k, v := range c.Tags {
		tags[k] = to.Ptr(v)
	}
	return &armeventhub.EHNamespace{
		ID:       to.Ptr(nsResourceID(c.Name)),
		Name:     to.Ptr(c.Name),
		Type:     to.Ptr(armType),
		Location: to.Ptr(location),
		SKU:      &armeventhub.SKU{Name: to.Ptr(armeventhub.SKUName("Standard")), Tier: to.Ptr(armeventhub.SKUTierStandard)},
		Tags:     tags,
		Properties: &armeventhub.EHNamespaceProperties{
			ProvisioningState: to.Ptr(string(provState)),
			CreatedAt:         to.Ptr(c.CreatedAt),
			UpdatedAt:         to.Ptr(c.CreatedAt),
			KafkaEnabled:      to.Ptr(true),
		},
	}
}

func topicToARM(nsName string, t domain.Topic) *armeventhub.Eventhub {
	partCount := int64(t.PartitionCount)
	partIDs := make([]*string, partCount)
	for i := range partIDs {
		s := fmt.Sprintf("%d", i)
		partIDs[i] = &s
	}
	retentionDays := int64(1)
	if t.Retention > 0 {
		days := int64(t.Retention.Hours() / 24)
		if days < 1 {
			days = 1
		}
		retentionDays = days
	}
	active := armeventhub.EntityStatusActive
	return &armeventhub.Eventhub{
		ID:   to.Ptr(ehResourceID(nsName, t.Name)),
		Name: to.Ptr(t.Name),
		Type: to.Ptr(ehArmType),
		Properties: &armeventhub.Properties{
			PartitionCount:         to.Ptr(partCount),
			PartitionIDs:           partIDs,
			MessageRetentionInDays: to.Ptr(retentionDays),
			Status:                 &active,
			CreatedAt:              to.Ptr(time.Now().UTC()),
			UpdatedAt:              to.Ptr(time.Now().UTC()),
		},
	}
}

// ── Validation ───────────────────────────────────────────────────────────────

func rejectOutOfIntersectionNamespaceOptions(req *armeventhub.EHNamespace) error {
	if req.SKU != nil && req.SKU.Name != nil {
		if strings.ToLower(string(*req.SKU.Name)) == "basic" {
			return fmt.Errorf("basic SKU does not support the Kafka endpoint; use Standard or Premium")
		}
	}
	if req.Properties == nil {
		return nil
	}
	p := req.Properties
	if p.Encryption != nil {
		return fmt.Errorf("encryption (BYOK) is out of the eventstream intersection")
	}
	if p.ClusterArmID != nil {
		return fmt.Errorf("dedicated cluster is out of the eventstream intersection")
	}
	if p.IsAutoInflateEnabled != nil && *p.IsAutoInflateEnabled {
		return fmt.Errorf("auto-inflate is out of the eventstream intersection")
	}
	return nil
}

func rejectOutOfIntersectionEventHubOptions(req *armeventhub.Eventhub) error {
	if req.Properties == nil {
		return nil
	}
	p := req.Properties
	if p.CaptureDescription != nil {
		return fmt.Errorf("CaptureDescription (archive) is out of the eventstream intersection")
	}
	if p.RetentionDescription != nil {
		return fmt.Errorf("RetentionDescription is out of the eventstream intersection; use MessageRetentionInDays")
	}
	if p.PartitionCount != nil && *p.PartitionCount > 32 {
		return fmt.Errorf("partitionCount %d exceeds the intersection maximum of 32", *p.PartitionCount)
	}
	return nil
}

// ── Error helpers ────────────────────────────────────────────────────────────

type armErrorInner struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type armErrorBody struct {
	Error armErrorInner `json:"error"`
}

func writeARMError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(armErrorBody{Error: armErrorInner{Code: code, Message: message}})
}

func writeARMDomainError(w http.ResponseWriter, err error) {
	switch {
	case domain.IsNotFound(err):
		writeARMError(w, http.StatusNotFound, "ResourceNotFound", err.Error())
	case domain.IsAlreadyExists(err):
		writeARMError(w, http.StatusConflict, "ResourceAlreadyExists", err.Error())
	case domain.IsInvalidInput(err):
		writeARMError(w, http.StatusBadRequest, "InvalidParameter", err.Error())
	default:
		writeARMError(w, http.StatusInternalServerError, "InternalServerError", err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func toStringMap(tags map[string]*string) map[string]string {
	if tags == nil {
		return nil
	}
	m := make(map[string]string, len(tags))
	for k, v := range tags {
		if v != nil {
			m[k] = *v
		}
	}
	return m
}
