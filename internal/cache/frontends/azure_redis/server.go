// Package azure_redis is the Azure Cache for Redis ARM frontend.
// Wire types + routing come from the spec-driven generated stubs in
// services/cache/gen/azure (cmd/azure-codegen). The adapter on
// Server implements gen.ServerInterface; gen.HandlerWithOptions
// dispatches each request to the matching method on the shape the
// hashicorp/azurerm provider + Azure Go SDK + `az redis` CLI all use.
//
// ARM URL shape (path-routed by the gen mux):
//
//	/subscriptions/{subscriptionId}
//	  /resourceGroups/{resourceGroupName}
//	    /providers/Microsoft.Cache/redis/{name}
package azure_redis

import (
	"net/http"

	"github.com/e6qu/shimanism/internal/cache/domain"
	gen "github.com/e6qu/shimanism/services/cache/gen/azure"
)

// Server is the Azure-Cache-for-Redis-shaped HTTP frontend. It
// implements gen.ServerInterface; ServeHTTP routes through the
// generated http.Handler.
type Server struct {
	s   domain.Cache
	mux http.Handler
}

// New returns a frontend bound to the given backend.
func New(s domain.Cache) *Server {
	srv := &Server{s: s}
	srv.mux = gen.HandlerWithOptions(srv, gen.StdHTTPServerOptions{})
	return srv
}

// ServeHTTP delegates to the generated routing layer.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	srv.mux.ServeHTTP(w, r)
}

// notImplemented writes the ARM "operation not supported" error
// envelope for spec-defined operations the cross-cloud cache
// intersection doesn't carry (Access policies / firewall rules /
// patch schedules / linked servers / private endpoints / key
// rotation / etc). Honest 501.
func notImplemented(w http.ResponseWriter, op string) {
	writeError(w, http.StatusNotImplemented, "OperationNotSupported",
		op+" is not in the cross-cloud cache intersection")
}

// =====================================================================
// In-intersection handlers
// =====================================================================

func (srv *Server) RedisCreate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.RedisCreateParams) {
	var body gen.RedisCreateParameters
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.CreateInstanceOptions{}
	if body.Properties.RedisVersion != nil {
		opt.EngineVersion = *body.Properties.RedisVersion
	}
	opt.NodeType = string(body.Properties.Sku.Name)
	if _, err := srv.s.CreateInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	inst, _ := srv.s.DescribeInstance(r.Context(), name)
	writeJSON(w, http.StatusCreated, instanceToARM(inst))
}

func (srv *Server) RedisGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.RedisGetParams) {
	inst, err := srv.s.DescribeInstance(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, instanceToARM(inst))
}

func (srv *Server) RedisDelete(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.RedisDeleteParams) {
	if err := srv.s.DeleteInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) RedisUpdate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.RedisUpdateParams) {
	var body gen.RedisUpdateParameters
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.ModifyInstanceOptions{}
	if body.Properties != nil {
		opt.NodeType = string(body.Properties.Sku.Name)
	}
	if err := srv.s.ModifyInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	inst, _ := srv.s.DescribeInstance(r.Context(), name)
	writeJSON(w, http.StatusOK, instanceToARM(inst))
}

func (srv *Server) RedisForceReboot(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.RedisForceRebootParams) {
	if err := srv.s.RebootInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) RedisListByResourceGroup(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ gen.RedisListByResourceGroupParams) {
	res, err := srv.s.ListInstances(r.Context(), domain.ListInstancesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	values := make([]gen.RedisResource, 0, len(res.Instances))
	for _, i := range res.Instances {
		values = append(values, *instanceToARM(i))
	}
	out := gen.RedisListResult{Value: values}
	writeJSON(w, http.StatusOK, &out)
}

// =====================================================================
// Out-of-intersection stubs — honest 501s
// =====================================================================

func (srv *Server) OperationsList(w http.ResponseWriter, _ *http.Request, _ gen.OperationsListParams) {
	notImplemented(w, "OperationsList")
}

func (srv *Server) RedisCheckNameAvailability(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.RedisCheckNameAvailabilityParams) {
	notImplemented(w, "RedisCheckNameAvailability")
}

func (srv *Server) AsyncOperationStatusGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ string, _ string, _ gen.AsyncOperationStatusGetParams) {
	notImplemented(w, "AsyncOperationStatusGet")
}

