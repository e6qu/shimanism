// Phase 12.B route-inventory invariants for the functions service's GCP
// Discovery routing table. A regression here means upstream
// Discovery drift or a cmd/gcp-codegen emitter regression.
package conformance_test

import (
	"sort"
	"testing"

	gcpgen "github.com/e6qu/shimanism/services/functions/gen/gcp"
)

func TestGCPRoutes_Functions_BasePathSane(t *testing.T) {
	if gcpgen.BasePath != "" {
		t.Errorf("BasePath = %q; want empty", gcpgen.BasePath)
	}
}

func TestGCPRoutes_Functions_InventoryWellFormed(t *testing.T) {
	if len(gcpgen.Routes) == 0 {
		t.Fatal("gen.gcp.Routes is empty; cmd/gcp-codegen emitted nothing")
	}
	for i, r := range gcpgen.Routes {
		if r.ID == "" || r.HTTPMethod == "" || r.URIPattern == "" {
			t.Errorf("Routes[%d] has empty field: %+v", i, r)
		}
	}
}

func TestGCPRoutes_Functions_DeterministicallySorted(t *testing.T) {
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

func TestGCPRoutes_Functions_CoversCrossCloudIntersection(t *testing.T) {
	expected := []string{
		"run.projects.locations.services.create",
		"run.projects.locations.services.get",
		"run.projects.locations.services.delete",
		"run.projects.locations.services.list",
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

// TestGCPRoutes_Functions_FrontendDispatchCoverage. The gcp_cloudrun
// frontend dispatches against /v2/projects/p/locations/l/services...
func TestGCPRoutes_Functions_FrontendDispatchCoverage(t *testing.T) {
	cases := []struct {
		op     string
		method string
		path   string
	}{
		{"run.projects.locations.services.create", "POST", "/v2/projects/p/locations/l/services"},
		{"run.projects.locations.services.get", "GET", "/v2/projects/p/locations/l/services/s"},
		{"run.projects.locations.services.delete", "DELETE", "/v2/projects/p/locations/l/services/s"},
		{"run.projects.locations.services.list", "GET", "/v2/projects/p/locations/l/services"},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			candidates := gcpgen.MatchAll(tc.method, tc.path)
			if len(candidates) == 0 {
				t.Fatalf("gen.MatchAll(%q, %q) = no matches; gen inventory missing route", tc.method, tc.path)
			}
			for _, r := range candidates {
				if r.ID == tc.op {
					return
				}
			}
			ids := make([]string, len(candidates))
			for i, r := range candidates {
				ids[i] = r.ID
			}
			t.Errorf("gen.MatchAll(%q, %q) candidates %v do not include expected %q",
				tc.method, tc.path, ids, tc.op)
		})
	}
}
