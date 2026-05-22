// Phase 12.A toolchain invariant: the functions service's Azure
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

	"github.com/e6qu/shimanism/internal/functions/frontends/azure_containerapps"
	"github.com/e6qu/shimanism/services/functions/backends/inmem"
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

// TestAzureGen_Functions_ContainerAppRoundTrips asserts that
// decoding then re-encoding then re-decoding a ContainerApp body
// preserves the original field set. Confirms gen.ContainerApp's
// JSON tags don't drop fields on the marshal side (a real risk
// after the allOf-flatten rewrite — properties merged into the
// local schema must keep their original json tag).
func TestAzureGen_Functions_ContainerAppRoundTrips(t *testing.T) {
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
	var first azuregen.ContainerApp
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatalf("first decode: %v", err)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var second azuregen.ContainerApp
	if err := json.Unmarshal(encoded, &second); err != nil {
		t.Fatalf("second decode (round-trip lost shape): %v\nencoded:\n%s", err, encoded)
	}
	// Spot-check key fields survived round-trip.
	if first.Location != second.Location {
		t.Errorf("Location lost: %q → %q", first.Location, second.Location)
	}
	if (first.Properties == nil) != (second.Properties == nil) {
		t.Fatalf("Properties nilness mismatch: first=%v second=%v",
			first.Properties != nil, second.Properties != nil)
	}
	if first.Properties != nil {
		if (first.Properties.ManagedEnvironmentId == nil) != (second.Properties.ManagedEnvironmentId == nil) {
			t.Error("ManagedEnvironmentId lost in round-trip")
		}
		if first.Properties.Template != nil && second.Properties.Template != nil {
			if first.Properties.Template.Containers != nil && second.Properties.Template.Containers != nil {
				if len(*first.Properties.Template.Containers) != len(*second.Properties.Template.Containers) {
					t.Error("Template.Containers length lost in round-trip")
				}
			}
		}
	}
}

// TestAzureGen_Functions_HandlerDispatch is the Phase 13.A.2
// acceptance: the azure_containerapps frontend dispatches through
// gen.HandlerWithOptions. A canonical ARM CreateOrUpdate request
// reaches ContainerAppsCreateOrUpdate on the Server, the in-memory
// backend stores the function, and the response decodes through
// gen.ContainerApp.
func TestAzureGen_Functions_HandlerDispatch(t *testing.T) {
	backend := inmem.New()
	srv := azure_containerapps.New(backend)

	body := []byte(`{
		"location": "eastus",
		"properties": {
			"template": {
				"containers": [{"name": "main", "image": "nginx:alpine"}]
			}
		}
	}`)
	req := httptest.NewRequest(http.MethodPut,
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.App/containerApps/shim-app?api-version=2024-08-01",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("CreateOrUpdate status = %d; want 201. body=%s", w.Code, w.Body.String())
	}
	var got azuregen.ContainerApp
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v\nbody=%s", err, w.Body.String())
	}
	if got.Name == nil || *got.Name != "shim-app" {
		t.Errorf("response.Name = %v; want shim-app", got.Name)
	}
	if got.Properties == nil || got.Properties.Template == nil || got.Properties.Template.Containers == nil {
		t.Fatal("response.Properties.Template.Containers missing")
	}
	if cs := *got.Properties.Template.Containers; len(cs) == 0 || cs[0].Image == nil || *cs[0].Image != "nginx:alpine" {
		t.Errorf("Containers[0].Image lost in round-trip: %+v", cs)
	}
}
