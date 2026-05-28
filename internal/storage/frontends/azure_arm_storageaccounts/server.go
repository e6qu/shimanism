// Package azure_arm_storageaccounts is the Azure Storage ARM
// (Microsoft.Storage/storageAccounts) frontend.
//
// ARM URL shape (path-routed by the gen mux):
//
//	/subscriptions/{subscriptionId}
//	  /resourceGroups/{resourceGroupName}
//	    /providers/Microsoft.Storage/storageAccounts/{accountName}
//	    /providers/Microsoft.Storage/storageAccounts/{accountName}/blobServices/default/containers/{containerName}
//
// **Account routing is fiction.** The shim's domain.Storage has no
// "account" concept — buckets/containers live in a flat namespace on
// the destination backend. Storage-account ARM operations are
// acknowledged without backend interaction: Create returns a
// synthetic StorageAccount resource, Get always returns "exists",
// Delete is a no-op success, List returns empty (the shim can't
// enumerate without state). Subsequent data-plane calls (the
// existing `azure_blob` frontend) already strip the account prefix
// from the host header. The cross-cloud Terraform Apply flow this
// unblocks: `azurerm_storage_account` PUT → ARM acknowledges →
// `azurerm_storage_container` PUT → ARM bridges to
// `domain.Storage.CreateBucket` → `azurerm_storage_blob` PUT →
// data-plane azure_blob frontend → backend.
//
// In-intersection (11):
//   - StorageAccountsCreate / GetProperties / Update / Delete / List /
//     ListByResourceGroup / CheckNameAvailability — all stateless
//     acknowledgements.
//   - BlobContainersCreate / Get / Delete / List — bridge to
//     domain.Storage.{CreateBucket, HeadBucket, DeleteBucket, ListBuckets}.
//
// Out-of-intersection (109): every other Microsoft.Storage operation
// (encryption scopes, blob inventory, lifecycle management, queue/
// file/table services, network rules, private endpoints, SAS / keys,
// etc.) returns the ARM error envelope via notImplemented.
package azure_arm_storageaccounts

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/storage/domain"
	gen "github.com/e6qu/shimanism/services/storage/gen/azure_arm"
)

// Server is the Microsoft.Storage ARM-shaped HTTP frontend.
type Server struct {
	s             domain.Storage
	mux           http.Handler
	blobEndpoint  string // optional override returned in PrimaryEndpoints.Blob
	trackAccounts bool   // when true, GetProperties returns 404 if no PUT seen
	mu            sync.Mutex
	accounts      map[string]struct{} // account names with at least one PUT
}

// Options configures the ARM frontend.
type Options struct {
	// BlobEndpoint, when non-empty, is returned verbatim in
	// `properties.primaryEndpoints.blob` on every StorageAccount
	// response. The `hashicorp/azurerm` Terraform provider reads
	// this field at apply time to derive the blob data-plane URL
	// for resources that depend on the account (azurerm_storage_container,
	// azurerm_storage_blob, etc.). Leave empty to fall back to the
	// real-Azure-shaped `https://<account>.blob.core.windows.net/`
	// default.
	BlobEndpoint string

	// TrackAccounts, when true, makes the frontend maintain a tiny
	// in-process set of account names that have been PUT to this
	// instance. GetProperties returns 404 before any PUT, the
	// synthetic resource after. azurerm does a pre-create existence
	// check (GET → 404 means "ok to create") and would otherwise
	// see the always-200 default and refuse to create.
	//
	// This is opt-in test-mode state. In production the frontend
	// stays stateless: TrackAccounts defaults to false, and the
	// shim's invariant ("no state of record") is preserved. The
	// `hashicorp/azurerm` Terraform conformance tests opt in via
	// the harness; SDK-driven tests don't need to.
	TrackAccounts bool
}

// New returns a frontend bound to the given backend.
func New(s domain.Storage, opts ...Options) *Server {
	srv := &Server{s: s}
	if len(opts) > 0 {
		srv.blobEndpoint = opts[0].BlobEndpoint
		srv.trackAccounts = opts[0].TrackAccounts
	}
	if srv.trackAccounts {
		srv.accounts = map[string]struct{}{}
	}
	srv.mux = gen.HandlerWithOptions(srv, gen.StdHTTPServerOptions{})
	return srv
}

