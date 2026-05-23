// Phase 12.A toolchain invariant: the rdbms service's Azure
// generated package compiles and exposes the spec-driven types
// that downstream adapters can decode/encode against. A regression
// here means cmd/azure-codegen broke the per-service codegen for
// this spec.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/e6qu/shimanism/internal/rdbms/frontends/azure_dbadmin"
	"github.com/e6qu/shimanism/services/rdbms/backends/inmem"
	azuregen "github.com/e6qu/shimanism/services/rdbms/gen/azure"
)

func TestAzureGen_Rdbms_PackageCompiles(t *testing.T) {
	// Sanity: at least one exported type must exist in the
	// generated package. ServerInterface is emitted by every
	// oapi-codegen std-net-http output.
	iface := reflect.TypeOf((*azuregen.ServerInterface)(nil)).Elem()
	if iface.Kind() != reflect.Interface {
		t.Fatalf("expected gen.ServerInterface to be an interface, got %s", iface.Kind())
	}
	// PostgreSQL FlexibleServers ARM spec — ~66 operations
	// across servers, databases, firewall rules, replicas,
	// backups, async operation status.
	if got := iface.NumMethod(); got < 50 {
		t.Errorf("ServerInterface has %d methods; want ≥50. Spec preprocessor may have dropped operations.", got)
	}
}

// TestAzureGen_Rdbms_ServerDecodesRealShape pins the BUG-20 fix:
// before 12.A.24, gen.Server was a type alias to TrackedResource
// and the Location / Properties / Sku fields didn't exist. Now
// the type is a proper struct and the canonical PostgreSQL
// FlexibleServer create request body decodes cleanly.
func TestAzureGen_Rdbms_ServerDecodesRealShape(t *testing.T) {
	body := []byte(`{
		"location": "eastus",
		"sku": {
			"name": "Standard_D2s_v3",
			"tier": "GeneralPurpose"
		},
		"properties": {
			"administratorLogin": "shimadmin",
			"administratorLoginPassword": "redacted",
			"availabilityZone": "1",
			"createMode": "Default",
			"version": "16"
		}
	}`)
	var s azuregen.Server
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("decode Server: %v (BUG-20 may have regressed)", err)
	}
	if s.Location != "eastus" {
		t.Errorf("Location = %q; want eastus", s.Location)
	}
	if s.Properties == nil {
		t.Fatal("Properties is nil — allOf flattening lost the schema's own fields")
	}
	if got := s.Properties.AdministratorLogin; got == nil || *got != "shimadmin" {
		t.Error("Properties.AdministratorLogin missing/wrong — gen type doesn't expose all ARM fields")
	}
	if s.Sku == nil || s.Sku.Name != "Standard_D2s_v3" {
		t.Error("Sku.Name missing/wrong — gen type missing the nested ARM structure")
	}
}

// TestAzureGen_Rdbms_ServerRoundTrips: decode → encode → decode →
// assert key fields survive. Confirms the JSON tags post-
// allOf-flatten still serialise correctly on the encode side.
func TestAzureGen_Rdbms_ServerRoundTrips(t *testing.T) {
	body := []byte(`{
		"location": "eastus",
		"sku": {"name": "Standard_D2s_v3", "tier": "GeneralPurpose"},
		"properties": {
			"administratorLogin": "shimadmin",
			"createMode": "Default",
			"version": "16"
		}
	}`)
	var first azuregen.Server
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatalf("first decode: %v", err)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var second azuregen.Server
	if err := json.Unmarshal(encoded, &second); err != nil {
		t.Fatalf("second decode: %v\nencoded:\n%s", err, encoded)
	}
	if first.Location != second.Location {
		t.Errorf("Location lost: %q → %q", first.Location, second.Location)
	}
	if first.Sku.Name != second.Sku.Name {
		t.Errorf("Sku.Name lost: %q → %q", first.Sku.Name, second.Sku.Name)
	}
	if first.Properties.AdministratorLogin == nil || second.Properties.AdministratorLogin == nil ||
		*first.Properties.AdministratorLogin != *second.Properties.AdministratorLogin {
		t.Error("Properties.AdministratorLogin lost in round-trip")
	}
}

// TestAzureGen_Rdbms_HandlerDispatch is the Phase 13.A.3 acceptance:
// the azure_dbadmin frontend dispatches through gen.HandlerWithOptions
// (this is the largest ARM gen interface in the project — 66 methods).
// A canonical PostgreSQL FlexibleServer Create request reaches
// ServersCreateOrUpdate on the Server, the in-memory backend stores
// the instance, and the response decodes through gen.Server.
func TestAzureGen_Rdbms_HandlerDispatch(t *testing.T) {
	backend := inmem.New()
	srv := azure_dbadmin.New(backend)

	body := []byte(`{
		"location": "eastus",
		"sku": {"name": "Standard_D2s_v3", "tier": "GeneralPurpose"},
		"properties": {
			"administratorLogin": "shimadmin",
			"administratorLoginPassword": "redacted",
			"version": "16",
			"storage": {"storageSizeGB": 32}
		}
	}`)
	req := httptest.NewRequest(http.MethodPut,
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/shim-pg?api-version=2024-08-01",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("ServersCreateOrUpdate status = %d; want 201. body=%s", w.Code, w.Body.String())
	}
	var got azuregen.Server
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v\nbody=%s", err, w.Body.String())
	}
	if got.Name == nil || *got.Name != "shim-pg" {
		t.Errorf("response.Name = %v; want shim-pg", got.Name)
	}
	if got.Sku == nil || got.Sku.Name != "Standard_D2s_v3" {
		t.Errorf("response.Sku.Name lost: %+v", got.Sku)
	}
	if got.Properties == nil || got.Properties.Version == nil || *got.Properties.Version != "16" {
		t.Errorf("response.Properties.Version = %v; want 16", got.Properties)
	}
}
