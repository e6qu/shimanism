// Package azure_dbadmin is the Azure DB Admin (PostgreSQL Flexible
// Server) ARM frontend. Wire types + routing come from the
// spec-driven generated stubs in services/rdbms/gen/azure
// (cmd/azure-codegen). The adapter on Server implements
// gen.ServerInterface; gen.HandlerWithOptions dispatches each
// request.
//
// ARM URL shape (path-routed by the gen mux):
//
//	/subscriptions/{subscriptionId}
//	  /resourceGroups/{resourceGroupName}
//	    /providers/Microsoft.DBforPostgreSQL
//	      /flexibleServers/{serverName}
//
// Mutating ops in real ARM return 202 + an `Azure-AsyncOperation`
// header pointing at a polling URL. The shim's frontend returns the
// current instance JSON; Azure SDK pollers eventually fall through
// to a `Get` call which the shim handles honestly. Trade wire-shape
// fidelity for stateless simplicity; SDK conformance for the Azure
// cell is deferred.
package azure_dbadmin

import (
	"encoding/json"
	"net/http"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
	gen "github.com/e6qu/shimanism/services/rdbms/gen/azure"
)

// Server is the Azure-PostgreSQL-FlexibleServer-shaped HTTP frontend.
type Server struct {
	s   domain.RDBMS
	mux http.Handler
}

// New returns a frontend bound to the given backend.
func New(s domain.RDBMS) *Server {
	srv := &Server{s: s}
	srv.mux = gen.HandlerWithOptions(srv, gen.StdHTTPServerOptions{})
	return srv
}

// ServeHTTP delegates to the generated routing layer.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	srv.mux.ServeHTTP(w, r)
}

// notImplemented writes the ARM "operation not supported" envelope
// for spec ops outside the cross-cloud rdbms intersection.
func notImplemented(w http.ResponseWriter, op string) {
	writeError(w, http.StatusNotImplemented, "OperationNotSupported",
		op+" is not in the cross-cloud rdbms intersection")
}

// =====================================================================
// In-intersection handlers — server CRUD
// =====================================================================

func (srv *Server) ServersCreateOrUpdate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.ServersCreateOrUpdateParams) {
	var body gen.Server
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.CreateInstanceOptions{Engine: domain.EnginePostgres}
	if body.Properties != nil {
		if body.Properties.Version != nil {
			opt.EngineVersion = string(*body.Properties.Version)
		}
		if body.Properties.AdministratorLogin != nil {
			opt.MasterUsername = *body.Properties.AdministratorLogin
		}
		if body.Properties.AdministratorLoginPassword != nil {
			opt.MasterPassword = *body.Properties.AdministratorLoginPassword
		}
		if body.Properties.Storage != nil && body.Properties.Storage.StorageSizeGB != nil {
			opt.AllocatedStorageGB = int(*body.Properties.Storage.StorageSizeGB)
		}
	}
	if body.Sku != nil {
		opt.InstanceClass = body.Sku.Name
	}
	if _, err := srv.s.CreateInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	inst, _ := srv.s.DescribeInstance(r.Context(), name)
	writeJSON(w, http.StatusCreated, instanceToARM(inst))
}

func (srv *Server) ServersGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.ServersGetParams) {
	inst, err := srv.s.DescribeInstance(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, instanceToARM(inst))
}

func (srv *Server) ServersDelete(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.ServersDeleteParams) {
	if err := srv.s.DeleteInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) ServersUpdate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.ServersUpdateParams) {
	var body gen.ServerForPatch
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.ModifyInstanceOptions{}
	if body.Properties != nil {
		if body.Properties.AdministratorLoginPassword != nil && *body.Properties.AdministratorLoginPassword != "" {
			opt.MasterPassword = *body.Properties.AdministratorLoginPassword
		}
		if body.Properties.Storage != nil && body.Properties.Storage.StorageSizeGB != nil {
			opt.AllocatedStorageGB = int(*body.Properties.Storage.StorageSizeGB)
		}
	}
	if body.Sku != nil && body.Sku.Name != nil {
		opt.InstanceClass = *body.Sku.Name
	}
	if err := srv.s.ModifyInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	inst, _ := srv.s.DescribeInstance(r.Context(), name)
	writeJSON(w, http.StatusOK, instanceToARM(inst))
}