// ServeHTTP delegates to the gen-generated routing layer.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	srv.mux.ServeHTTP(w, r)
}

// notImplemented writes the ARM "operation not supported" envelope
// for spec-defined operations the cross-cloud storage intersection
// doesn't carry.
func notImplemented(w http.ResponseWriter, op string) {
	writeError(w, http.StatusNotImplemented, "OperationNotSupported",
		op+" is not in the cross-cloud storage intersection")
}

// =====================================================================
// In-intersection handlers — StorageAccounts (acknowledged statelessly)
// =====================================================================

func (srv *Server) StorageAccountsCreate(w http.ResponseWriter, r *http.Request, subscriptionId gen.SubscriptionIdParameter, resourceGroupName gen.ResourceGroupNameParameter, accountName string, _ gen.StorageAccountsCreateParams) {
	var body gen.StorageAccountCreateParameters
	if !decodeJSON(w, r, &body) {
		return
	}
	srv.recordAccount(accountName)
	writeJSON(w, http.StatusOK, srv.syntheticStorageAccount(subscriptionId, resourceGroupName, accountName, body.Location))
}

func (srv *Server) StorageAccountsGetProperties(w http.ResponseWriter, _ *http.Request, subscriptionId gen.SubscriptionIdParameter, resourceGroupName gen.ResourceGroupNameParameter, accountName string, _ gen.StorageAccountsGetPropertiesParams) {
	if srv.trackAccounts && !srv.hasAccount(accountName) {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.Storage/storageAccounts/"+accountName+"' under resource group '"+string(resourceGroupName)+"' was not found.")
		return
	}
	writeJSON(w, http.StatusOK, srv.syntheticStorageAccount(subscriptionId, resourceGroupName, accountName, ""))
}

func (srv *Server) StorageAccountsUpdate(w http.ResponseWriter, r *http.Request, subscriptionId gen.SubscriptionIdParameter, resourceGroupName gen.ResourceGroupNameParameter, accountName string, _ gen.StorageAccountsUpdateParams) {
	// Drain body for fidelity; ignore contents (shim doesn't persist).
	_ = decodeJSON(w, r, &struct{}{})
	writeJSON(w, http.StatusOK, srv.syntheticStorageAccount(subscriptionId, resourceGroupName, accountName, ""))
}

func (srv *Server) StorageAccountsDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, accountName string, _ gen.StorageAccountsDeleteParams) {
	srv.forgetAccount(accountName)
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) StorageAccountsList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.StorageAccountsListParams) {
	writeJSON(w, http.StatusOK, gen.StorageAccountListResult{Value: []gen.StorageAccount{}})
}

func (srv *Server) StorageAccountsListByResourceGroup(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ gen.StorageAccountsListByResourceGroupParams) {
	writeJSON(w, http.StatusOK, gen.StorageAccountListResult{Value: []gen.StorageAccount{}})
}

func (srv *Server) StorageAccountsCheckNameAvailability(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.StorageAccountsCheckNameAvailabilityParams) {
	var body gen.StorageAccountCheckNameAvailabilityParameters
	if !decodeJSON(w, r, &body) {
		return
	}
	available := true
	writeJSON(w, http.StatusOK, gen.CheckNameAvailabilityResult{NameAvailable: &available})
}

// =====================================================================
// In-intersection handlers — BlobContainers (bridge to domain.Storage)
// =====================================================================

func (srv *Server) BlobContainersCreate(w http.ResponseWriter, r *http.Request, subscriptionId gen.SubscriptionIdParameter, resourceGroupName gen.ResourceGroupNameParameter, accountName string, containerName string, _ gen.BlobContainersCreateParams) {
	if err := srv.s.CreateBucket(r.Context(), containerName, ""); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, syntheticContainerItem(subscriptionId, resourceGroupName, accountName, containerName))
}

func (srv *Server) BlobContainersGet(w http.ResponseWriter, r *http.Request, subscriptionId gen.SubscriptionIdParameter, resourceGroupName gen.ResourceGroupNameParameter, accountName string, containerName string, _ gen.BlobContainersGetParams) {
	if _, err := srv.s.HeadBucket(r.Context(), containerName); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, syntheticContainerItem(subscriptionId, resourceGroupName, accountName, containerName))
}

