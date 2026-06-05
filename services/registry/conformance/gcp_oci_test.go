// Conformance: GCP Artifact Registry-shaped frontend exercised by the
// official go-containerregistry (crane/ggcr) OCI client — the canonical
// Go image push/pull surface. A real image is pushed through the shim's
// AR frontend (Bearer-authenticated /v2/) into the inmem content-
// addressable backend, then pulled back and its digest compared.
package conformance_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/registry/frontends/gcp_artifactregistry"
	"github.com/e6qu/shimanism/services/registry/backends/inmem"
)

func arBearerJWT() string {
	return gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://artifactregistry.googleapis.com/",
		15*time.Minute,
	)
}

func TestGCR_AR_ImagePushPull(t *testing.T) {
	srv := httptest.NewServer(gcp_artifactregistry.Handler(inmem.New()))
	defer srv.Close()

	host := srv.Listener.Addr().String() // 127.0.0.1:PORT
	ref, err := name.ParseReference(host+"/proj/repo/app:v1", name.Insecure)
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	opts := []remote.Option{
		remote.WithAuth(&authn.Bearer{Token: arBearerJWT()}),
		remote.WithTransport(http.DefaultTransport),
	}

	// Build a small random image and push it through the shim.
	img, err := random.Image(2048, 2) // 2KiB layers, 2 of them
	if err != nil {
		t.Fatalf("random.Image: %v", err)
	}
	pushedDigest, err := img.Digest()
	if err != nil {
		t.Fatalf("img.Digest: %v", err)
	}
	if err := remote.Write(ref, img, opts...); err != nil {
		t.Fatalf("push (remote.Write through shim): %v", err)
	}

	// Pull it back by tag and compare the manifest digest.
	pulled, err := remote.Image(ref, opts...)
	if err != nil {
		t.Fatalf("pull (remote.Image through shim): %v", err)
	}
	pulledDigest, err := pulled.Digest()
	if err != nil {
		t.Fatalf("pulled.Digest: %v", err)
	}
	if pulledDigest != pushedDigest {
		t.Errorf("pulled digest = %s, want %s", pulledDigest, pushedDigest)
	}

	// Pull by digest reference too.
	digestRef, err := name.ParseReference(host+"/proj/repo/app@"+pushedDigest.String(), name.Insecure)
	if err != nil {
		t.Fatalf("parse digest ref: %v", err)
	}
	if _, err := remote.Image(digestRef, opts...); err != nil {
		t.Fatalf("pull by digest: %v", err)
	}

	// List tags through the shim; "v1" must be present.
	tags, err := remote.List(ref.Context(), opts...)
	if err != nil {
		t.Fatalf("remote.List (tags): %v", err)
	}
	if !contains(tags, "v1") {
		t.Errorf("tags = %v, want it to contain v1", tags)
	}

	// Verify the layers actually round-tripped (content-addressable).
	layers, err := pulled.Layers()
	if err != nil {
		t.Fatalf("pulled.Layers: %v", err)
	}
	srcLayers, _ := img.Layers()
	if len(layers) != len(srcLayers) {
		t.Errorf("pulled %d layers, want %d", len(layers), len(srcLayers))
	}
	for i, l := range layers {
		got, _ := l.Digest()
		want, _ := srcLayers[i].Digest()
		if got != want {
			t.Errorf("layer %d digest = %s, want %s", i, got, want)
		}
	}
}

func TestGCR_AR_UnauthenticatedChallenged(t *testing.T) {
	srv := httptest.NewServer(gcp_artifactregistry.Handler(inmem.New()))
	defer srv.Close()

	// A raw GET /v2/ with no token must be challenged (401 + Bearer
	// WWW-Authenticate), proving the data plane is authenticated (N31).
	resp, err := http.Get(srv.URL + "/v2/")
	if err != nil {
		t.Fatalf("GET /v2/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /v2/ = %d, want 401", resp.StatusCode)
	}
	if wa := resp.Header.Get("WWW-Authenticate"); wa == "" {
		t.Error("401 missing WWW-Authenticate Bearer challenge")
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
