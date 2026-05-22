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

// TestGCPRoutes_Secrets_FrontendDispatchCoverage asserts that for
// each sample request shape the hand-written gcp_secretmanager
// frontend dispatches, the expected op ID is present in the
// candidates returned by gen.MatchAll. Discovery's Secret Manager
// uses `v1/{+name}` for many ops (overloaded by name shape:
// projects.secrets.get vs projects.locations.secrets.get etc.), so
// MatchAll exposes every candidate; the assertion is "the expected
// op is among them," not "Match returns exactly this op."
func TestGCPRoutes_Secrets_FrontendDispatchCoverage(t *testing.T) {
	cases := []struct {
		op     string
		method string
		path   string
	}{
		{"secretmanager.projects.secrets.create", "POST", "/v1/projects/p/secrets"},
		{"secretmanager.projects.secrets.get", "GET", "/v1/projects/p/secrets/s"},
		{"secretmanager.projects.secrets.delete", "DELETE", "/v1/projects/p/secrets/s"},
		{"secretmanager.projects.secrets.list", "GET", "/v1/projects/p/secrets"},
		{"secretmanager.projects.secrets.addVersion", "POST", "/v1/projects/p/secrets/s:addVersion"},
		{"secretmanager.projects.secrets.versions.access", "GET", "/v1/projects/p/secrets/s/versions/1:access"},
		{"secretmanager.projects.secrets.versions.list", "GET", "/v1/projects/p/secrets/s/versions"},
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
