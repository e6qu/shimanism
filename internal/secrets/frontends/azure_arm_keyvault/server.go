// Package azure_arm_keyvault is the Azure Key Vault ARM
// (Microsoft.KeyVault/vaults) frontend.
//
// ARM URL shape (path-routed by the gen mux):
//
//	/subscriptions/{subscriptionId}
//	  /resourceGroups/{resourceGroupName}
//	    /providers/Microsoft.KeyVault/vaults/{vaultName}
//
// **Vaults are routing fiction.** The shim's `domain.Secrets` has
// no "vault" concept — secrets live in a flat namespace on the
// destination backend, and the existing `azure_keyvault` data-plane
// frontend already accepts any vault URL prefix as opaque. Vault
// ARM operations are acknowledged without backend interaction:
// CreateOrUpdate returns a synthetic Vault resource, Get always
// returns "exists", Delete is a no-op success, List returns empty.
// The cross-cloud Terraform Apply flow this unblocks:
// `azurerm_key_vault` PUT → ARM acknowledges → `azurerm_key_vault_secret`
// PUT → data-plane azure_keyvault frontend → backend.
//
// In-intersection (7):
//   - VaultsCreateOrUpdate / Get / Update / Delete / ListBySubscription /
//     ListByResourceGroup / CheckNameAvailability — all stateless
//     acknowledgements.
//
// Out-of-intersection (10): soft-delete (ListDeleted / GetDeleted /
// PurgeDeleted), access policy updates, private-endpoint connections,
// private-link resources, and the legacy generic `VaultsList` over
// `/subscriptions/{s}/resources`.
package azure_arm_keyvault

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	openapi_types "github.com/oapi-codegen/runtime/types"

	gen "github.com/e6qu/shimanism/services/secrets/gen/azure_arm"
)

// Server is the Microsoft.KeyVault ARM-shaped HTTP frontend.
type Server struct {
	mux         http.Handler
	vaultURI    string // optional override returned in properties.vaultUri
	trackVaults bool
	mu          sync.Mutex
	vaults      map[string]struct{}
}

// Options configures the ARM frontend.
type Options struct {
	// VaultURI, when non-empty, is returned in every synthetic
	// Vault's `properties.vaultUri`. The `hashicorp/azurerm`
	// Terraform provider reads this field to derive the data-plane
	// URL for `azurerm_key_vault_secret` / `azurerm_key_vault_key`
	// / etc. Leave empty to fall back to the real-Azure-shaped
	// `https://<name>.vault.azure.net/` default.
	VaultURI string

	// TrackVaults, when true, makes the frontend maintain a tiny
	// in-process set of vault names that have been PUT to this
	// instance. VaultsGet returns 404 before any PUT, the
	// synthetic resource after — needed for azurerm's pre-create
	// idempotency check.
	TrackVaults bool
}

// New returns a vault-ARM frontend. The shim is vault-agnostic, so
// no backend is required at this layer — vault ARM operations are
// acknowledged synthetically; subsequent secret data-plane calls go
// through the existing azure_keyvault frontend.
func New(opts ...Options) *Server {
	srv := &Server{}
	if len(opts) > 0 {
		srv.vaultURI = opts[0].VaultURI
		srv.trackVaults = opts[0].TrackVaults
	}
	if srv.trackVaults {
		srv.vaults = map[string]struct{}{}
	}
	srv.mux = gen.HandlerWithOptions(srv, gen.StdHTTPServerOptions{})
	return srv
}

// ServeHTTP delegates to the gen-generated routing layer.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	srv.mux.ServeHTTP(w, r)
}

// notImplemented writes the ARM "operation not supported" envelope
// for spec-defined operations the cross-cloud secrets intersection
// doesn't carry.
func notImplemented(w http.ResponseWriter, op string) {
	writeError(w, http.StatusNotImplemented, "OperationNotSupported",
		op+" is not in the cross-cloud secrets intersection")
}

