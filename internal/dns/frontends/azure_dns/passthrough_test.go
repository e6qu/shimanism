package azure_dns

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/services/dns/backends/inmem"
)

// TestPassthrough_ForwardsNonDNSPaths verifies the frontend forwards
// ARM paths it doesn't handle to the upstream handler. The shim's
// dispatch matches `Microsoft.Network/<dnsZones|privateDnsZones>` —
// any other ARM path (`Microsoft.Resources/...`, generic resource
// groups, subscription operations, other `Microsoft.Network/*`
// resources) passes through unchanged.
func TestPassthrough_ForwardsNonDNSPaths(t *testing.T) {
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
		{"non-DNS Microsoft.Network", "/subscriptions/sub-1/resourceGroups/shim-rg/providers/Microsoft.Network/virtualNetworks/vnet-1"},
		{"non-Microsoft.Network provider", "/subscriptions/sub-1/resourceGroups/shim-rg/providers/Microsoft.Storage/storageAccounts/acct1"},
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
				t.Errorf("upstream was not hit for %s", tc.path)
			}
		})
	}
}

// TestPassthrough_DNSPathsHandledLocally verifies DNS paths still
// reach the local domain.DNS backend even when an upstream is
// configured.
func TestPassthrough_DNSPathsHandledLocally(t *testing.T) {
	upstreamHit := make(chan struct{}, 1)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit <- struct{}{}
		http.Error(w, "upstream should not be reached", http.StatusInternalServerError)
	})
	srv := NewWithPassthrough(inmem.New(), upstream)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// PUT /dnsZones/example.com — DNS path, must be handled locally.
	req, _ := http.NewRequest("PUT",
		ts.URL+"/subscriptions/sub-1/resourceGroups/shim-rg/providers/Microsoft.Network/dnsZones/example.com?api-version=2018-05-01",
		strings.NewReader(`{"location":"global"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("DNS PUT status = %d; body=%s", resp.StatusCode, body)
	}
	select {
	case <-upstreamHit:
		t.Errorf("upstream was hit for a DNS path — frontend dispatch wrong")
	default:
	}
}

// TestPassthrough_NotConfiguredReturns404 verifies the frontend
// returns the Azure-shaped 404 envelope when no upstream is set and
// the path doesn't match DNS dispatch. No silent fallback.
func TestPassthrough_NotConfiguredReturns404(t *testing.T) {
	srv := New(inmem.New()) // no upstream
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/subscriptions/sub/resourceGroups/rg")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestMetadata_ResourceManagerPointsAtShim_AuthPointsAtUpstream
// verifies the cloud-metadata endpoint: resourceManager pointing at
// the shim's own URL (so ARM calls flow through the frontend's DNS
// dispatch) while authentication.loginEndpoint and other service
// endpoints point at the configured upstream URL. This is the
// BUG-46 contract that lets azurerm acquire its Entra ID token from
// the upstream while routing ARM through the shim.
func TestMetadata_ResourceManagerPointsAtShim_AuthPointsAtUpstream(t *testing.T) {
	const upstreamURL = "https://sockerless.example:14569"
	srv := NewWithConfig(inmem.New(), Config{
		MetadataLoginURL: upstreamURL,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	for _, tc := range []struct {
		name            string
		apiVersion      string
		decodeSingle    bool // 2022-09-01 returns a single object; older returns an array
		wantInOutput    []string
		notWantInOutput []string
	}{
		{"v2022-09-01-single", "2022-09-01", true, nil, nil},
		{"legacy-array", "2020-06-01", false, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(ts.URL + "/metadata/endpoints?api-version=" + tc.apiVersion)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			body, _ := io.ReadAll(resp.Body)

			var env map[string]any
			if tc.decodeSingle {
				if err := json.Unmarshal(body, &env); err != nil {
					t.Fatalf("decode single: %v\n%s", err, body)
				}
			} else {
				var arr []map[string]any
				if err := json.Unmarshal(body, &arr); err != nil {
					t.Fatalf("decode array: %v\n%s", err, body)
				}
				if len(arr) != 1 {
					t.Fatalf("legacy array len = %d, want 1", len(arr))
				}
				env = arr[0]
			}
			rm, _ := env["resourceManager"].(string)
			if !strings.HasPrefix(rm, ts.URL) {
				t.Errorf("resourceManager = %q, want prefix %q", rm, ts.URL)
			}
			auth, _ := env["authentication"].(map[string]any)
			login, _ := auth["loginEndpoint"].(string)
			if login != upstreamURL {
				t.Errorf("authentication.loginEndpoint = %q, want %q", login, upstreamURL)
			}
		})
	}
}

// TestMetadata_NotConfiguredFallsThrough verifies that when
// MetadataLoginURL is unset, /metadata/endpoints falls through to
// the passthrough (or 404 if no upstream).
func TestMetadata_NotConfiguredFallsThrough(t *testing.T) {
	srv := New(inmem.New()) // no metadata config, no upstream
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/metadata/endpoints?api-version=2022-09-01")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no metadata config, no upstream)", resp.StatusCode)
	}
}
