// Phase 1.15 conformance: Azure Blob-shaped frontend exercised by
// the official `azure-sdk-for-go/sdk/storage/azblob` SDK. The
// client is pointed at the shim with NewClientWithNoCredential;
// the shim accepts unsigned requests at this phase (signature
// validation is a future hardening step).
package conformance_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
)

const shimAccount = "shimaccount"

func newAzureBlobClient(t *testing.T, endpoint string) *azblob.Client {
	t.Helper()
	// Endpoint includes the account path segment so the SDK
	// constructs URLs as `<shimURL>/<account>/<container>/...` — the
	// shim's frontend strips the account segment and routes the rest.
	full := strings.TrimRight(endpoint, "/") + "/" + shimAccount + "/"
	c, err := azblob.NewClientWithNoCredential(full, nil)
	if err != nil {
		t.Fatalf("new Azure Blob client: %v", err)
	}
	return c
}

func TestAzureBlob_SDK_ContainerLifecycle(t *testing.T) {
	srv := harness.StartStorageServerAzureBlob(t, inmem.New())
	cli := newAzureBlobClient(t, srv.URL)
	ctx := context.Background()

	if _, err := cli.CreateContainer(ctx, "alpha", nil); err != nil {
		t.Fatalf("CreateContainer alpha: %v", err)
	}
	if _, err := cli.CreateContainer(ctx, "beta", nil); err != nil {
		t.Fatalf("CreateContainer beta: %v", err)
	}

	pager := cli.NewListContainersPager(&service.ListContainersOptions{})
	names := []string{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListContainers: %v", err)
		}
		for _, c := range page.ContainerItems {
			if c.Name != nil {
				names = append(names, *c.Name)
			}
		}
	}
	if len(names) != 2 {
		t.Errorf("ListContainers = %v, want 2", names)
	}

	if _, err := cli.DeleteContainer(ctx, "alpha", nil); err != nil {
		t.Errorf("DeleteContainer alpha: %v", err)
	}
}

func TestAzureBlob_SDK_BlobRoundTrip(t *testing.T) {
	srv := harness.StartStorageServerAzureBlob(t, inmem.New())
	cli := newAzureBlobClient(t, srv.URL)
	ctx := context.Background()

	if _, err := cli.CreateContainer(ctx, "data", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	body := []byte("hello shimanism via Azure")
	_, err := cli.UploadBuffer(ctx, "data", "greetings/hello.txt", body, &azblob.UploadBufferOptions{})
	if err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	// HEAD via GetProperties
	props, err := cli.ServiceClient().NewContainerClient("data").NewBlobClient("greetings/hello.txt").GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}
	if props.ContentLength != nil && *props.ContentLength != int64(len(body)) {
		t.Errorf("GetProperties ContentLength = %d, want %d", *props.ContentLength, len(body))
	}

	// Download
	rd, err := cli.DownloadStream(ctx, "data", "greetings/hello.txt", &blob.DownloadStreamOptions{})
	if err != nil {
		t.Fatalf("DownloadStream: %v", err)
	}
	got, err := io.ReadAll(rd.Body)
	_ = rd.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("body = %q, want %q", got, body)
	}

	// List
	listPager := cli.NewListBlobsFlatPager("data", &container.ListBlobsFlatOptions{})
	count := 0
	for listPager.More() {
		page, err := listPager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListBlobs: %v", err)
		}
		for range page.Segment.BlobItems {
			count++
		}
	}
	if count != 1 {
		t.Errorf("ListBlobs count = %d, want 1", count)
	}

	if _, err := cli.DeleteBlob(ctx, "data", "greetings/hello.txt", nil); err != nil {
		t.Errorf("DeleteBlob: %v", err)
	}
}
