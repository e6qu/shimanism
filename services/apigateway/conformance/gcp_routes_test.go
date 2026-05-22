// Phase 12.B route-inventory invariants for the apigateway service's GCP
// Discovery routing table. A regression here means upstream
// Discovery drift or a cmd/gcp-codegen emitter regression.
package conformance_test

import (
	"sort"
	"testing"

	gcpgen "github.com/e6qu/shimanism/services/apigateway/gen/gcp"
)

func TestGCPRoutes_Apigateway_InventoryWellFormed(t *testing.T) {
	if len(gcpgen.Routes) == 0 {
		t.Fatal("gen.gcp.Routes is empty; cmd/gcp-codegen emitted nothing")
	}
	for i, r := range gcpgen.Routes {
		if r.ID == "" || r.HTTPMethod == "" || r.URIPattern == "" {
			t.Errorf("Routes[%d] has empty field: %+v", i, r)
		}
	}
}

func TestGCPRoutes_Apigateway_DeterministicallySorted(t *testing.T) {
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

func TestGCPRoutes_Apigateway_CoversCrossCloudIntersection(t *testing.T) {
	expected := []string{
		"apigateway.projects.locations.apis.create",
		"apigateway.projects.locations.gateways.create",
		"apigateway.projects.locations.gateways.get",
		"apigateway.projects.locations.gateways.delete",
		"apigateway.projects.locations.gateways.list",
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