func (srv *Server) BlobContainersDelete(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, containerName string, _ gen.BlobContainersDeleteParams) {
	if err := srv.s.DeleteBucket(r.Context(), containerName); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) BlobContainersList(w http.ResponseWriter, r *http.Request, subscriptionId gen.SubscriptionIdParameter, resourceGroupName gen.ResourceGroupNameParameter, accountName string, _ gen.BlobContainersListParams) {
	out, err := srv.s.ListBuckets(r.Context(), domain.ListBucketsOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	items := make([]gen.ListContainerItem, 0, len(out.Buckets))
	for _, b := range out.Buckets {
		items = append(items, syntheticContainerItem(subscriptionId, resourceGroupName, accountName, b.Name))
	}
	writeJSON(w, http.StatusOK, gen.ListContainerItems{Value: &items})
}

// =====================================================================
// Helpers
// =====================================================================

// syntheticStorageAccount returns the canonical Microsoft.Storage/
// storageAccounts response shape — the shim doesn't persist account
// state, so every Get/Create/Update returns the same baseline derived
// from the request path. Honest defaults: a Standard_LRS, hot-tier,
// StorageV2 account at the requested location. Provisioning state is
// "Succeeded" because the shim doesn't run async; the create
// completes synchronously from the caller's POV.
//
// `PrimaryEndpoints.Blob` is populated from `srv.blobEndpoint` if
// set; otherwise the real-Azure-shaped default
// `https://<account>.blob.core.windows.net/` is emitted. The
// hashicorp/azurerm Terraform provider reads this field at apply
// time to derive the blob data-plane URL for resources that depend
// on the account (azurerm_storage_container, azurerm_storage_blob,
// etc.) — populating it with the shim's blob frontend URL is what
// makes through-shim Terraform Apply work.
func (srv *Server) syntheticStorageAccount(subId gen.SubscriptionIdParameter, rg gen.ResourceGroupNameParameter, name, location string) *gen.StorageAccount {
	if location == "" {
		location = "eastus"
	}
	id := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s",
		subId, rg, name)
	typ := "Microsoft.Storage/storageAccounts"
	skuName := gen.SkuNameStandardLRS
	kind := gen.StorageV2
	created := time.Now().UTC()
	state := gen.ProvisioningStateSucceeded
	primary := gen.AccountStatusAvailable
	blobURL := srv.blobEndpoint
	if blobURL == "" {
		blobURL = fmt.Sprintf("https://%s.blob.core.windows.net/", name)
	} else if !strings.HasSuffix(blobURL, "/") {
		blobURL += "/"
	}
	return &gen.StorageAccount{
		Id:       &id,
		Name:     &name,
		Type:     &typ,
		Location: location,
		Sku: &gen.Sku{
			Name: skuName,
		},
		Kind: &kind,
		Properties: &gen.StorageAccountProperties{
			ProvisioningState:        &state,
			CreationTime:             &created,
			StatusOfPrimary:          &primary,
			PrimaryLocation:          &location,
			AllowBlobPublicAccess:    ptr(false),
			SupportsHttpsTrafficOnly: ptr(true),
			PrimaryEndpoints: &gen.Endpoints{
				Blob: &blobURL,
			},
		},
	}
}

// syntheticContainerItem returns the Microsoft.Storage/storageAccounts/
// blobServices/default/containers response shape for a single
// container. Real Azure populates Etag + LastModifiedTime + LeaseStatus
// — the shim emits stable synthetic values since the backend doesn't
// carry container-level metadata at the ARM granularity.
func syntheticContainerItem(subId gen.SubscriptionIdParameter, rg gen.ResourceGroupNameParameter, account, container string) gen.ListContainerItem {
	id := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s/blobServices/default/containers/%s",
		subId, rg, account, container)
	typ := "Microsoft.Storage/storageAccounts/blobServices/containers"
	now := time.Now().UTC()
	state := gen.Available
	status := gen.LeaseStatusUnlocked
	publicAccess := gen.PublicAccessNone
	return gen.ListContainerItem{
		Id:   &id,
		Name: &container,
		Type: &typ,
		Properties: &gen.ContainerProperties{
			LastModifiedTime: &now,
			LeaseStatus:      &status,
			LeaseState:       &state,
			PublicAccess:     &publicAccess,
		},
	}
}

func ptr[T any](v T) *T { return &v }