func (srv *Server) ServersListByResourceGroup(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ gen.ServersListByResourceGroupParams) {
	res, err := srv.s.ListInstances(r.Context(), domain.ListInstancesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	values := make([]gen.Server, 0, len(res.Instances))
	for _, i := range res.Instances {
		values = append(values, *instanceToARM(i))
	}
	writeJSON(w, http.StatusOK, map[string]any{"value": values})
}

func (srv *Server) ServersRestart(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.ServersRestartParams) {
	if err := srv.s.RebootInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// =====================================================================
// In-intersection handlers — backups
// =====================================================================

func (srv *Server) BackupsAutomaticAndOnDemandCreate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, instance string, backupName string, _ gen.BackupsAutomaticAndOnDemandCreateParams) {
	if _, err := srv.s.CreateSnapshot(r.Context(), instance, backupName); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"name": backupName,
		"type": "Microsoft.DBforPostgreSQL/flexibleServers/backups",
	})
}

func (srv *Server) BackupsAutomaticAndOnDemandGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, backupName string, _ gen.BackupsAutomaticAndOnDemandGetParams) {
	snap, err := srv.s.DescribeSnapshot(r.Context(), backupName)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": snap.ID,
		"type": "Microsoft.DBforPostgreSQL/flexibleServers/backups",
		"properties": map[string]any{
			"status": statusToARM(snap.Status),
		},
	})
}

func (srv *Server) BackupsAutomaticAndOnDemandDelete(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, backupName string, _ gen.BackupsAutomaticAndOnDemandDeleteParams) {
	if err := srv.s.DeleteSnapshot(r.Context(), backupName); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) BackupsAutomaticAndOnDemandListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, instance string, _ gen.BackupsAutomaticAndOnDemandListByServerParams) {
	res, err := srv.s.ListSnapshots(r.Context(), domain.ListSnapshotsOptions{Instance: instance})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	values := make([]map[string]any, 0, len(res.Snapshots))
	for _, s := range res.Snapshots {
		values = append(values, map[string]any{
			"name": s.ID,
			"type": "Microsoft.DBforPostgreSQL/flexibleServers/backups",
			"properties": map[string]any{
				"status": statusToARM(s.Status),
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"value": values})
}

// =====================================================================
// Out-of-intersection stubs — honest 501s for the remaining 56 spec ops
// =====================================================================

func (srv *Server) PrivateDnsZoneSuffixGet(w http.ResponseWriter, r *http.Request, _ gen.PrivateDnsZoneSuffixGetParams) {
	notImplemented(w, "PrivateDnsZoneSuffixGet")
}

func (srv *Server) OperationsList(w http.ResponseWriter, r *http.Request, _ gen.OperationsListParams) {
	notImplemented(w, "OperationsList")
}

func (srv *Server) NameAvailabilityCheckGlobally(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.NameAvailabilityCheckGloballyParams) {
	notImplemented(w, "NameAvailabilityCheckGlobally")
}

func (srv *Server) ServersListBySubscription(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ServersListBySubscriptionParams) {
	notImplemented(w, "ServersListBySubscription")
}

func (srv *Server) CapabilitiesByLocationList(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ string, _ gen.CapabilitiesByLocationListParams) {
	notImplemented(w, "CapabilitiesByLocationList")
}

func (srv *Server) NameAvailabilityCheckWithLocation(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ string, _ gen.NameAvailabilityCheckWithLocationParams) {
	notImplemented(w, "NameAvailabilityCheckWithLocation")
}

func (srv *Server) VirtualNetworkSubnetUsageList(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ string, _ gen.VirtualNetworkSubnetUsageListParams) {
	notImplemented(w, "VirtualNetworkSubnetUsageList")
}

func (srv *Server) QuotaUsagesList(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ string, _ gen.QuotaUsagesListParams) {
	notImplemented(w, "QuotaUsagesList")
}

func (srv *Server) AdministratorsMicrosoftEntraListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.AdministratorsMicrosoftEntraListByServerParams) {
	notImplemented(w, "AdministratorsMicrosoftEntraListByServer")
}

func (srv *Server) AdministratorsMicrosoftEntraDelete(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.AdministratorsMicrosoftEntraDeleteParams) {
	notImplemented(w, "AdministratorsMicrosoftEntraDelete")
}

func (srv *Server) AdministratorsMicrosoftEntraGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.AdministratorsMicrosoftEntraGetParams) {
	notImplemented(w, "AdministratorsMicrosoftEntraGet")
}

func (srv *Server) AdministratorsMicrosoftEntraCreateOrUpdate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.AdministratorsMicrosoftEntraCreateOrUpdateParams) {
	notImplemented(w, "AdministratorsMicrosoftEntraCreateOrUpdate")
}

func (srv *Server) AdvancedThreatProtectionSettingsListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.AdvancedThreatProtectionSettingsListByServerParams) {
	notImplemented(w, "AdvancedThreatProtectionSettingsListByServer")
}

func (srv *Server) AdvancedThreatProtectionSettingsGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.AdvancedThreatProtectionSettingsGetParamsThreatProtectionName, _ gen.AdvancedThreatProtectionSettingsGetParams) {
	notImplemented(w, "AdvancedThreatProtectionSettingsGet")
}

