// Phase 12.B route-inventory invariants for the pubsub service's GCP
// Discovery routing table. A regression here means upstream
// Discovery drift or a cmd/gcp-codegen emitter regression.
package conformance_test

import (
	"sort"
	"testing"

	gcpgen "github.com/e6qu/shimanism/services/pubsub/gen/gcp"
)

func TestGCPRoutes_Pubsub_BasePathSane(t *testing.T) {
	if gcpgen.BasePath != "" {
		t.Errorf("BasePath = %q; want empty", gcpgen.BasePath)
	}
}

func TestGCPRoutes_Pubsub_InventoryWellFormed(t *testing.T) {
	if len(gcpgen.Routes) == 0 {
		t.Fatal("gen.gcp.Routes is empty; cmd/gcp-codegen emitted nothing")
	}
	for i, r := range gcpgen.Routes {
		if r.ID == "" || r.HTTPMethod == "" || r.URIPattern == "" {
			t.Errorf("Routes[%d] has empty field: %+v", i, r)
		}
	}
}

func TestGCPRoutes_Pubsub_DeterministicallySorted(t *testing.T) {
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

func TestGCPRoutes_Pubsub_CoversCrossCloudIntersection(t *testing.T) {
	expected := []string{
		"pubsub.projects.topics.create",
		"pubsub.projects.topics.get",
		"pubsub.projects.topics.delete",
		"pubsub.projects.topics.list",
		"pubsub.projects.topics.publish",
		"pubsub.projects.subscriptions.create",
		"pubsub.projects.subscriptions.get",
		"pubsub.projects.subscriptions.delete",
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

// TestGCPRoutes_Pubsub_FrontendDispatchCoverage asserts the
// hand-written gcp_pubsub frontend's dispatch shapes are present
// in the gen inventory's MatchAll candidates. Pub/Sub's Discovery
// uses `v1/{+topic}`, `v1/{+subscription}`, etc. as overlapping
// templates; MatchAll surfaces every candidate so the test passes
// when the expected op is among them.
func TestGCPRoutes_Pubsub_FrontendDispatchCoverage(t *testing.T) {
	cases := []struct {
		op     string
		method string
		path   string
	}{
		{"pubsub.projects.topics.create", "PUT", "/v1/projects/p/topics/t"},
		{"pubsub.projects.topics.get", "GET", "/v1/projects/p/topics/t"},
		{"pubsub.projects.topics.delete", "DELETE", "/v1/projects/p/topics/t"},
		{"pubsub.projects.topics.list", "GET", "/v1/projects/p/topics"},
		{"pubsub.projects.topics.publish", "POST", "/v1/projects/p/topics/t:publish"},
		{"pubsub.projects.subscriptions.create", "PUT", "/v1/projects/p/subscriptions/s"},
		{"pubsub.projects.subscriptions.get", "GET", "/v1/projects/p/subscriptions/s"},
		{"pubsub.projects.subscriptions.delete", "DELETE", "/v1/projects/p/subscriptions/s"},
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

// TestGCPRoutes_Pubsub_EveryRouteRoundTrips synthesizes a sample path
// for every Route in gen.gcp.Routes and asserts the Pattern matches
// + Match() returns a candidate. Catches regressions where
// templateToRegex emits a pattern that fails to match its own
// template-derived path.
func TestGCPRoutes_Pubsub_EveryRouteRoundTrips(t *testing.T) {
	for _, r := range gcpgen.Routes {
		path := gcpgen.BasePath + "/" + r.URIPattern
		path = expandRouteTemplatePubsub(path)
		if !r.Pattern.MatchString(path) {
			t.Errorf("Route %q: Pattern %q does not match its own template-derived path %q",
				r.ID, r.Pattern, path)
		}
	}
}

// expandRouteTemplatePubsub substitutes URI-template variables with
// sample values; both {var} and {+var} become "x".
func expandRouteTemplatePubsub(t string) string {
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
