// Phase 12.A toolchain invariant: the cache service's Azure
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

	"github.com/e6qu/shimanism/internal/cache/frontends/azure_redis"
	"github.com/e6qu/shimanism/services/cache/backends/inmem"
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

// TestAzureGen_Cache_RedisResourceRoundTrips: decode → encode →
// decode → assert key fields survive. Confirms the JSON tags
// post-allOf-flatten still serialise correctly on the encode side.
func TestAzureGen_Cache_RedisResourceRoundTrips(t *testing.T) {
	body := []byte(`{
		"location": "eastus",
		"properties": {
			"enableNonSslPort": false,
			"minimumTlsVersion": "1.2",
			"sku": {"name": "Premium", "family": "P", "capacity": 1}
		}
	}`)
	var first azuregen.RedisResource
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatalf("first decode: %v", err)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var second azuregen.RedisResource
	if err := json.Unmarshal(encoded, &second); err != nil {
		t.Fatalf("second decode: %v\nencoded:\n%s", err, encoded)
	}
	if first.Location != second.Location {
		t.Errorf("Location lost: %q → %q", first.Location, second.Location)
	}
	if first.Properties.Sku.Name != second.Properties.Sku.Name {
		t.Errorf("Sku.Name lost: %q → %q", first.Properties.Sku.Name, second.Properties.Sku.Name)
	}
}

// TestAzureGen_Cache_HandlerDispatch is the Phase 13.A.1 acceptance:
// the azure_redis frontend now dispatches through gen.HandlerWithOptions
// instead of the prior hand-written regex. A canonical ARM Create
// request reaches RedisCreate on the Server, the in-memory backend
// stores the instance, and the response decodes through gen.RedisResource
// (post-BUG-20 struct shape — Location + Properties survive).
func TestAzureGen_Cache_HandlerDispatch(t *testing.T) {
	backend := inmem.New()
	srv := azure_redis.New(backend)

	body := []byte(`{
		"location": "eastus",
		"properties": {
			"redisVersion": "7.1",
			"sku": {"name": "Premium", "family": "P", "capacity": 1}
		}
	}`)
	req := httptest.NewRequest(http.MethodPut,
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Cache/redis/shim-cache?api-version=2024-11-01",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("RedisCreate status = %d; want 201. body=%s", w.Code, w.Body.String())
	}
	var got azuregen.RedisResource
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v\nbody=%s", err, w.Body.String())
	}
	if got.Name == nil || *got.Name != "shim-cache" {
		t.Errorf("response.Name = %v; want shim-cache", got.Name)
	}
	if got.Properties.Sku.Name != "Premium" {
		t.Errorf("response.Properties.Sku.Name = %q; want Premium", got.Properties.Sku.Name)
	}
}