func (srv *Server) ServerThreatProtectionSettingsCreateOrUpdate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ServerThreatProtectionSettingsCreateOrUpdateParamsThreatProtectionName, _ gen.ServerThreatProtectionSettingsCreateOrUpdateParams) {
	notImplemented(w, "ServerThreatProtectionSettingsCreateOrUpdate")
}

func (srv *Server) CapabilitiesByServerList(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.CapabilitiesByServerListParams) {
	notImplemented(w, "CapabilitiesByServerList")
}

func (srv *Server) MigrationsCheckNameAvailability(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.MigrationsCheckNameAvailabilityParams) {
	notImplemented(w, "MigrationsCheckNameAvailability")
}

func (srv *Server) ConfigurationsListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ConfigurationsListByServerParams) {
	notImplemented(w, "ConfigurationsListByServer")
}

func (srv *Server) ConfigurationsGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.ConfigurationsGetParams) {
	notImplemented(w, "ConfigurationsGet")
}

func (srv *Server) ConfigurationsUpdate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.ConfigurationsUpdateParams) {
	notImplemented(w, "ConfigurationsUpdate")
}

func (srv *Server) ConfigurationsPut(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.ConfigurationsPutParams) {
	notImplemented(w, "ConfigurationsPut")
}

func (srv *Server) DatabasesListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.DatabasesListByServerParams) {
	notImplemented(w, "DatabasesListByServer")
}

func (srv *Server) DatabasesDelete(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.DatabasesDeleteParams) {
	notImplemented(w, "DatabasesDelete")
}

func (srv *Server) DatabasesGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.DatabasesGetParams) {
	notImplemented(w, "DatabasesGet")
}

func (srv *Server) DatabasesCreate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.DatabasesCreateParams) {
	notImplemented(w, "DatabasesCreate")
}

func (srv *Server) FirewallRulesListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.FirewallRulesListByServerParams) {
	notImplemented(w, "FirewallRulesListByServer")
}

func (srv *Server) FirewallRulesDelete(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.FirewallRulesDeleteParams) {
	notImplemented(w, "FirewallRulesDelete")
}

func (srv *Server) FirewallRulesGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.FirewallRulesGetParams) {
	notImplemented(w, "FirewallRulesGet")
}

func (srv *Server) FirewallRulesCreateOrUpdate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.FirewallRulesCreateOrUpdateParams) {
	notImplemented(w, "FirewallRulesCreateOrUpdate")
}

func (srv *Server) CapturedLogsListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.CapturedLogsListByServerParams) {
	notImplemented(w, "CapturedLogsListByServer")
}

func (srv *Server) BackupsLongTermRetentionListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.BackupsLongTermRetentionListByServerParams) {
	notImplemented(w, "BackupsLongTermRetentionListByServer")
}

func (srv *Server) BackupsLongTermRetentionGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.BackupsLongTermRetentionGetParams) {
	notImplemented(w, "BackupsLongTermRetentionGet")
}

func (srv *Server) BackupsLongTermRetentionCheckPrerequisites(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.BackupsLongTermRetentionCheckPrerequisitesParams) {
	notImplemented(w, "BackupsLongTermRetentionCheckPrerequisites")
}

func (srv *Server) MigrationsListByTargetServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.MigrationsListByTargetServerParams) {
	notImplemented(w, "MigrationsListByTargetServer")
}

func (srv *Server) MigrationsCancel(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.MigrationsCancelParams) {
	notImplemented(w, "MigrationsCancel")
}

func (srv *Server) MigrationsGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.MigrationsGetParams) {
	notImplemented(w, "MigrationsGet")
}

func (srv *Server) MigrationsUpdate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.MigrationsUpdateParams) {
	notImplemented(w, "MigrationsUpdate")
}

func (srv *Server) MigrationsCreate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.MigrationsCreateParams) {
	notImplemented(w, "MigrationsCreate")
}

func (srv *Server) PrivateEndpointConnectionsListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PrivateEndpointConnectionsListByServerParams) {
	notImplemented(w, "PrivateEndpointConnectionsListByServer")
}

func (srv *Server) PrivateEndpointConnectionsDelete(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PrivateEndpointConnectionName, _ gen.PrivateEndpointConnectionsDeleteParams) {
	notImplemented(w, "PrivateEndpointConnectionsDelete")
}

func (srv *Server) PrivateEndpointConnectionsGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PrivateEndpointConnectionName, _ gen.PrivateEndpointConnectionsGetParams) {
	notImplemented(w, "PrivateEndpointConnectionsGet")
}

