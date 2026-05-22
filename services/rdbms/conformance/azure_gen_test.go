// Phase 12.A toolchain invariant: the rdbms service's Azure
// generated package compiles and exposes the spec-driven types
// that downstream adapters can decode/encode against. A regression
// here means cmd/azure-codegen broke the per-service codegen for
// this spec.
package conformance_test

import (
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
