// Phase 12.A toolchain invariant: the pubsub service's Azure
// generated package compiles and exposes the spec-driven types
// that downstream adapters can decode/encode against. A regression
// here means cmd/azure-codegen broke the per-service codegen for
// this spec.
package conformance_test

import (
	"reflect"
	"testing"

	azuregen "github.com/e6qu/shimanism/services/pubsub/gen/azure"
)

func TestAzureGen_Pubsub_PackageCompiles(t *testing.T) {
	// Sanity: at least one exported type must exist in the
	// generated package. ServerInterface is emitted by every
	// oapi-codegen std-net-http output.
	iface := reflect.TypeOf((*azuregen.ServerInterface)(nil)).Elem()
	if iface.Kind() != reflect.Interface {
		t.Fatalf("expected gen.ServerInterface to be an interface, got %s", iface.Kind())
	}
	// Service Bus data-plane (shared with queue) — ~13 ops
	// covering Send / Receive / Peek / Renew / Delete on
	// topics+subscriptions.
	if got := iface.NumMethod(); got < 10 {
		t.Errorf("ServerInterface has %d methods; want ≥10. Spec preprocessor may have dropped operations.", got)
	}
}