func (srv *Server) PrivateEndpointConnectionsUpdate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PrivateEndpointConnectionName, _ gen.PrivateEndpointConnectionsUpdateParams) {
	notImplemented(w, "PrivateEndpointConnectionsUpdate")
}

func (srv *Server) PrivateLinkResourcesListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PrivateLinkResourcesListByServerParams) {
	notImplemented(w, "PrivateLinkResourcesListByServer")
}

func (srv *Server) PrivateLinkResourcesGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.PrivateLinkResourcesGetParams) {
	notImplemented(w, "PrivateLinkResourcesGet")
}

func (srv *Server) ReplicasListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ReplicasListByServerParams) {
	notImplemented(w, "ReplicasListByServer")
}

func (srv *Server) ServersStart(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ServersStartParams) {
	notImplemented(w, "ServersStart")
}

func (srv *Server) BackupsLongTermRetentionStart(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.BackupsLongTermRetentionStartParams) {
	notImplemented(w, "BackupsLongTermRetentionStart")
}

func (srv *Server) ServersStop(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ServersStopParams) {
	notImplemented(w, "ServersStop")
}

func (srv *Server) TuningOptionsListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.TuningOptionsListByServerParams) {
	notImplemented(w, "TuningOptionsListByServer")
}

func (srv *Server) TuningOptionsGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.TuningOptionsGetParamsTuningOption, _ gen.TuningOptionsGetParams) {
	notImplemented(w, "TuningOptionsGet")
}

func (srv *Server) TuningOptionsListRecommendations(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.TuningOptionsListRecommendationsParamsTuningOption, _ gen.TuningOptionsListRecommendationsParams) {
	notImplemented(w, "TuningOptionsListRecommendations")
}

func (srv *Server) VirtualEndpointsListByServer(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.VirtualEndpointsListByServerParams) {
	notImplemented(w, "VirtualEndpointsListByServer")
}

func (srv *Server) VirtualEndpointsDelete(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.VirtualEndpointsDeleteParams) {
	notImplemented(w, "VirtualEndpointsDelete")
}

func (srv *Server) VirtualEndpointsGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.VirtualEndpointsGetParams) {
	notImplemented(w, "VirtualEndpointsGet")
}

func (srv *Server) VirtualEndpointsUpdate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.VirtualEndpointsUpdateParams) {
	notImplemented(w, "VirtualEndpointsUpdate")
}

func (srv *Server) VirtualEndpointsCreate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.VirtualEndpointsCreateParams) {
	notImplemented(w, "VirtualEndpointsCreate")
}

// =====================================================================
// Helpers
// =====================================================================

// instanceToARM maps a domain.Instance to gen.Server (PostgreSQL
// FlexibleServer ARM resource). Pointers everywhere because the
// OpenAPI spec marks every Properties field optional and oapi-codegen
// emits each as `*T`.
func instanceToARM(in domain.Instance) *gen.Server {
	location := "eastus"
	typ := "Microsoft.DBforPostgreSQL/flexibleServers"
	resourceID := "/subscriptions/shim/resourceGroups/shim/providers/Microsoft.DBforPostgreSQL/flexibleServers/" + in.Name

	res := &gen.Server{
		Id:       &resourceID,
		Name:     &in.Name,
		Type:     &typ,
		Location: location,
		Sku: &gen.Sku{
			Name: in.InstanceClass,
			Tier: "GeneralPurpose",
		},
	}
	// Build the properties via JSON round-trip to keep the
	// anonymous-struct boilerplate out of the call site (same
	// trick as azure_containerapps).
	propsBlob := map[string]any{
		"version": in.EngineVersion,
		"state":   statusToARM(in.Status),
		"storage": map[string]any{"storageSizeGB": in.AllocatedStorageGB},
	}
	if in.Connection.MasterUsername != "" {
		propsBlob["administratorLogin"] = in.Connection.MasterUsername
	}
	if in.Status == domain.StatusAvailable && in.Connection.Host != "" {
		propsBlob["fullyQualifiedDomainName"] = in.Connection.Host
	}
	envelope := map[string]any{"location": location, "properties": propsBlob}
	raw, err := json.Marshal(envelope)
	if err == nil {
		var hydrated gen.Server
		if json.Unmarshal(raw, &hydrated) == nil {
			res.Properties = hydrated.Properties
		}
	}
	return res
}

func statusToARM(s domain.Status) string {
	switch s {
	case domain.StatusAvailable:
		return "Ready"
	case domain.StatusCreating:
		return "Provisioning"
	case domain.StatusModifying:
		return "Updating"
	case domain.StatusRebooting:
		return "Restarting"
	case domain.StatusDeleting:
		return "Dropping"
	default:
		return "Unknown"
	}
}