// recordAccount notes that StorageAccountsCreate was called for
// `name`. No-op when TrackAccounts is false.
func (srv *Server) recordAccount(name string) {
	if !srv.trackAccounts {
		return
	}
	srv.mu.Lock()
	srv.accounts[name] = struct{}{}
	srv.mu.Unlock()
}

// hasAccount reports whether `name` has been PUT. Only meaningful
// when TrackAccounts is true; the caller's check is gated on that.
func (srv *Server) hasAccount(name string) bool {
	srv.mu.Lock()
	_, ok := srv.accounts[name]
	srv.mu.Unlock()
	return ok
}

// forgetAccount removes `name` from the tracked set so subsequent
// GETs return 404. Called on StorageAccountsDelete.
func (srv *Server) forgetAccount(name string) {
	if !srv.trackAccounts {
		return
	}
	srv.mu.Lock()
	delete(srv.accounts, name)
	srv.mu.Unlock()
}

// Compile-time guard: gen.ServerInterface must be fully implemented.
var _ gen.ServerInterface = (*Server)(nil)

// StorageAccountsListKeys returns the shim's static SharedKey
// credentials. azurerm calls this immediately after PUT to obtain
// the access key it uses for SharedKey auth on the blob data plane.
// The shim's `azure_blob` data-plane frontend's azuresharedkey
// verifier is configured with the same base64-encoded key (32 bytes
// of "k") in the harness — see `StartStorageServerAzureBlob`.
//
// Returning synthetic keys is honest: the shim doesn't manage
// account-level secrets in production; in test mode it serves the
// static key the data-plane verifier is configured to accept, so
// the whole Terraform Apply cycle composes.
func (srv *Server) StorageAccountsListKeys(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageAccountsListKeysParams) {
	// Matches `azuresharedkey.StaticStore{Key: bytes.Repeat([]byte("k"), 32)}`
	// in StartStorageServerAzureBlob. Base64-encode for the
	// wire format ARM uses.
	keyValue := "a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s="
	keyName := "key1"
	perm := gen.KeyPermissionFull
	now := time.Now().UTC()
	writeJSON(w, http.StatusOK, gen.StorageAccountListKeysResult{
		Keys: &[]gen.StorageAccountKey{{
			KeyName:      &keyName,
			Value:        &keyValue,
			Permissions:  &perm,
			CreationTime: &now,
		}},
	})
}

// =====================================================================
// Out-of-intersection stubs (109)
// =====================================================================

func (srv *Server) OperationsList(w http.ResponseWriter, _ *http.Request, _ gen.OperationsListParams) {
	notImplemented(w, "OperationsList")
}

func (srv *Server) DeletedAccountsList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.DeletedAccountsListParams) {
	notImplemented(w, "DeletedAccountsList")
}

func (srv *Server) DeletedAccountsGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.LocationParameter, _ string, _ gen.DeletedAccountsGetParams) {
	notImplemented(w, "DeletedAccountsGet")
}

func (srv *Server) UsagesListByLocation(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.LocationParameter, _ gen.UsagesListByLocationParams) {
	notImplemented(w, "UsagesListByLocation")
}

func (srv *Server) SkusList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.SkusListParams) {
	notImplemented(w, "SkusList")
}

func (srv *Server) StorageAccountsAbortHierarchicalNamespaceMigration(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageAccountsAbortHierarchicalNamespaceMigrationParams) {
	notImplemented(w, "StorageAccountsAbortHierarchicalNamespaceMigration")
}

func (srv *Server) StorageAccountsGetCustomerInitiatedMigration(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageAccountsGetCustomerInitiatedMigrationParamsMigrationName, _ gen.StorageAccountsGetCustomerInitiatedMigrationParams) {
	notImplemented(w, "StorageAccountsGetCustomerInitiatedMigration")
}

func (srv *Server) AdvancedPlatformMetricsList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.AdvancedPlatformMetricsListParams) {
	notImplemented(w, "AdvancedPlatformMetricsList")
}

func (srv *Server) AdvancedPlatformMetricsDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.AdvancedPlatformMetricsDeleteParamsAdvancedPlatformMetricsRuleType, _ gen.AdvancedPlatformMetricsDeleteParams) {
	notImplemented(w, "AdvancedPlatformMetricsDelete")
}

func (srv *Server) AdvancedPlatformMetricsGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.AdvancedPlatformMetricsGetParamsAdvancedPlatformMetricsRuleType, _ gen.AdvancedPlatformMetricsGetParams) {
	notImplemented(w, "AdvancedPlatformMetricsGet")
}

func (srv *Server) AdvancedPlatformMetricsCreateOrUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.AdvancedPlatformMetricsCreateOrUpdateParamsAdvancedPlatformMetricsRuleType, _ gen.AdvancedPlatformMetricsCreateOrUpdateParams) {
	notImplemented(w, "AdvancedPlatformMetricsCreateOrUpdate")
}

func (srv *Server) BlobServicesList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.BlobServicesListParams) {
	notImplemented(w, "BlobServicesList")
}

func (srv *Server) BlobServicesGetServiceProperties(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.BlobServicesGetServicePropertiesParams) {
	notImplemented(w, "BlobServicesGetServiceProperties")
}

func (srv *Server) BlobServicesSetServiceProperties(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.BlobServicesSetServicePropertiesParams) {
	notImplemented(w, "BlobServicesSetServiceProperties")
}

func (srv *Server) BlobContainersUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.BlobContainersUpdateParams) {
	notImplemented(w, "BlobContainersUpdate")
}

func (srv *Server) BlobContainersClearLegalHold(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.BlobContainersClearLegalHoldParams) {
	notImplemented(w, "BlobContainersClearLegalHold")
}

func (srv *Server) BlobContainersDeleteImmutabilityPolicy(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.BlobContainersDeleteImmutabilityPolicyParams) {
	notImplemented(w, "BlobContainersDeleteImmutabilityPolicy")
}

func (srv *Server) BlobContainersGetImmutabilityPolicy(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.BlobContainersGetImmutabilityPolicyParams) {
	notImplemented(w, "BlobContainersGetImmutabilityPolicy")
}

func (srv *Server) BlobContainersCreateOrUpdateImmutabilityPolicy(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.BlobContainersCreateOrUpdateImmutabilityPolicyParams) {
	notImplemented(w, "BlobContainersCreateOrUpdateImmutabilityPolicy")
}

func (srv *Server) BlobContainersExtendImmutabilityPolicy(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.BlobContainersExtendImmutabilityPolicyParams) {
	notImplemented(w, "BlobContainersExtendImmutabilityPolicy")
}

func (srv *Server) BlobContainersLockImmutabilityPolicy(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.BlobContainersLockImmutabilityPolicyParams) {
	notImplemented(w, "BlobContainersLockImmutabilityPolicy")
}

func (srv *Server) BlobContainersLease(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.BlobContainersLeaseParams) {
	notImplemented(w, "BlobContainersLease")
}

func (srv *Server) BlobContainersObjectLevelWorm(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.BlobContainersObjectLevelWormParams) {
	notImplemented(w, "BlobContainersObjectLevelWorm")
}

func (srv *Server) BlobContainersSetLegalHold(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.BlobContainersSetLegalHoldParams) {
	notImplemented(w, "BlobContainersSetLegalHold")
}

func (srv *Server) ConnectorsListByStorageAccount(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ConnectorsListByStorageAccountParams) {
	notImplemented(w, "ConnectorsListByStorageAccount")
}

func (srv *Server) ConnectorsDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.ConnectorsDeleteParams) {
	notImplemented(w, "ConnectorsDelete")
}

func (srv *Server) ConnectorsGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.ConnectorsGetParams) {
	notImplemented(w, "ConnectorsGet")
}

func (srv *Server) ConnectorsUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.ConnectorsUpdateParams) {
	notImplemented(w, "ConnectorsUpdate")
}

func (srv *Server) ConnectorsCreate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.ConnectorsCreateParams) {
	notImplemented(w, "ConnectorsCreate")
}

func (srv *Server) ConnectorsTestExistingConnection(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.ConnectorsTestExistingConnectionParams) {
	notImplemented(w, "ConnectorsTestExistingConnection")
}

func (srv *Server) DataSharesListByStorageAccount(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.DataSharesListByStorageAccountParams) {
	notImplemented(w, "DataSharesListByStorageAccount")
}

func (srv *Server) DataSharesDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.DataSharesDeleteParams) {
	notImplemented(w, "DataSharesDelete")
}

func (srv *Server) DataSharesGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.DataSharesGetParams) {
	notImplemented(w, "DataSharesGet")
}

