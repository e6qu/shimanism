// Phase 12.A toolchain invariant: the functions service's Azure
// generated package compiles and exposes the spec-driven types
// that downstream adapters can decode/encode against. A regression
// here means cmd/azure-codegen broke the per-service codegen for
// this spec.
package conformance_test

import (
	"encoding/json"
	"reflect"
	"testing"

	azuregen "github.com/e6qu/shimanism/services/functions/gen/azure"
)

func TestAzureGen_Functions_PackageCompiles(t *testing.T) {
	// Sanity: at least one exported type must exist in the
	// generated package. ServerInterface is emitted by every
	// oapi-codegen std-net-http output.
	iface := reflect.TypeOf((*azuregen.ServerInterface)(nil)).Elem()
	if iface.Kind() != reflect.Interface {
		t.Fatalf("expected gen.ServerInterface to be an interface, got %s", iface.Kind())
	}
	// Container Apps ARM spec declares ~11 operations on the
	// containerApps resource (list / create / get / update /
	// delete / start / stop / restart / GetAuthToken / ...).
	if got := iface.NumMethod(); got < 8 {
		t.Errorf("ServerInterface has %d methods; want ≥8. Spec preprocessor may have dropped operations.", got)
	}
}

// TestAzureGen_Functions_ContainerAppDecodesRealShape verifies that
// the gen.ContainerApp wire type decodes a realistic ARM Container
// App request body — confirming the BUG-20 allOf-flattening fix
// actually populated the schema's own Properties fields and the
// gen type is adapter-migration-ready.
//
// Before 12.A.24, gen.ContainerApp was a type alias to
// TrackedResource (alloy was dropping the schema's properties);
// decoding this body would have lost Location + Properties.
// Today the gen type is a proper struct with all the fields.
func TestAzureGen_Functions_ContainerAppDecodesRealShape(t *testing.T) {
	// Minimal but realistic Create ContainerApp request body.
	// Matches the shape the hashicorp/azurerm provider sends.
	body := []byte(`{
		"location": "eastus",
		"properties": {
			"managedEnvironmentId": "/subscriptions/s/resourceGroups/rg/providers/Microsoft.App/managedEnvironments/env",
			"configuration": {
				"ingress": {
					"external": true,
					"targetPort": 8080
				}
			},
			"template": {
				"containers": [{
					"name": "main",
					"image": "nginx:alpine"
				}]
			}
		}
	}`)
	var app azuregen.ContainerApp
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode ContainerApp: %v (BUG-20 may have regressed — type might be aliasing TrackedResource again)", err)
	}
	if app.Location != "eastus" {
		t.Errorf("Location = %q; want eastus", app.Location)
	}
	if app.Properties == nil {
		t.Fatal("Properties is nil; allOf flattening lost the schema's own fields (BUG-20)")
	}
	if got := app.Properties.ManagedEnvironmentId; got == nil || *got == "" {
		t.Error("Properties.ManagedEnvironmentId missing — gen type doesn't expose all ARM fields")
	}
	if app.Properties.Template == nil || len(*app.Properties.Template.Containers) == 0 {
		t.Error("Properties.Template.Containers missing — gen type missing the nested ARM structure")
	}
}
