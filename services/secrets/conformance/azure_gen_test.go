// Phase 12.A toolchain invariant: the secrets service's Azure
// generated package (Key Vault Secrets data-plane) compiles and
// exposes the spec-driven types downstream adapters decode/encode
// against. A regression here means cmd/azure-codegen broke the
// per-service codegen for this spec.
//
// Secrets is the canonical Phase 11/12 pilot — azure_keyvault is
// the fully-migrated reference frontend (every handler routes
// through gen.HandlerWithOptions; wire types are gen.SecretBundle
// / gen.SecretAttributes / etc.). The smoke test ensures the gen
// package keeps compiling as the preprocessor evolves.
package conformance_test

import (
	"reflect"
	"testing"

	azuregen "github.com/e6qu/shimanism/services/secrets/gen/azure"
)

func TestAzureGen_Secrets_PackageCompiles(t *testing.T) {
	iface := reflect.TypeOf((*azuregen.ServerInterface)(nil)).Elem()
	if iface.Kind() != reflect.Interface {
		t.Fatalf("expected gen.ServerInterface to be an interface, got %s", iface.Kind())
	}
}