func (srv *Server) DataSharesUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.DataSharesUpdateParams) {
	notImplemented(w, "DataSharesUpdate")
}

func (srv *Server) DataSharesCreate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.DataSharesCreateParams) {
	notImplemented(w, "DataSharesCreate")
}

func (srv *Server) EncryptionScopesList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.EncryptionScopesListParams) {
	notImplemented(w, "EncryptionScopesList")
}

func (srv *Server) EncryptionScopesGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.EncryptionScopesGetParams) {
	notImplemented(w, "EncryptionScopesGet")
}

func (srv *Server) EncryptionScopesPatch(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.EncryptionScopesPatchParams) {
	notImplemented(w, "EncryptionScopesPatch")
}

func (srv *Server) EncryptionScopesPut(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.EncryptionScopesPutParams) {
	notImplemented(w, "EncryptionScopesPut")
}

func (srv *Server) StorageAccountsFailover(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageAccountsFailoverParams) {
	notImplemented(w, "StorageAccountsFailover")
}

func (srv *Server) FileServicesList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.FileServicesListParams) {
	notImplemented(w, "FileServicesList")
}

func (srv *Server) FileServicesGetServiceProperties(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.FileServicesGetServicePropertiesParams) {
	notImplemented(w, "FileServicesGetServiceProperties")
}

func (srv *Server) FileServicesSetServiceProperties(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.FileServicesSetServicePropertiesParams) {
	notImplemented(w, "FileServicesSetServiceProperties")
}

func (srv *Server) FileSharesList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.FileSharesListParams) {
	notImplemented(w, "FileSharesList")
}

func (srv *Server) FileSharesDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.FileSharesDeleteParams) {
	notImplemented(w, "FileSharesDelete")
}

func (srv *Server) FileSharesGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.FileSharesGetParams) {
	notImplemented(w, "FileSharesGet")
}

func (srv *Server) FileSharesUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.FileSharesUpdateParams) {
	notImplemented(w, "FileSharesUpdate")
}

func (srv *Server) FileSharesCreate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.FileSharesCreateParams) {
	notImplemented(w, "FileSharesCreate")
}

func (srv *Server) FileSharesLease(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.FileSharesLeaseParams) {
	notImplemented(w, "FileSharesLease")
}

func (srv *Server) FileSharesRestore(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.FileSharesRestoreParams) {
	notImplemented(w, "FileSharesRestore")
}

func (srv *Server) FileServicesListServiceUsages(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.FileServicesListServiceUsagesParams) {
	notImplemented(w, "FileServicesListServiceUsages")
}

func (srv *Server) FileServicesGetServiceUsage(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.FileServicesGetServiceUsageParams) {
	notImplemented(w, "FileServicesGetServiceUsage")
}

func (srv *Server) StorageAccountsHierarchicalNamespaceMigration(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageAccountsHierarchicalNamespaceMigrationParams) {
	notImplemented(w, "StorageAccountsHierarchicalNamespaceMigration")
}

func (srv *Server) BlobInventoryPoliciesList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.BlobInventoryPoliciesListParams) {
	notImplemented(w, "BlobInventoryPoliciesList")
}

func (srv *Server) BlobInventoryPoliciesDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.BlobInventoryPoliciesDeleteParamsBlobInventoryPolicyName, _ gen.BlobInventoryPoliciesDeleteParams) {
	notImplemented(w, "BlobInventoryPoliciesDelete")
}

func (srv *Server) BlobInventoryPoliciesGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.BlobInventoryPoliciesGetParamsBlobInventoryPolicyName, _ gen.BlobInventoryPoliciesGetParams) {
	notImplemented(w, "BlobInventoryPoliciesGet")
}

func (srv *Server) BlobInventoryPoliciesCreateOrUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.BlobInventoryPoliciesCreateOrUpdateParamsBlobInventoryPolicyName, _ gen.BlobInventoryPoliciesCreateOrUpdateParams) {
	notImplemented(w, "BlobInventoryPoliciesCreateOrUpdate")
}

func (srv *Server) StorageAccountsListAccountSAS(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageAccountsListAccountSASParams) {
	notImplemented(w, "StorageAccountsListAccountSAS")
}

func (srv *Server) StorageAccountsListServiceSAS(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageAccountsListServiceSASParams) {
	notImplemented(w, "StorageAccountsListServiceSAS")
}

