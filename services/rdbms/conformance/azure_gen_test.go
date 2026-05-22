// Phase 12.A toolchain invariant: the rdbms service's Azure
// generated package compiles and exposes the spec-driven types
// that downstream adapters can decode/encode against. A regression
// here means cmd/azure-codegen broke the per-service codegen for
// this spec.
package conformance_test

import (
	"encoding/json"
	"reflect"
	"testing"

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
