package azure_cosmos_tables

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/services/nosql/backends/inmem"
)

// TestPassthrough_ForwardsARMPaths verifies the frontend forwards
// ARM paths to the upstream handler. ARM paths start with
// `/subscriptions/`; the data-plane handlers (Tables / entity ops)
// use unprefixed paths, so the routing split is unambiguous.
func TestPassthrough_ForwardsARMPaths(t *testing.T) {
	upstreamHit := make(chan string, 4)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit <- r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"upstream-handled"}`)
	})
	srv := NewWithPassthrough(inmem.New(), upstream)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cases := []struct {
		name string
		path string
	}{
		{"resource-group", "/subscriptions/sub-1/resourceGroups/shim-rg"},
		{"cosmos account", "/subscriptions/sub-1/resourceGroups/shim-rg/providers/Microsoft.DocumentDB/databaseAccounts/acct1"},
		{"cosmos table", "/subscriptions/sub-1/resourceGroups/shim-rg/providers/Microsoft.DocumentDB/databaseAccounts/acct1/tables/users"},
		{"non-DocumentDB provider", "/subscriptions/sub-1/resourceGroups/shim-rg/providers/Microsoft.Storage/storageAccounts/acct1"},
		{"providers list", "/subscriptions/sub-1/providers"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(ts.URL + tc.path + "?api-version=2024-01-01")
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
			}
			if !strings.Contains(string(body), "upstream-handled") {
				t.Errorf("response body did not come from upstream: %s", body)
			}
			select {
			case got := <-upstreamHit:
				if got != tc.path {
					t.Errorf("upstream received %q, want %q", got, tc.path)
				}
			default:
				t.Errorf("upstream handler did not record the request")
			}
		})
	}
}

// TestPassthrough_PreservesTablesDataPlane verifies that Tables
// data-plane requests (POST /Tables, GET /Tables('x'), etc.) still
// go to the data-plane handler when a passthrough is configured.
// The split: ARM = `/subscriptions/`, Tables = everything else.
func TestPassthrough_PreservesTablesDataPlane(t *testing.T) {
	upstreamHit := make(chan struct{}, 1)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit <- struct{}{}
		w.WriteHeader(http.StatusTeapot) // marker; real upstream would never return this on Tables ops.
	})
	srv := NewWithPassthrough(inmem.New(), upstream)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// CreateTable via the Tables data plane.
	resp, err := http.Post(ts.URL+"/Tables", "application/json",
		strings.NewReader(`{"TableName":"datatest"}`))
	if err != nil {
		t.Fatalf("POST /Tables: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("data-plane POST /Tables status = %d, want 201\nbody: %s", resp.StatusCode, body)
	}
	select {
	case <-upstreamHit:
		t.Errorf("upstream handler was hit on data-plane path; should be routed to Tables dispatch")
	default:
	}
}

// TestPassthrough_NilFallsThrough verifies that without a
// passthrough configured, ARM paths fall through to the data-plane
// dispatcher, which surfaces a 404 in the OData error envelope. No
// silent success.
func TestPassthrough_NilFallsThrough(t *testing.T) {
	srv := New(inmem.New())
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/subscriptions/sub-1/resourceGroups/shim-rg?api-version=2024-01-01")
	if err != nil {
		t.Fatalf("GET subscriptions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 404\nbody: %s", resp.StatusCode, body)
	}
}

// TestMetadata_PointsResourceManagerAtShim verifies the metadata
// endpoint returns the shim's URL for `resourceManager` and the
// configured upstream URL for `authentication.loginEndpoint`.
// azurerm uses this discovery to decide where to acquire tokens vs
// where to send ARM calls.
func TestMetadata_PointsResourceManagerAtShim(t *testing.T) {
	const upstream = "https://sockerless.example/upstream"
	srv := NewWithConfig(inmem.New(), Config{MetadataLoginURL: upstream})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/metadata/endpoints?api-version=2022-09-01")
	if err != nil {
		t.Fatalf("GET metadata: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rm, _ := env["resourceManager"].(string); rm != ts.URL {
		t.Errorf("resourceManager = %q, want %q (the shim's URL)", rm, ts.URL)
	}
	auth, _ := env["authentication"].(map[string]any)
	if auth == nil {
		t.Fatal("authentication missing")
	}
	if login, _ := auth["loginEndpoint"].(string); login != upstream {
		t.Errorf("loginEndpoint = %q, want %q", login, upstream)
	}
}

// TestMetadata_OlderApiVersionReturnsArray verifies the api-version
// branching: 2022-09-01 returns a single object (azurerm v3/v4);
// older versions return a singleton array.
func TestMetadata_OlderApiVersionReturnsArray(t *testing.T) {
	srv := NewWithConfig(inmem.New(), Config{MetadataLoginURL: "https://example/"})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/metadata/endpoints?api-version=2019-05-01")
	if err != nil {
		t.Fatalf("GET metadata old: %v", err)
	}
	defer resp.Body.Close()
	var arr []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 1 {
		t.Errorf("array len = %d, want 1", len(arr))
	}
}

// TestMetadata_NotServedWhenURLEmpty verifies the metadata endpoint
// is NOT served when MetadataLoginURL is unset — the request falls
// through (to the passthrough if configured, or 404 otherwise).
// Skipping this would silently hide misconfiguration.
func TestMetadata_NotServedWhenURLEmpty(t *testing.T) {
	srv := New(inmem.New())
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/metadata/endpoints?api-version=2022-09-01")
	if err != nil {
		t.Fatalf("GET metadata: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("metadata endpoint returned 200 with MetadataLoginURL unset")
	}
}