func (srv *Server) RedisListBySubscription(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.RedisListBySubscriptionParams) {
	notImplemented(w, "RedisListBySubscription")
}

func (srv *Server) AccessPolicyList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.AccessPolicyListParams) {
	notImplemented(w, "AccessPolicyList")
}

func (srv *Server) AccessPolicyDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.AccessPolicyDeleteParams) {
	notImplemented(w, "AccessPolicyDelete")
}

func (srv *Server) AccessPolicyGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.AccessPolicyGetParams) {
	notImplemented(w, "AccessPolicyGet")
}

func (srv *Server) AccessPolicyCreateUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.AccessPolicyCreateUpdateParams) {
	notImplemented(w, "AccessPolicyCreateUpdate")
}

func (srv *Server) AccessPolicyAssignmentList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.AccessPolicyAssignmentListParams) {
	notImplemented(w, "AccessPolicyAssignmentList")
}

func (srv *Server) AccessPolicyAssignmentDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.AccessPolicyAssignmentDeleteParams) {
	notImplemented(w, "AccessPolicyAssignmentDelete")
}

func (srv *Server) AccessPolicyAssignmentGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.AccessPolicyAssignmentGetParams) {
	notImplemented(w, "AccessPolicyAssignmentGet")
}

func (srv *Server) AccessPolicyAssignmentCreateUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.AccessPolicyAssignmentCreateUpdateParams) {
	notImplemented(w, "AccessPolicyAssignmentCreateUpdate")
}

func (srv *Server) FirewallRulesList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.FirewallRulesListParams) {
	notImplemented(w, "FirewallRulesList")
}

func (srv *Server) FirewallRulesDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.FirewallRulesDeleteParams) {
	notImplemented(w, "FirewallRulesDelete")
}

func (srv *Server) FirewallRulesGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.FirewallRulesGetParams) {
	notImplemented(w, "FirewallRulesGet")
}

func (srv *Server) FirewallRulesCreateOrUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.FirewallRulesCreateOrUpdateParams) {
	notImplemented(w, "FirewallRulesCreateOrUpdate")
}

func (srv *Server) RedisFlushCache(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.RedisFlushCacheParams) {
	notImplemented(w, "RedisFlushCache")
}

func (srv *Server) PatchSchedulesListByRedisResource(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PatchSchedulesListByRedisResourceParams) {
	notImplemented(w, "PatchSchedulesListByRedisResource")
}

func (srv *Server) PrivateEndpointConnectionsList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PrivateEndpointConnectionsListParams) {
	notImplemented(w, "PrivateEndpointConnectionsList")
}

func (srv *Server) PrivateEndpointConnectionsDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PrivateEndpointConnectionName, _ gen.PrivateEndpointConnectionsDeleteParams) {
	notImplemented(w, "PrivateEndpointConnectionsDelete")
}

func (srv *Server) PrivateEndpointConnectionsGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PrivateEndpointConnectionName, _ gen.PrivateEndpointConnectionsGetParams) {
	notImplemented(w, "PrivateEndpointConnectionsGet")
}

func (srv *Server) PrivateEndpointConnectionsPut(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PrivateEndpointConnectionName, _ gen.PrivateEndpointConnectionsPutParams) {
	notImplemented(w, "PrivateEndpointConnectionsPut")
}

func (srv *Server) PrivateLinkResourcesListByRedisCache(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PrivateLinkResourcesListByRedisCacheParams) {
	notImplemented(w, "PrivateLinkResourcesListByRedisCache")
}

