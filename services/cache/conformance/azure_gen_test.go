// Phase 12.A toolchain invariant: the cache service's Azure
// generated package compiles and exposes the spec-driven types
// that downstream adapters can decode/encode against. A regression
// here means cmd/azure-codegen broke the per-service codegen for
// this spec.
package conformance_test

import (
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