func (srv *Server) LocalUsersList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.LocalUsersListParams) {
	notImplemented(w, "LocalUsersList")
}

func (srv *Server) LocalUsersDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.LocalUsersDeleteParams) {
	notImplemented(w, "LocalUsersDelete")
}

func (srv *Server) LocalUsersGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.LocalUsersGetParams) {
	notImplemented(w, "LocalUsersGet")
}

func (srv *Server) LocalUsersCreateOrUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.LocalUsersCreateOrUpdateParams) {
	notImplemented(w, "LocalUsersCreateOrUpdate")
}

func (srv *Server) LocalUsersListKeys(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.LocalUsersListKeysParams) {
	notImplemented(w, "LocalUsersListKeys")
}

func (srv *Server) LocalUsersRegeneratePassword(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.LocalUsersRegeneratePasswordParams) {
	notImplemented(w, "LocalUsersRegeneratePassword")
}

func (srv *Server) ManagementPoliciesDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ManagementPoliciesDeleteParamsManagementPolicyName, _ gen.ManagementPoliciesDeleteParams) {
	notImplemented(w, "ManagementPoliciesDelete")
}

func (srv *Server) ManagementPoliciesGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ManagementPoliciesGetParamsManagementPolicyName, _ gen.ManagementPoliciesGetParams) {
	notImplemented(w, "ManagementPoliciesGet")
}

func (srv *Server) ManagementPoliciesCreateOrUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ManagementPoliciesCreateOrUpdateParamsManagementPolicyName, _ gen.ManagementPoliciesCreateOrUpdateParams) {
	notImplemented(w, "ManagementPoliciesCreateOrUpdate")
}

func (srv *Server) NetworkSecurityPerimeterConfigurationsList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.NetworkSecurityPerimeterConfigurationsListParams) {
	notImplemented(w, "NetworkSecurityPerimeterConfigurationsList")
}

func (srv *Server) NetworkSecurityPerimeterConfigurationsGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.NetworkSecurityPerimeterConfigurationsGetParams) {
	notImplemented(w, "NetworkSecurityPerimeterConfigurationsGet")
}

func (srv *Server) NetworkSecurityPerimeterConfigurationsReconcile(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.NetworkSecurityPerimeterConfigurationsReconcileParams) {
	notImplemented(w, "NetworkSecurityPerimeterConfigurationsReconcile")
}

func (srv *Server) ObjectReplicationPoliciesList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ObjectReplicationPoliciesListParams) {
	notImplemented(w, "ObjectReplicationPoliciesList")
}

func (srv *Server) ObjectReplicationPoliciesDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.ObjectReplicationPoliciesDeleteParams) {
	notImplemented(w, "ObjectReplicationPoliciesDelete")
}

func (srv *Server) ObjectReplicationPoliciesGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.ObjectReplicationPoliciesGetParams) {
	notImplemented(w, "ObjectReplicationPoliciesGet")
}

func (srv *Server) ObjectReplicationPoliciesCreateOrUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.ObjectReplicationPoliciesCreateOrUpdateParams) {
	notImplemented(w, "ObjectReplicationPoliciesCreateOrUpdate")
}

func (srv *Server) PrivateEndpointConnectionsList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PrivateEndpointConnectionsListParams) {
	notImplemented(w, "PrivateEndpointConnectionsList")
}

func (srv *Server) PrivateEndpointConnectionsDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.PrivateEndpointConnectionsDeleteParams) {
	notImplemented(w, "PrivateEndpointConnectionsDelete")
}

func (srv *Server) PrivateEndpointConnectionsGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.PrivateEndpointConnectionsGetParams) {
	notImplemented(w, "PrivateEndpointConnectionsGet")
}

func (srv *Server) PrivateEndpointConnectionsPut(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.PrivateEndpointConnectionsPutParams) {
	notImplemented(w, "PrivateEndpointConnectionsPut")
}

func (srv *Server) PrivateLinkResourcesListByStorageAccount(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.PrivateLinkResourcesListByStorageAccountParams) {
	notImplemented(w, "PrivateLinkResourcesListByStorageAccount")
}

func (srv *Server) QueueServicesList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.QueueServicesListParams) {
	notImplemented(w, "QueueServicesList")
}

