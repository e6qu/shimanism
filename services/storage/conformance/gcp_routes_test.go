// Phase 12.B route-inventory invariants for the storage service's GCP
// Discovery routing table. A regression here means upstream
// Discovery drift or a cmd/gcp-codegen emitter regression.
package conformance_test

import (
	"sort"
	"testing"

	gcpgen "github.com/e6qu/shimanism/services/storage/gen/gcp"
)

func TestGCPRoutes_Storage_BasePathSane(t *testing.T) {
	bp := gcpgen.BasePath
	// Storage's Discovery doc declares basePath="/storage/v1/".
	// The emitter trims the trailing slash. Any other value
	// indicates an emitter regression or a spec change we
	// haven't accounted for.
	if bp != "/storage/v1" {
		t.Errorf("BasePath = %q; want %q (Discovery doc declares /storage/v1/)", bp, "/storage/v1")
	}
}

func TestGCPRoutes_Storage_InventoryWellFormed(t *testing.T) {
	if len(gcpgen.Routes) == 0 {
		t.Fatal("gen.gcp.Routes is empty; cmd/gcp-codegen emitted nothing")
	}
	for i, r := range gcpgen.Routes {
		if r.ID == "" || r.HTTPMethod == "" || r.URIPattern == "" {
			t.Errorf("Routes[%d] has empty field: %+v", i, r)
		}
	}
}

func TestGCPRoutes_Storage_DeterministicallySorted(t *testing.T) {
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

func TestGCPRoutes_Storage_CoversCrossCloudIntersection(t *testing.T) {
	expected := []string{
		"storage.buckets.insert",
		"storage.buckets.get",
		"storage.buckets.delete",
		"storage.buckets.list",
		"storage.objects.insert",
		"storage.objects.get",
		"storage.objects.delete",
		"storage.objects.list",
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

func TestGCPRoutes_Storage_MatchExtractsParams(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		path    string
		wantID  string
		wantVar string // expected variable name in the match
		wantVal string // expected value for that variable
	}{
		{
			name:    "GetBucket",
			method:  "GET",
			path:    "/storage/v1/b/my-bucket",
			wantID:  "storage.buckets.get",
			wantVar: "bucket",
			wantVal: "my-bucket",
		},
		{
			// Object names with slashes are URL-encoded as %2F per the
			// Discovery `{object}` (non-reserved) variable. The GCS
			// SDK does this encoding; the frontend then decodes.
			name:    "GetObject_singleSegmentVar",
			method:  "GET",
			path:    "/storage/v1/b/my-bucket/o/path%2Fto%2Fobject.txt",
			wantID:  "storage.objects.get",
			wantVar: "object",
			wantVal: "path%2Fto%2Fobject.txt",
		},
		{
			// CopyTo: four path variables in a single route.
			// Verifies the URI-template compiler emits the right
			// number of capture groups + ordering matches Vars.
			name:    "CopyObject_fourVars_first",
			method:  "POST",
			path:    "/storage/v1/b/src-bucket/o/src-obj/copyTo/b/dst-bucket/o/dst-obj",
			wantID:  "storage.objects.copy",
			wantVar: "sourceBucket",
			wantVal: "src-bucket",
		},
		{
			name:    "CopyObject_fourVars_last",
			method:  "POST",
			path:    "/storage/v1/b/src-bucket/o/src-obj/copyTo/b/dst-bucket/o/dst-obj",
			wantID:  "storage.objects.copy",
			wantVar: "destinationObject",
			wantVal: "dst-obj",
		},
		{
			// Rewrite parallels Copy with a different URI shape.
			name:    "RewriteObject",
			method:  "POST",
			path:    "/storage/v1/b/src-bucket/o/src-obj/rewriteTo/b/dst-bucket/o/dst-obj",
			wantID:  "storage.objects.rewrite",
			wantVar: "sourceObject",
			wantVal: "src-obj",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, params, ok := gcpgen.Match(tc.method, tc.path)
			if !ok {
				t.Fatalf("Match(%q, %q) = !ok; want match", tc.method, tc.path)
			}
			if r.ID != tc.wantID {
				t.Errorf("matched ID = %q; want %q", r.ID, tc.wantID)
			}
			if got := params[tc.wantVar]; got != tc.wantVal {
				t.Errorf("params[%q] = %q; want %q", tc.wantVar, got, tc.wantVal)
			}
		})
	}

	if _, _, ok := gcpgen.Match("GET", "/nonsense/path"); ok {
		t.Error("Match on bogus path returned ok; want false")
	}
}

// TestGCPRoutes_Storage_FrontendDispatchCoverage asserts that every
// cross-cloud-intersection storage operation the GCS frontend
// dispatches to a real handler is present in gen.gcp.Routes. The
// hand-written GCS frontend (internal/storage/frontends/gcs) uses
// regex routing, not the gen inventory directly — so this test is
// the bridge: when upstream Discovery drops or renames an op the
// frontend implements, the test fails and forces the migration to
// happen in the same PR as the spec bump.
//
// "Dispatch coverage" is checked by URI shape: for each expected
// (method, sample-path) tuple the frontend's regex would match, we
// verify gen.Match() finds the same operation ID.
func TestGCPRoutes_Storage_FrontendDispatchCoverage(t *testing.T) {
	cases := []struct {
		op     string
		method string
		path   string
	}{
		{"storage.buckets.list", "GET", "/storage/v1/b"},
		{"storage.buckets.insert", "POST", "/storage/v1/b"},
		{"storage.buckets.get", "GET", "/storage/v1/b/example"},
		{"storage.buckets.delete", "DELETE", "/storage/v1/b/example"},
		{"storage.objects.list", "GET", "/storage/v1/b/example/o"},
		{"storage.objects.get", "GET", "/storage/v1/b/example/o/key"},
		{"storage.objects.delete", "DELETE", "/storage/v1/b/example/o/key"},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			r, _, ok := gcpgen.Match(tc.method, tc.path)
			if !ok {
				t.Fatalf("gen.Match(%q, %q) failed; gen inventory missing route", tc.method, tc.path)
			}
			if r.ID != tc.op {
				t.Errorf("gen.Match(%q, %q) returned %q; want %q (frontend dispatches this shape)",
					tc.method, tc.path, r.ID, tc.op)
			}
		})
	}
}
