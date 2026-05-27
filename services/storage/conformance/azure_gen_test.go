// Phase 12.A.20 toolchain invariant: the storage service's Azure
// generated package (Blob Storage data-plane) compiles and exposes
// the spec-driven types downstream adapters decode/encode against.
// A regression here means cmd/azure-codegen broke the per-service
// codegen for the Blob spec — most likely the preprocessor's
// schema/parameter/header gating or the parameter/definition
// name-collision deduper.
package conformance_test

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/storage/frontends/azure_blob"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
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

// TestAzureGen_Storage_ServerImplementsInterface pins the spec-drift
// contract: the azure_blob frontend must implement every method on
// gen.ServerInterface. A regression here means a new operation got
// added to the upstream spec and the frontend hasn't been extended
// to either bridge it (in-intersection) or stub it (out-of-intersection).
func TestAzureGen_Storage_ServerImplementsInterface(t *testing.T) {
	var _ azuregen.ServerInterface = (*azure_blob.Server)(nil)
}

// TestAzureGen_Storage_HandlerDispatch_InIntersection sends a canonical
// container PUT through the frontend and asserts the in-intersection
// bridge wired up correctly. Pairs with the compile-time guard above:
// compile-time proves the interface is satisfied; this runtime test
// proves the bridge calls reach the real backend.
func TestAzureGen_Storage_HandlerDispatch_InIntersection(t *testing.T) {
	srv := azure_blob.New(inmem.New())

	req := httptest.NewRequest(http.MethodPut, "/devstoreaccount1/shim-test-container?restype=container", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("ContainerCreate status = %d; want 201. body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/devstoreaccount1/?comp=list", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ServiceListContainersSegment status = %d; want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "shim-test-container") {
		t.Errorf("list response missing container; body=%s", w.Body.String())
	}
}

// TestAzureGen_Storage_HandlerDispatch_OutOfIntersection drives an
// out-of-intersection op through the gen.ServerInterface directly and
// asserts the Azure error envelope (status 501, x-ms-error-code
// OperationNotSupported, XML body). ServeHTTP itself doesn't dispatch
// to these — the gen.ServerInterface methods are the entry point used
// by future spec-driven mux wiring + the spec-drift contract.
func TestAzureGen_Storage_HandlerDispatch_OutOfIntersection(t *testing.T) {
	srv := azure_blob.New(inmem.New())

	w := httptest.NewRecorder()
	srv.BlobSetTier(w, httptest.NewRequest(http.MethodPut, "/c/b?comp=tier", nil), "c", "b", azuregen.BlobSetTierParams{})
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("BlobSetTier status = %d; want 501", w.Code)
	}
	if got := w.Header().Get("x-ms-error-code"); got != "OperationNotSupported" {
		t.Errorf("x-ms-error-code = %q; want OperationNotSupported", got)
	}
	var apiErr struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
		Message string   `xml:"Message"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, w.Body.String())
	}
	if apiErr.Code != "OperationNotSupported" {
		t.Errorf("error code = %q; want OperationNotSupported", apiErr.Code)
	}
}