func (srv *Server) QueueServicesGetServiceProperties(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.QueueServicesGetServicePropertiesParams) {
	notImplemented(w, "QueueServicesGetServiceProperties")
}

func (srv *Server) QueueServicesSetServiceProperties(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.QueueServicesSetServicePropertiesParams) {
	notImplemented(w, "QueueServicesSetServiceProperties")
}

func (srv *Server) QueueList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.QueueListParams) {
	notImplemented(w, "QueueList")
}

func (srv *Server) QueueDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.QueueDeleteParams) {
	notImplemented(w, "QueueDelete")
}

func (srv *Server) QueueGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.QueueGetParams) {
	notImplemented(w, "QueueGet")
}

func (srv *Server) QueueUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.QueueUpdateParams) {
	notImplemented(w, "QueueUpdate")
}

func (srv *Server) QueueCreate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.QueueCreateParams) {
	notImplemented(w, "QueueCreate")
}

func (srv *Server) StorageAccountsRegenerateKey(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageAccountsRegenerateKeyParams) {
	notImplemented(w, "StorageAccountsRegenerateKey")
}

func (srv *Server) StorageTaskAssignmentsInstancesReportList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageTaskAssignmentsInstancesReportListParams) {
	notImplemented(w, "StorageTaskAssignmentsInstancesReportList")
}

func (srv *Server) StorageAccountsRestoreBlobRanges(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageAccountsRestoreBlobRangesParams) {
	notImplemented(w, "StorageAccountsRestoreBlobRanges")
}

func (srv *Server) StorageAccountsRevokeUserDelegationKeys(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageAccountsRevokeUserDelegationKeysParams) {
	notImplemented(w, "StorageAccountsRevokeUserDelegationKeys")
}

func (srv *Server) StorageAccountsCustomerInitiatedMigration(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageAccountsCustomerInitiatedMigrationParams) {
	notImplemented(w, "StorageAccountsCustomerInitiatedMigration")
}

func (srv *Server) StorageTaskAssignmentsList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.StorageTaskAssignmentsListParams) {
	notImplemented(w, "StorageTaskAssignmentsList")
}

func (srv *Server) StorageTaskAssignmentsDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.StorageTaskAssignmentsDeleteParams) {
	notImplemented(w, "StorageTaskAssignmentsDelete")
}

func (srv *Server) StorageTaskAssignmentsGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.StorageTaskAssignmentsGetParams) {
	notImplemented(w, "StorageTaskAssignmentsGet")
}

func (srv *Server) StorageTaskAssignmentsUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.StorageTaskAssignmentsUpdateParams) {
	notImplemented(w, "StorageTaskAssignmentsUpdate")
}

func (srv *Server) StorageTaskAssignmentsCreate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.StorageTaskAssignmentsCreateParams) {
	notImplemented(w, "StorageTaskAssignmentsCreate")
}

func (srv *Server) StorageTaskAssignmentInstancesReportList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.StorageTaskAssignmentInstancesReportListParams) {
	notImplemented(w, "StorageTaskAssignmentInstancesReportList")
}

func (srv *Server) StorageTaskAssignmentsStopAssignment(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.StorageTaskAssignmentsStopAssignmentParams) {
	notImplemented(w, "StorageTaskAssignmentsStopAssignment")
}

func (srv *Server) TableServicesList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.TableServicesListParams) {
	notImplemented(w, "TableServicesList")
}

func (srv *Server) TableServicesGetServiceProperties(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.TableServicesGetServicePropertiesParams) {
	notImplemented(w, "TableServicesGetServiceProperties")
}

func (srv *Server) TableServicesSetServiceProperties(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.TableServicesSetServicePropertiesParams) {
	notImplemented(w, "TableServicesSetServiceProperties")
}

func (srv *Server) TableList(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.TableListParams) {
	notImplemented(w, "TableList")
}

func (srv *Server) TableDelete(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.TableDeleteParams) {
	notImplemented(w, "TableDelete")
}

func (srv *Server) TableGet(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.TableGetParams) {
	notImplemented(w, "TableGet")
}

func (srv *Server) TableUpdate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.TableUpdateParams) {
	notImplemented(w, "TableUpdate")
}

func (srv *Server) TableCreate(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ string, _ gen.TableCreateParams) {
	notImplemented(w, "TableCreate")
}
