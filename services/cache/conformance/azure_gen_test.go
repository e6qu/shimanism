// Phase 12.A toolchain invariant: the cache service's Azure
// generated package compiles and exposes the spec-driven types
// that downstream adapters can decode/encode against. A regression
// here means cmd/azure-codegen broke the per-service codegen for
// this spec.
package conformance_test

import (
	"encoding/json"
	"reflect"
	"testing"

	azuregen "github.com/e6qu/shimanism/services/cache/gen/azure"
)

func TestAzureGen_Cache_PackageCompiles(t *testing.T) {
	// Sanity: at least one exported type must exist in the
	// generated package. ServerInterface is emitted by every
	// oapi-codegen std-net-http output.
	iface := reflect.TypeOf((*azuregen.ServerInterface)(nil)).Elem()
	if iface.Kind() != reflect.Interface {
		t.Fatalf("expected gen.ServerInterface to be an interface, got %s", iface.Kind())
	}
	// Azure Cache for Redis ARM spec declares ~40 operations
	// (Redis* + AccessPolicy* + AccessPolicyAssignment* +
	// AsyncOperationStatusGet + OperationsList + ...).
	if got := iface.NumMethod(); got < 30 {
		t.Errorf("ServerInterface has %d methods; want ≥30. Spec preprocessor may have dropped operations.", got)
	}
}

// TestAzureGen_Cache_RedisResourceDecodesRealShape pins the BUG-20
// fix: before 12.A.24, gen.RedisResource was a type alias to
// TrackedResource and the Location / Properties fields didn't exist
// on the alias. flattenARMAllOf now inlines TrackedResource's
// fields + the schema's own properties, so RedisResource decodes
// the canonical Azure REST request body.
func TestAzureGen_Cache_RedisResourceDecodesRealShape(t *testing.T) {
	body := []byte(`{
		"location": "eastus",
		"properties": {
			"enableNonSslPort": false,
			"minimumTlsVersion": "1.2",
			"sku": {
				"name": "Premium",
				"family": "P",
				"capacity": 1
			}
		}
	}`)
	var r azuregen.RedisResource
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode RedisResource: %v (BUG-20 may have regressed)", err)
	}
	if r.Location != "eastus" {
		t.Errorf("Location = %q; want eastus", r.Location)
	}
	if r.Properties.EnableNonSslPort == nil || *r.Properties.EnableNonSslPort {
		t.Error("Properties.EnableNonSslPort = nil/true; want false")
	}
	if r.Properties.Sku.Name != "Premium" {
		t.Errorf("Properties.Sku.Name = %q; want Premium", r.Properties.Sku.Name)
	}
}
