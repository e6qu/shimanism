package azure_acr

import "testing"

func TestRepoFromV2Path(t *testing.T) {
	cases := map[string]string{
		"/v2/team/app/manifests/v1":            "team/app",
		"/v2/team/app/blobs/sha256:abc":        "team/app",
		"/v2/team/app/blobs/uploads/upload-id": "team/app",
		"/v2/team/app/tags/list":               "team/app",
		"/v2/":                                 "",
		"/acr/v1/team/app/_manifests":          "",
	}
	for path, want := range cases {
		if got := repoFromV2Path(path); got != want {
			t.Fatalf("repoFromV2Path(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestScopeForV2Request(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   string
	}{
		{"GET", "/v2/team/app/manifests/v1", "repository:team/app:pull"},
		{"HEAD", "/v2/team/app/blobs/sha256:abc", "repository:team/app:pull"},
		{"PATCH", "/v2/team/app/blobs/uploads/id", "repository:team/app:pull,push"},
		{"PUT", "/v2/team/app/manifests/v1", "repository:team/app:pull,push"},
		{"DELETE", "/v2/team/app/manifests/sha256:abc", "repository:team/app:pull,delete"},
		{"GET", "/v2/", "registry:catalog:*"},
	}
	for _, tc := range cases {
		if got := scopeFor(tc.method, tc.path); got != tc.want {
			t.Fatalf("scopeFor(%q, %q) = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestNextTokenFromLink(t *testing.T) {
	link := "https://example.azurecr.io/acr/v1/_catalog?last=team%2Fapp&n=1"
	if got := nextToken(link); got != "team/app" {
		t.Fatalf("nextToken = %q, want team/app", got)
	}
}
