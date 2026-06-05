package ocidistribution_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/registry/domain"
	"github.com/e6qu/shimanism/internal/registry/ocidistribution"
	"github.com/e6qu/shimanism/services/registry/backends/inmem"
)

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

func newTestServer(t *testing.T) (*httptest.Server, domain.Registry) {
	t.Helper()
	backend := inmem.New()
	srv := httptest.NewServer(ocidistribution.New(backend))
	t.Cleanup(srv.Close)
	return srv, backend
}

func do(t *testing.T, method, url string, body []byte, ctype string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, url, err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func TestOCIRouter_BaseCheck(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := do(t, http.MethodGet, srv.URL+"/v2/", nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v2/ = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Docker-Distribution-API-Version"); got != "registry/2.0" {
		t.Errorf("API-Version header = %q, want registry/2.0", got)
	}
}

func TestOCIRouter_MonolithicBlobRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	repo := "myorg/app" // slash in name exercises path parsing
	blob := []byte("layer-bytes-monolithic")
	dg := sha256hex(blob)

	// Single-POST monolithic upload: POST .../uploads/?digest=X with body.
	resp := do(t, http.MethodPost, srv.URL+"/v2/"+repo+"/blobs/uploads/?digest="+dg, blob, "application/octet-stream")
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("monolithic upload = %d, want 201", resp.StatusCode)
	}
	if got := resp.Header.Get("Docker-Content-Digest"); got != dg {
		t.Errorf("upload digest header = %q, want %q", got, dg)
	}

	// HEAD the blob.
	resp = do(t, http.MethodHead, srv.URL+"/v2/"+repo+"/blobs/"+dg, nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD blob = %d, want 200", resp.StatusCode)
	}

	// GET the blob and verify content.
	resp = do(t, http.MethodGet, srv.URL+"/v2/"+repo+"/blobs/"+dg, nil, "")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET blob = %d, want 200", resp.StatusCode)
	}
	if !bytes.Equal(got, blob) {
		t.Errorf("GET blob body = %q, want %q", got, blob)
	}
}

func TestOCIRouter_ChunkedBlobRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	repo := "lib/base"
	chunkA := []byte("first-half-")
	chunkB := []byte("second-half")
	blob := append(append([]byte{}, chunkA...), chunkB...)
	dg := sha256hex(blob)

	// POST to open a session.
	resp := do(t, http.MethodPost, srv.URL+"/v2/"+repo+"/blobs/uploads/", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("start upload = %d, want 202", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		t.Fatal("start upload: empty Location header")
	}
	uploadURL := srv.URL + loc

	// PATCH first chunk.
	resp = do(t, http.MethodPatch, uploadURL, chunkA, "application/octet-stream")
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("PATCH chunk A = %d, want 202", resp.StatusCode)
	}
	if r := resp.Header.Get("Range"); r != "0-"+strconv.Itoa(len(chunkA)-1) {
		t.Errorf("Range after chunk A = %q, want 0-%d", r, len(chunkA)-1)
	}

	// PATCH second chunk.
	resp = do(t, http.MethodPatch, uploadURL, chunkB, "application/octet-stream")
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("PATCH chunk B = %d, want 202", resp.StatusCode)
	}

	// PUT to finalize with the digest (empty body).
	resp = do(t, http.MethodPut, uploadURL+"?digest="+dg, nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("finalize = %d, want 201", resp.StatusCode)
	}

	// GET verifies the assembled content.
	resp = do(t, http.MethodGet, srv.URL+"/v2/"+repo+"/blobs/"+dg, nil, "")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, blob) {
		t.Errorf("assembled blob = %q, want %q", got, blob)
	}
}

func TestOCIRouter_DigestMismatchRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	blob := []byte("real-content")
	wrong := sha256hex([]byte("different-content"))
	resp := do(t, http.MethodPost, srv.URL+"/v2/r/blobs/uploads/?digest="+wrong, blob, "application/octet-stream")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("digest mismatch = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "DIGEST_INVALID") {
		t.Errorf("mismatch body = %q, want DIGEST_INVALID", body)
	}
}

func TestOCIRouter_ManifestAndTags(t *testing.T) {
	srv, backend := newTestServer(t)
	repo := "team/svc"
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	mediaType := "application/vnd.oci.image.manifest.v1+json"
	mdg := sha256hex(manifest)

	// PUT manifest under tag "latest".
	resp := do(t, http.MethodPut, srv.URL+"/v2/"+repo+"/manifests/latest", manifest, mediaType)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT manifest = %d, want 201", resp.StatusCode)
	}
	if got := resp.Header.Get("Docker-Content-Digest"); got != mdg {
		t.Errorf("manifest digest = %q, want %q", got, mdg)
	}

	// GET by tag; media type + body round-trip.
	resp = do(t, http.MethodGet, srv.URL+"/v2/"+repo+"/manifests/latest", nil, "")
	got, _ := io.ReadAll(resp.Body)
	ct := resp.Header.Get("Content-Type")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET manifest = %d, want 200", resp.StatusCode)
	}
	if !bytes.Equal(got, manifest) {
		t.Errorf("manifest body = %q, want %q", got, manifest)
	}
	if ct != mediaType {
		t.Errorf("Content-Type = %q, want %q (verbatim, N32)", ct, mediaType)
	}

	// HEAD by digest.
	resp = do(t, http.MethodHead, srv.URL+"/v2/"+repo+"/manifests/"+mdg, nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD manifest by digest = %d, want 200", resp.StatusCode)
	}

	// tags/list contains "latest".
	resp = do(t, http.MethodGet, srv.URL+"/v2/"+repo+"/tags/list", nil, "")
	tagsBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(tagsBody), `"latest"`) {
		t.Errorf("tags/list = %q, want it to contain latest", tagsBody)
	}

	// Control-plane cross-check via the backend directly.
	imgs, err := backend.ListImages(context.Background(), repo, domain.ListOptions{})
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(imgs.Images) != 1 || imgs.Images[0].Digest != mdg {
		t.Fatalf("ListImages = %+v, want one image with digest %s", imgs.Images, mdg)
	}

	// DELETE manifest, then GET 404s.
	resp = do(t, http.MethodDelete, srv.URL+"/v2/"+repo+"/manifests/latest", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("DELETE manifest = %d, want 202", resp.StatusCode)
	}
	resp = do(t, http.MethodGet, srv.URL+"/v2/"+repo+"/manifests/latest", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET after delete = %d, want 404", resp.StatusCode)
	}
}
