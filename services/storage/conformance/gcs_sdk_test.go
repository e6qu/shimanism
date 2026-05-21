// Phase 1.14 conformance: GCS-shaped frontend exercised by the
// official `cloud.google.com/go/storage` SDK. The SDK is pointed at
// the shim via option.WithEndpoint; we disable auth so the shim
// receives bearer-less requests. Same intersection set as the AWS
// SDK tests, so a regression in the domain layer surfaces in both.
package conformance_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
)

// bearerTransport injects Authorization: Bearer <jwt> on every
// outbound request. We can't rely on option.WithTokenSource for the
// storage client because cloud.google.com/go/storage strips auth
// when STORAGE_EMULATOR_HOST is set (CI's gcs job sets it so the
// fake-gcs-server backend is reachable). A round-tripper that adds
// the header unconditionally bypasses that SDK-internal stripping.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b *bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.Header.Set("Authorization", "Bearer "+b.token)
	if b.base != nil {
		return b.base.RoundTrip(r2)
	}
	return http.DefaultTransport.RoundTrip(r2)
}

func newGCSClient(t *testing.T, endpoint string) *gcsstorage.Client {
	t.Helper()
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://storage.googleapis.com/",
		15*time.Minute,
	)
	hc := &http.Client{Transport: &bearerTransport{token: jwt}}
	c, err := gcsstorage.NewClient(context.Background(),
		option.WithEndpoint(endpoint),
		option.WithHTTPClient(hc),
	)
	if err != nil {
		t.Fatalf("new GCS client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestGCS_SDK_BucketLifecycle(t *testing.T) {
	srv := harness.StartStorageServerGCS(t, inmem.New())
	cli := newGCSClient(t, srv.URL)
	ctx := context.Background()
	project := "shim-conformance"

	if err := cli.Bucket("alpha").Create(ctx, project, nil); err != nil {
		t.Fatalf("Create alpha: %v", err)
	}
	if err := cli.Bucket("beta").Create(ctx, project, nil); err != nil {
		t.Fatalf("Create beta: %v", err)
	}

	it := cli.Buckets(ctx, project)
	names := []string{}
	for {
		b, err := it.Next()
		if err != nil {
			break
		}
		names = append(names, b.Name)
	}
	if len(names) != 2 {
		t.Errorf("ListBuckets count = %d (%v), want 2", len(names), names)
	}

	if _, err := cli.Bucket("alpha").Attrs(ctx); err != nil {
		t.Errorf("Bucket.Attrs alpha: %v", err)
	}

	if err := cli.Bucket("alpha").Delete(ctx); err != nil {
		t.Errorf("Delete alpha: %v", err)
	}
}

func TestGCS_SDK_ObjectRoundTrip(t *testing.T) {
	srv := harness.StartStorageServerGCS(t, inmem.New())
	cli := newGCSClient(t, srv.URL)
	ctx := context.Background()
	project := "shim-conformance"
	bucket := "data"

	if err := cli.Bucket(bucket).Create(ctx, project, nil); err != nil {
		t.Fatalf("Create bucket: %v", err)
	}

	body := []byte("hello shimanism via GCS")
	wr := cli.Bucket(bucket).Object("greetings/hello.txt").NewWriter(ctx)
	wr.ContentType = "text/plain"
	if _, err := wr.Write(body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := wr.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	attrs, err := cli.Bucket(bucket).Object("greetings/hello.txt").Attrs(ctx)
	if err != nil {
		t.Fatalf("Object.Attrs: %v", err)
	}
	if attrs.Size != int64(len(body)) {
		t.Errorf("Attrs.Size = %d, want %d", attrs.Size, len(body))
	}

	rd, err := cli.Bucket(bucket).Object("greetings/hello.txt").NewReader(ctx)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	got, err := io.ReadAll(rd)
	_ = rd.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("body = %q, want %q", got, body)
	}

	q := &gcsstorage.Query{Prefix: ""}
	it := cli.Bucket(bucket).Objects(ctx, q)
	count := 0
	for {
		_, err := it.Next()
		if err != nil {
			break
		}
		count++
	}
	if count != 1 {
		t.Errorf("ListObjects count = %d, want 1", count)
	}

	if err := cli.Bucket(bucket).Object("greetings/hello.txt").Delete(ctx); err != nil {
		t.Errorf("Delete object: %v", err)
	}
}

func TestGCS_SDK_CopyObject(t *testing.T) {
	srv := harness.StartStorageServerGCS(t, inmem.New())
	cli := newGCSClient(t, srv.URL)
	ctx := context.Background()
	if err := cli.Bucket("c").Create(ctx, "shim-conformance", nil); err != nil {
		t.Fatalf("Create bucket: %v", err)
	}
	body := strings.Repeat("x", 256)
	wr := cli.Bucket("c").Object("src.bin").NewWriter(ctx)
	if _, err := wr.Write([]byte(body)); err != nil {
		t.Fatalf("Write src: %v", err)
	}
	if err := wr.Close(); err != nil {
		t.Fatalf("Close src writer: %v", err)
	}

	src := cli.Bucket("c").Object("src.bin")
	dst := cli.Bucket("c").Object("dst.bin")
	if _, err := dst.CopierFrom(src).Run(ctx); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	attrs, err := dst.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs dst: %v", err)
	}
	if attrs.Size != int64(len(body)) {
		t.Errorf("dst Size = %d, want %d", attrs.Size, len(body))
	}
}
