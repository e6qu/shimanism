// Phase 12.B route-inventory invariants: the generated Discovery
// route table for GCP Secret Manager must be non-empty, sorted, and
// cover the cross-cloud secrets-intersection operations the shim
// dispatches today. A regression here means either the upstream
// Discovery document drifted (operations renamed, removed, or
// added) or cmd/gcp-codegen's emitter changed its output shape.
package conformance_test

import (
	"sort"
	"testing"

	gcpgen "github.com/e6qu/shimanism/services/secrets/gen/gcp"
)

func TestGCPRoutes_InventoryWellFormed(t *testing.T) {
	if len(gcpgen.Routes) == 0 {
		t.Fatal("gen.gcp.Routes is empty; cmd/gcp-codegen emitted nothing")
	}
	for i, r := range gcpgen.Routes {
		if r.ID == "" {
			t.Errorf("Routes[%d].ID is empty", i)
		}
		if r.HTTPMethod == "" {
			t.Errorf("Routes[%d].HTTPMethod is empty (ID=%s)", i, r.ID)
		}
		if r.URIPattern == "" {
			t.Errorf("Routes[%d].URIPattern is empty (ID=%s)", i, r.ID)
		}
	}
}

func TestGCPRoutes_DeterministicallySorted(t *testing.T) {
	if !sort.SliceIsSorted(gcpgen.Routes, func(i, j int) bool {
		if gcpgen.Routes[i].HTTPMethod != gcpgen.Routes[j].HTTPMethod {
			return gcpgen.Routes[i].HTTPMethod < gcpgen.Routes[j].HTTPMethod
		}
		if gcpgen.Routes[i].URIPattern != gcpgen.Routes[j].URIPattern {
			return gcpgen.Routes[i].URIPattern < gcpgen.Routes[j].URIPattern
		}
		return gcpgen.Routes[i].ID < gcpgen.Routes[j].ID
	}) {
		t.Error("gen.gcp.Routes is not sorted by (HTTPMethod, URIPattern, ID)")
	}
}

func TestGCPRoutes_CoversCrossCloudIntersection(t *testing.T) {
	expected := []string{
		"secretmanager.projects.secrets.create",
		"secretmanager.projects.secrets.get",
		"secretmanager.projects.secrets.delete",
		"secretmanager.projects.secrets.list",
		"secretmanager.projects.secrets.addVersion",
		"secretmanager.projects.secrets.versions.access",
		"secretmanager.projects.secrets.versions.list",
	}
	have := map[string]bool{}
	for _, r := range gcpgen.Routes {
		have[r.ID] = true
	}
	for _, op := range expected {
		if !have[op] {
			t.Errorf("expected operation %q missing from gen.gcp.Routes", op)
		}
	}
}
