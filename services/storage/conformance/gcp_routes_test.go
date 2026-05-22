// Phase 12.B route-inventory invariants for the storage service's GCP
// Discovery routing table. A regression here means upstream
// Discovery drift or a cmd/gcp-codegen emitter regression.
package conformance_test

import (
	"sort"
	"testing"

	gcpgen "github.com/e6qu/shimanism/services/storage/gen/gcp"
)

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