// =====================================================================
// In-intersection handlers — Vaults (acknowledged statelessly)
// =====================================================================

func (srv *Server) VaultsCreateOrUpdate(w http.ResponseWriter, r *http.Request, subscriptionId gen.SubscriptionIdParameter, resourceGroupName string, vaultName string, _ gen.VaultsCreateOrUpdateParams) {
	var body gen.VaultCreateOrUpdateParameters
	if !decodeJSON(w, r, &body) {
		return
	}
	srv.recordVault(vaultName)
	writeJSON(w, http.StatusCreated, srv.syntheticVault(subscriptionId, resourceGroupName, vaultName, body.Location))
}

func (srv *Server) VaultsGet(w http.ResponseWriter, _ *http.Request, subscriptionId gen.SubscriptionIdParameter, resourceGroupName string, vaultName string, _ gen.VaultsGetParams) {
	if srv.trackVaults && !srv.hasVault(vaultName) {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.KeyVault/vaults/"+vaultName+"' under resource group '"+resourceGroupName+"' was not found.")
		return
	}
	writeJSON(w, http.StatusOK, srv.syntheticVault(subscriptionId, resourceGroupName, vaultName, ""))
}

func (srv *Server) VaultsUpdate(w http.ResponseWriter, r *http.Request, subscriptionId gen.SubscriptionIdParameter, resourceGroupName string, vaultName string, _ gen.VaultsUpdateParams) {
	_ = decodeJSON(w, r, &struct{}{})
	writeJSON(w, http.StatusOK, srv.syntheticVault(subscriptionId, resourceGroupName, vaultName, ""))
}

func (srv *Server) VaultsDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ string, vaultName string, _ gen.VaultsDeleteParams) {
	srv.forgetVault(vaultName)
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) VaultsListBySubscription(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.VaultsListBySubscriptionParams) {
	writeJSON(w, http.StatusOK, gen.VaultListResult{Value: &[]gen.Vault{}})
}

func (srv *Server) VaultsListByResourceGroup(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ string, _ gen.VaultsListByResourceGroupParams) {
	writeJSON(w, http.StatusOK, gen.VaultListResult{Value: &[]gen.Vault{}})
}

func (srv *Server) VaultsCheckNameAvailability(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.VaultsCheckNameAvailabilityParams) {
	var body gen.VaultCheckNameAvailabilityParameters
	if !decodeJSON(w, r, &body) {
		return
	}
	available := true
	writeJSON(w, http.StatusOK, gen.CheckNameAvailabilityResult{NameAvailable: &available})
}

// =====================================================================
// Out-of-intersection stubs
// =====================================================================

func (srv *Server) VaultsList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.VaultsListParams) {
	notImplemented(w, "VaultsList")
}

func (srv *Server) VaultsListDeleted(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.VaultsListDeletedParams) {
	notImplemented(w, "VaultsListDeleted")
}

func (srv *Server) VaultsGetDeleted(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ string, _ string, _ gen.VaultsGetDeletedParams) {
	notImplemented(w, "VaultsGetDeleted")
}

func (srv *Server) VaultsPurgeDeleted(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ string, _ string, _ gen.VaultsPurgeDeletedParams) {
	notImplemented(w, "VaultsPurgeDeleted")
}

func (srv *Server) VaultsUpdateAccessPolicy(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ string, _ string, _ gen.VaultsUpdateAccessPolicyParamsOperationKind, _ gen.VaultsUpdateAccessPolicyParams) {
	notImplemented(w, "VaultsUpdateAccessPolicy")
}

func (srv *Server) PrivateEndpointConnectionsListByResource(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupName, _ gen.VaultName, _ gen.PrivateEndpointConnectionsListByResourceParams) {
	notImplemented(w, "PrivateEndpointConnectionsListByResource")
}

func (srv *Server) PrivateEndpointConnectionsDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupName, _ gen.VaultName, _ gen.PrivateEndpointConnectionName, _ gen.PrivateEndpointConnectionsDeleteParams) {
	notImplemented(w, "PrivateEndpointConnectionsDelete")
}

func (srv *Server) PrivateEndpointConnectionsGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupName, _ gen.VaultName, _ gen.PrivateEndpointConnectionName, _ gen.PrivateEndpointConnectionsGetParams) {
	notImplemented(w, "PrivateEndpointConnectionsGet")
}

func (srv *Server) PrivateEndpointConnectionsPut(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupName, _ gen.VaultName, _ gen.PrivateEndpointConnectionName, _ gen.PrivateEndpointConnectionsPutParams) {
	notImplemented(w, "PrivateEndpointConnectionsPut")
}

func (srv *Server) PrivateLinkResourcesListByVault(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupName, _ gen.VaultName, _ gen.PrivateLinkResourcesListByVaultParams) {
	notImplemented(w, "PrivateLinkResourcesListByVault")
}

// =====================================================================
// Helpers
// =====================================================================

// syntheticVault returns the canonical Microsoft.KeyVault/vaults
// response shape. The shim doesn't persist vault state — every
// Get/Create returns the same baseline derived from the request path.
// Soft-delete + retention defaults mirror what real Azure emits on
// vault create.
func (srv *Server) syntheticVault(subId gen.SubscriptionIdParameter, rg, name, location string) *gen.Vault {
	if location == "" {
		location = "eastus"
	}
	id := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.KeyVault/vaults/%s",
		subId, rg, name)
	typ := "Microsoft.KeyVault/vaults"
	enabledForDeployment := false
	enableSoftDelete := true
	softDeleteRetention := int32(90)
	tenant := openapi_types.UUID{} // zero UUID — the shim doesn't model tenants
	skuFamily := gen.A
	skuName := gen.Standard
	enabledForTemplateDeployment := false
	enabledForDiskEncryption := false
	return &gen.Vault{
		Id:       &id,
		Name:     &name,
		Type:     &typ,
		Location: &location,
		Properties: gen.VaultProperties{
			TenantId: tenant,
			Sku: gen.Sku{
				Family: skuFamily,
				Name:   skuName,
			},
			EnabledForDeployment:         &enabledForDeployment,
			EnabledForDiskEncryption:     &enabledForDiskEncryption,
			EnabledForTemplateDeployment: &enabledForTemplateDeployment,
			EnableSoftDelete:             &enableSoftDelete,
			SoftDeleteRetentionInDays:    &softDeleteRetention,
			VaultUri:                     ptr(srv.vaultURIFor(name)),
		},
	}
}

func ptr[T any](v T) *T { return &v }

// vaultURIFor returns the data-plane URI to advertise for vault
// `name`. If Options.VaultURI was set, it overrides the default
// `https://<name>.vault.azure.net/` shape.
func (srv *Server) vaultURIFor(name string) string {
	if srv.vaultURI != "" {
		if strings.HasSuffix(srv.vaultURI, "/") {
			return srv.vaultURI
		}
		return srv.vaultURI + "/"
	}
	return fmt.Sprintf("https://%s.vault.azure.net/", name)
}

// recordVault notes that VaultsCreateOrUpdate was called for `name`.
func (srv *Server) recordVault(name string) {
	if !srv.trackVaults {
		return
	}
	srv.mu.Lock()
	srv.vaults[name] = struct{}{}
	srv.mu.Unlock()
}

func (srv *Server) hasVault(name string) bool {
	srv.mu.Lock()
	_, ok := srv.vaults[name]
	srv.mu.Unlock()
	return ok
}

func (srv *Server) forgetVault(name string) {
	if !srv.trackVaults {
		return
	}
	srv.mu.Lock()
	delete(srv.vaults, name)
	srv.mu.Unlock()
}

// Compile-time guard: gen.ServerInterface must be fully implemented.
var _ gen.ServerInterface = (*Server)(nil)
