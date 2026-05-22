// Phase 12.B route-inventory invariants for the apigateway service's GCP
// Discovery routing table. A regression here means upstream
// Discovery drift or a cmd/gcp-codegen emitter regression.
package conformance_test

import (
	"sort"
	"testing"

	gcpgen "github.com/e6qu/shimanism/services/apigateway/gen/gcp"
)

func TestGCPRoutes_Apigateway_BasePathSane(t *testing.T) {
	if gcpgen.BasePath != "" {
		t.Errorf("BasePath = %q; want empty", gcpgen.BasePath)
	}
}

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

// TestGCPRoutes_Apigateway_FrontendDispatchCoverage. The gcp_apigateway
// frontend dispatches against /v1/projects/p/locations/l/gateways and
// /v1/projects/p/locations/global/apis.
func TestGCPRoutes_Apigateway_FrontendDispatchCoverage(t *testing.T) {
	cases := []struct {
		op     string
		method string
		path   string
	}{
		{"apigateway.projects.locations.apis.create", "POST", "/v1/projects/p/locations/global/apis"},
		{"apigateway.projects.locations.gateways.create", "POST", "/v1/projects/p/locations/l/gateways"},
		{"apigateway.projects.locations.gateways.get", "GET", "/v1/projects/p/locations/l/gateways/g"},
		{"apigateway.projects.locations.gateways.delete", "DELETE", "/v1/projects/p/locations/l/gateways/g"},
		{"apigateway.projects.locations.gateways.list", "GET", "/v1/projects/p/locations/l/gateways"},
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

// TestGCPRoutes_Apigateway_EveryRouteRoundTrips synthesizes a sample path
// for every Route in gen.gcp.Routes and asserts the Pattern matches
// + Match() returns a candidate. Catches regressions where
// templateToRegex emits a pattern that fails to match its own
// template-derived path.
func TestGCPRoutes_Apigateway_EveryRouteRoundTrips(t *testing.T) {
	for _, r := range gcpgen.Routes {
		path := gcpgen.BasePath + "/" + r.URIPattern
		path = expandRouteTemplateApigateway(path)
		if !r.Pattern.MatchString(path) {
			t.Errorf("Route %q: Pattern %q does not match its own template-derived path %q",
				r.ID, r.Pattern, path)
		}
	}
}

// expandRouteTemplateApigateway substitutes URI-template variables with
// sample values; both {var} and {+var} become "x".
func expandRouteTemplateApigateway(t string) string {
	out := []byte{}
	for i := 0; i < len(t); {
		if t[i] == '{' {
			end := i + 1
			for end < len(t) && t[end] != '}' {
				end++
			}
			if end < len(t) {
				out = append(out, 'x')
				i = end + 1
				continue
			}
		}
		out = append(out, t[i])
		i++
	}
	return string(out)
}