func (srv *Server) RedisExportData(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.RedisExportDataParams) {
	notImplemented(w, "RedisExportData")
}

func (srv *Server) RedisImportData(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.RedisImportDataParams) {
	notImplemented(w, "RedisImportData")
}

func (srv *Server) LinkedServerList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.LinkedServerListParams) {
	notImplemented(w, "LinkedServerList")
}

func (srv *Server) LinkedServerDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.LinkedServerDeleteParams) {
	notImplemented(w, "LinkedServerDelete")
}

func (srv *Server) LinkedServerGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.LinkedServerGetParams) {
	notImplemented(w, "LinkedServerGet")
}

func (srv *Server) LinkedServerCreate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.LinkedServerCreateParams) {
	notImplemented(w, "LinkedServerCreate")
}

func (srv *Server) RedisListKeys(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.RedisListKeysParams) {
	notImplemented(w, "RedisListKeys")
}

func (srv *Server) RedisListUpgradeNotifications(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.RedisListUpgradeNotificationsParams) {
	notImplemented(w, "RedisListUpgradeNotifications")
}

func (srv *Server) PatchSchedulesDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PatchSchedulesDeleteParamsDefault, _ gen.PatchSchedulesDeleteParams) {
	notImplemented(w, "PatchSchedulesDelete")
}

func (srv *Server) PatchSchedulesGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PatchSchedulesGetParamsDefault, _ gen.PatchSchedulesGetParams) {
	notImplemented(w, "PatchSchedulesGet")
}

func (srv *Server) PatchSchedulesCreateOrUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PatchSchedulesCreateOrUpdateParamsDefault, _ gen.PatchSchedulesCreateOrUpdateParams) {
	notImplemented(w, "PatchSchedulesCreateOrUpdate")
}

func (srv *Server) RedisRegenerateKey(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.RedisRegenerateKeyParams) {
	notImplemented(w, "RedisRegenerateKey")
}

// =====================================================================
// Helpers
// =====================================================================

// instanceToARM maps a domain.Instance to the gen.RedisResource shape
// that hashicorp/azurerm + the Azure Go SDK + `az redis` all decode.
// Pointers everywhere because oapi-codegen emits OpenAPI optional
// fields as `*T`.
func instanceToARM(in domain.Instance) *gen.RedisResource {
	location := "eastus"
	typ := "Microsoft.Cache/Redis"
	resourceID := "/subscriptions/shim/resourceGroups/shim/providers/Microsoft.Cache/redis/" + in.Name

	props := gen.RedisProperties{
		Sku: gen.Sku{
			Name:     gen.SkuName(in.NodeType),
			Family:   "C",
			Capacity: 0,
		},
	}
	if in.EngineVersion != "" {
		v := in.EngineVersion
		props.RedisVersion = &v
	}
	state := statusToARM(in.Status)
	provisioning := gen.ProvisioningState(state)
	props.ProvisioningState = &provisioning

	if in.Status == domain.StatusAvailable && in.Connection.Host != "" {
		host := in.Connection.Host
		props.HostName = &host
		port := int32(in.Connection.Port) //nolint:gosec // 6379 in canonical case fits int32 trivially.
		if port == 0 {
			port = 6379
		}
		props.Port = &port
	}

	res := &gen.RedisResource{
		Id:         &resourceID,
		Name:       &in.Name,
		Type:       &typ,
		Location:   location,
		Properties: props,
	}
	return res
}

// statusToARM maps the domain status enum to Azure's ProvisioningState.
func statusToARM(s domain.Status) string {
	switch s {
	case domain.StatusAvailable:
		return "Succeeded"
	case domain.StatusCreating:
		return "Creating"
	case domain.StatusModifying:
		return "Updating"
	case domain.StatusRebooting:
		return "Restarting"
	case domain.StatusDeleting:
		return "Deleting"
	default:
		return "Unknown"
	}
}
