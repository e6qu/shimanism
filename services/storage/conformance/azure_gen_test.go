// Phase 12.A.20 toolchain invariant: the storage service's Azure
// generated package (Blob Storage data-plane) compiles and exposes
// the spec-driven types downstream adapters decode/encode against.
// A regression here means cmd/azure-codegen broke the per-service
// codegen for the Blob spec — most likely the preprocessor's
// schema/parameter/header gating or the parameter/definition
// name-collision deduper.
package conformance_test

import (
	"reflect"
	"testing"

	azuregen "github.com/e6qu/shimanism/services/storage/gen/azure"
)

func TestAzureGen_Storage_PackageCompiles(t *testing.T) {
	iface := reflect.TypeOf((*azuregen.ServerInterface)(nil)).Elem()
	if iface.Kind() != reflect.Interface {
		t.Fatalf("expected gen.ServerInterface to be an interface, got %s", iface.Kind())
	}
	// Blob data-plane is the largest Azure spec — ~70 operations
	// across blobs / containers / accounts. The x-ms-paths
	// flattener moves 60 entries from `x-ms-paths` into `paths`;
	// a regression there would collapse the count dramatically.
	if got := iface.NumMethod(); got < 50 {
		t.Errorf("ServerInterface has %d methods; want ≥50. flattenXMSPaths may have regressed.", got)
	}
}
