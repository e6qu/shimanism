package mockaad_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/mockaad"
)

func TestMockAAD_OIDCDiscovery(t *testing.T) {
	srv := mockaad.NewServer(&mockaad.Options{ResourceManagerURL: "https://arm.example/"})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	srv.SetSelfURL(ts.URL)

	tenant := "00000000-0000-0000-0000-000000000000"
	resp, err := http.Get(ts.URL + "/" + tenant + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("GET openid-configuration: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openid-configuration status = %d, want 200", resp.StatusCode)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode openid-configuration: %v", err)
	}
	want := ts.URL + "/" + tenant + "/oauth2/v2.0/token"
	if got, _ := doc["token_endpoint"].(string); got != want {
		t.Errorf("token_endpoint = %q, want %q", got, want)
	}
}

func TestMockAAD_TokenExchange(t *testing.T) {
	srv := mockaad.NewServer(&mockaad.Options{ResourceManagerURL: "https://arm.example/"})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	srv.SetSelfURL(ts.URL)

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"any-client"},
		"client_secret": {"any-secret"},
		"scope":         {"https://management.azure.com/.default"},
	}
	resp, err := http.PostForm(ts.URL+"/some-tenant/oauth2/v2.0/token", form)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d, want 200", resp.StatusCode)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if got, _ := doc["token_type"].(string); got != "Bearer" {
		t.Errorf("token_type = %q, want Bearer", got)
	}
	tok, _ := doc["access_token"].(string)
	if strings.Count(tok, ".") != 2 {
		t.Errorf("access_token doesn't look like a JWT: %q", tok)
	}
}

func TestMockAAD_CloudMetadata(t *testing.T) {
	armURL := "https://arm.example.test/"
	srv := mockaad.NewServer(&mockaad.Options{ResourceManagerURL: armURL})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	srv.SetSelfURL(ts.URL)

	resp, err := http.Get(ts.URL + "/metadata/endpoints?api-version=2019-05-01")
	if err != nil {
		t.Fatalf("GET metadata/endpoints: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metadata status = %d, want 200", resp.StatusCode)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if got, _ := doc["resourceManager"].(string); got != armURL {
		t.Errorf("resourceManager = %q, want %q", got, armURL)
	}
	auth, _ := doc["authentication"].(map[string]any)
	if auth == nil {
		t.Fatalf("authentication block missing")
	}
	loginEndpoint, _ := auth["loginEndpoint"].(string)
	if !strings.HasPrefix(loginEndpoint, ts.URL) {
		t.Errorf("authentication.loginEndpoint = %q, want prefix %q", loginEndpoint, ts.URL)
	}
}
