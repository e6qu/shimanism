// Package azure_blob is the Azure Blob Storage REST API frontend.
//
// Dispatch: hybrid. The Azure Blob spec uses query-discriminated URLs
// (`?restype=container`, `?comp=list`, `?comp=block`, …) that Go
// 1.22's ServeMux can't natively dispatch on — the operation depends
// on the query string, not the path. ServeHTTP keeps a hand-written
// query-discriminated dispatcher (see below) for the in-intersection
// surface the shim actually serves.
//
// The frontend ALSO implements gen.ServerInterface so the spec-drift
// contract from cmd/azure-codegen is honoured directly (not just via
// blank import). In-intersection methods bridge to the same hand-
// written handlers ServeHTTP uses; out-of-intersection methods return
// the Azure error envelope via notImplemented. This matches the hybrid
// pattern Service Bus uses in `internal/queue/frontends/azure_servicebus`
// (Phase 13.A.4).
//
// In-intersection (12):
//   - ServiceListContainersSegment, ContainerCreate, ContainerGetProperties,
//     ContainerDelete, ContainerListBlobFlatSegment,
//     ContainerListBlobHierarchySegment, BlockBlobUpload, BlobDownload,
//     BlobGetProperties, BlobDelete, BlobStartCopyFromURL, BlobCopyFromURL.
//
// Out-of-intersection (57): lease ops, page-blob ops, append-blob ops,
// block-list ops (the frontend doesn't expose multipart staging; the
// backend speaks directly to its destination cloud for that), tags,
// snapshots, tiers, ACLs, batch, query, immutability/legal-hold,
// service-level properties/stats/account-info, undelete/restore/rename.
package azure_blob

import (
	"net/http"
	"strings"

	"github.com/e6qu/shimanism/internal/storage/domain"

	gen "github.com/e6qu/shimanism/services/storage/gen/azure"
)

// Server is an Azure-Blob-shaped HTTP frontend that dispatches to a
// domain.Storage backend. Routing follows Azure's `?restype=` +
// `?comp=` query convention plus per-method dispatch — no need for
// the AWS S3-style required-headers/required-queries matrix because
// Azure's URL grammar disambiguates operations explicitly via these
// query params.
type Server struct {
	s domain.Storage
}

// New returns an Azure Blob frontend wired to the given backend.
func New(s domain.Storage) *Server { return &Server{s: s} }

// ServeHTTP routes the request. Azure routes are:
//
//	/                        — account-level (ListContainers when ?comp=list)
//	/{account}/              — same, with explicit account prefix (path-style override)
//	/{container}             — container ops (PUT create / GET props / DELETE delete) with ?restype=container
//	/{container}?comp=list   — list blobs with ?restype=container&comp=list
//	/{container}/{blob}      — blob ops (GET / HEAD / PUT / DELETE)
//
// When the Azure SDK is given an endpoint override pointing at the
// shim, it constructs URLs with the storage-account name as the
// first path segment: `/devstoreaccount1/container/blob`. We accept
// both shapes — with and without the account prefix.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	// Strip a leading "account" segment if present. The shim is
	// account-agnostic; the account name is purely a routing hint
	// for Azure's host-style URLs, not a state-of-record concept
	// for our backends.
	if i := strings.IndexByte(path, '/'); i >= 0 {
		first := path[:i]
		// Heuristic: an "account" segment never contains `=` or `.`
		// and never matches reserved keywords. The rest of the path
		// must start with a container name or be empty.
		if isAccountSegment(first) {
			path = path[i+1:]
		}
	} else if isAccountSegment(path) {
		// Just /{account} → list-containers at the account.
		path = ""
	}
	q := r.URL.Query()
	restype := q.Get("restype")
	comp := q.Get("comp")
	method := r.Method

	switch {
	case path == "" && method == http.MethodGet && comp == "list":
		srv.listContainers(w, r)
		return
	case path == "" && method == http.MethodGet:
		// Bare GET on root with no comp= falls back to listing.
		srv.listContainers(w, r)
		return
	}

	// Split container/blob.
	slash := strings.IndexByte(path, '/')
	if slash < 0 {
		// /{container}
		container := path
		switch {
		case method == http.MethodPut && restype == "container":
			srv.createContainer(w, r, container)
		case method == http.MethodGet && restype == "container" && comp == "list":
			srv.listBlobs(w, r, container)
		case method == http.MethodGet && restype == "container":
			srv.getContainerProperties(w, r, container)
		case method == http.MethodHead && restype == "container":
			srv.getContainerProperties(w, r, container)
		case method == http.MethodDelete && restype == "container":
			srv.deleteContainer(w, r, container)
		default:
			writeError(w, http.StatusBadRequest, "InvalidInput",
				"unrecognised container-level request: "+method+" /"+container+"?"+r.URL.RawQuery)
		}
		return
	}

	container := path[:slash]
	blob := path[slash+1:]
	switch method {
	case http.MethodPut:
		if r.Header.Get("x-ms-copy-source") != "" {
			srv.copyBlob(w, r, container, blob)
			return
		}
		srv.putBlob(w, r, container, blob)
	case http.MethodGet:
		srv.getBlob(w, r, container, blob)
	case http.MethodHead:
		srv.headBlob(w, r, container, blob)
	case http.MethodDelete:
		srv.deleteBlob(w, r, container, blob)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidInput", method+" not allowed on blob")
	}
}

// isAccountSegment reports whether a path segment looks like an
// Azure storage-account name (lowercase letters + digits, between
// 3 and 24 chars). Conservative — any segment that doesn't match
// is treated as a container.
func isAccountSegment(s string) bool {
	if len(s) < 3 || len(s) > 24 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// notImplemented writes the Azure "operation not supported" envelope
// for spec ops outside the cross-cloud storage intersection.
func notImplemented(w http.ResponseWriter, op string) {
	writeError(w, http.StatusNotImplemented, "OperationNotSupported",
		op+" is not in the cross-cloud storage intersection")
}

// =====================================================================
// gen.ServerInterface implementation
//
// ServeHTTP keeps the hand-written query-discriminated dispatcher; the
// gen.ServerInterface methods exist so the spec-drift contract is
// honoured at the type-check boundary. In-intersection methods bridge
// to the same handlers ServeHTTP routes to; out-of-intersection methods
// return the Azure error envelope via notImplemented.
// =====================================================================

// ----- In-intersection bridges -----

func (srv *Server) ServiceListContainersSegment(w http.ResponseWriter, r *http.Request, _ gen.ServiceListContainersSegmentParams) {
	srv.listContainers(w, r)
}

func (srv *Server) ContainerCreate(w http.ResponseWriter, r *http.Request, containerName gen.ContainerName, _ gen.ContainerCreateParams) {
	srv.createContainer(w, r, containerName)
}

func (srv *Server) ContainerGetProperties(w http.ResponseWriter, r *http.Request, containerName gen.ContainerName, _ gen.ContainerGetPropertiesParams) {
	srv.getContainerProperties(w, r, containerName)
}

func (srv *Server) ContainerDelete(w http.ResponseWriter, r *http.Request, containerName gen.ContainerName, _ gen.ContainerDeleteParams) {
	srv.deleteContainer(w, r, containerName)
}

func (srv *Server) ContainerListBlobFlatSegment(w http.ResponseWriter, r *http.Request, containerName gen.ContainerName, _ gen.ContainerListBlobFlatSegmentParams) {
	srv.listBlobs(w, r, containerName)
}

func (srv *Server) ContainerListBlobHierarchySegment(w http.ResponseWriter, r *http.Request, containerName gen.ContainerName, _ gen.ContainerListBlobHierarchySegmentParams) {
	srv.listBlobs(w, r, containerName)
}

func (srv *Server) BlockBlobUpload(w http.ResponseWriter, r *http.Request, containerName gen.ContainerName, blob gen.Blob, _ gen.BlockBlobUploadParams) {
	srv.putBlob(w, r, containerName, blob)
}

func (srv *Server) BlobDownload(w http.ResponseWriter, r *http.Request, containerName gen.ContainerName, blob gen.Blob, _ gen.BlobDownloadParams) {
	srv.getBlob(w, r, containerName, blob)
}

func (srv *Server) BlobGetProperties(w http.ResponseWriter, r *http.Request, containerName gen.ContainerName, blob gen.Blob, _ gen.BlobGetPropertiesParams) {
	srv.headBlob(w, r, containerName, blob)
}

func (srv *Server) BlobDelete(w http.ResponseWriter, r *http.Request, containerName gen.ContainerName, blob gen.Blob, _ gen.BlobDeleteParams) {
	srv.deleteBlob(w, r, containerName, blob)
}

func (srv *Server) BlobStartCopyFromURL(w http.ResponseWriter, r *http.Request, containerName gen.ContainerName, blob gen.Blob, _ gen.BlobStartCopyFromURLParams) {
	srv.copyBlob(w, r, containerName, blob)
}

func (srv *Server) BlobCopyFromURL(w http.ResponseWriter, r *http.Request, containerName gen.ContainerName, blob gen.Blob, _ gen.BlobCopyFromURLParams) {
	srv.copyBlob(w, r, containerName, blob)
}

// ----- Out-of-intersection stubs -----

func (srv *Server) ServiceSubmitBatch(w http.ResponseWriter, _ *http.Request, _ gen.ServiceSubmitBatchParams) {
	notImplemented(w, "ServiceSubmitBatch")
}

func (srv *Server) ServiceFilterBlobs(w http.ResponseWriter, _ *http.Request, _ gen.ServiceFilterBlobsParams) {
	notImplemented(w, "ServiceFilterBlobs")
}

func (srv *Server) ServiceGetAccountInfo(w http.ResponseWriter, _ *http.Request, _ gen.ServiceGetAccountInfoParams) {
	notImplemented(w, "ServiceGetAccountInfo")
}

func (srv *Server) ServiceGetProperties(w http.ResponseWriter, _ *http.Request, _ gen.ServiceGetPropertiesParams) {
	notImplemented(w, "ServiceGetProperties")
}

func (srv *Server) ServiceSetProperties(w http.ResponseWriter, _ *http.Request, _ gen.ServiceSetPropertiesParams) {
	notImplemented(w, "ServiceSetProperties")
}

func (srv *Server) ServiceGetStatistics(w http.ResponseWriter, _ *http.Request, _ gen.ServiceGetStatisticsParams) {
	notImplemented(w, "ServiceGetStatistics")
}

func (srv *Server) ServiceGetUserDelegationKey(w http.ResponseWriter, _ *http.Request, _ gen.ServiceGetUserDelegationKeyParams) {
	notImplemented(w, "ServiceGetUserDelegationKey")
}

func (srv *Server) AppendBlobCreate(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.AppendBlobCreateParams) {
	notImplemented(w, "AppendBlobCreate")
}

func (srv *Server) BlockBlobPutBlobFromUrl(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlockBlobPutBlobFromUrlParams) {
	notImplemented(w, "BlockBlobPutBlobFromUrl")
}

func (srv *Server) PageBlobCreate(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.PageBlobCreateParams) {
	notImplemented(w, "PageBlobCreate")
}

func (srv *Server) AppendBlobAppendBlock(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.AppendBlobAppendBlockParams) {
	notImplemented(w, "AppendBlobAppendBlock")
}

func (srv *Server) AppendBlobAppendBlockFromUrl(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.AppendBlobAppendBlockFromUrlParams) {
	notImplemented(w, "AppendBlobAppendBlockFromUrl")
}

func (srv *Server) BlockBlobStageBlock(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlockBlobStageBlockParams) {
	notImplemented(w, "BlockBlobStageBlock")
}

func (srv *Server) BlockBlobStageBlockFromURL(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlockBlobStageBlockFromURLParams) {
	notImplemented(w, "BlockBlobStageBlockFromURL")
}

func (srv *Server) BlockBlobGetBlockList(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlockBlobGetBlockListParams) {
	notImplemented(w, "BlockBlobGetBlockList")
}

func (srv *Server) BlockBlobCommitBlockList(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlockBlobCommitBlockListParams) {
	notImplemented(w, "BlockBlobCommitBlockList")
}

func (srv *Server) BlobAbortCopyFromURL(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobAbortCopyFromURLParams) {
	notImplemented(w, "BlobAbortCopyFromURL")
}

func (srv *Server) BlobSetExpiry(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobSetExpiryParams) {
	notImplemented(w, "BlobSetExpiry")
}

func (srv *Server) BlobDeleteImmutabilityPolicy(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobDeleteImmutabilityPolicyParams) {
	notImplemented(w, "BlobDeleteImmutabilityPolicy")
}

func (srv *Server) BlobSetImmutabilityPolicy(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobSetImmutabilityPolicyParams) {
	notImplemented(w, "BlobSetImmutabilityPolicy")
}

func (srv *Server) PageBlobCopyIncremental(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.PageBlobCopyIncrementalParams) {
	notImplemented(w, "PageBlobCopyIncremental")
}

func (srv *Server) BlobAcquireLease(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobAcquireLeaseParams) {
	notImplemented(w, "BlobAcquireLease")
}

func (srv *Server) BlobBreakLease(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobBreakLeaseParams) {
	notImplemented(w, "BlobBreakLease")
}

func (srv *Server) BlobChangeLease(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobChangeLeaseParams) {
	notImplemented(w, "BlobChangeLease")
}

func (srv *Server) BlobReleaseLease(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobReleaseLeaseParams) {
	notImplemented(w, "BlobReleaseLease")
}

func (srv *Server) BlobRenewLease(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobRenewLeaseParams) {
	notImplemented(w, "BlobRenewLease")
}

func (srv *Server) BlobSetLegalHold(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobSetLegalHoldParams) {
	notImplemented(w, "BlobSetLegalHold")
}

func (srv *Server) BlobSetMetadata(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobSetMetadataParams) {
	notImplemented(w, "BlobSetMetadata")
}

func (srv *Server) PageBlobClearPages(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.PageBlobClearPagesParams) {
	notImplemented(w, "PageBlobClearPages")
}

func (srv *Server) PageBlobUploadPages(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.PageBlobUploadPagesParams) {
	notImplemented(w, "PageBlobUploadPages")
}

func (srv *Server) PageBlobUploadPagesFromURL(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.PageBlobUploadPagesFromURLParams) {
	notImplemented(w, "PageBlobUploadPagesFromURL")
}

func (srv *Server) PageBlobGetPageRanges(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.PageBlobGetPageRangesParams) {
	notImplemented(w, "PageBlobGetPageRanges")
}

func (srv *Server) PageBlobGetPageRangesDiff(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.PageBlobGetPageRangesDiffParams) {
	notImplemented(w, "PageBlobGetPageRangesDiff")
}

func (srv *Server) PageBlobResize(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.PageBlobResizeParams) {
	notImplemented(w, "PageBlobResize")
}

func (srv *Server) BlobSetHTTPHeaders(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobSetHTTPHeadersParams) {
	notImplemented(w, "BlobSetHTTPHeaders")
}

func (srv *Server) PageBlobUpdateSequenceNumber(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.PageBlobUpdateSequenceNumberParams) {
	notImplemented(w, "PageBlobUpdateSequenceNumber")
}

func (srv *Server) BlobQuery(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobQueryParams) {
	notImplemented(w, "BlobQuery")
}

func (srv *Server) AppendBlobSeal(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.AppendBlobSealParams) {
	notImplemented(w, "AppendBlobSeal")
}

func (srv *Server) BlobCreateSnapshot(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobCreateSnapshotParams) {
	notImplemented(w, "BlobCreateSnapshot")
}

func (srv *Server) BlobGetTags(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobGetTagsParams) {
	notImplemented(w, "BlobGetTags")
}

func (srv *Server) BlobSetTags(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobSetTagsParams) {
	notImplemented(w, "BlobSetTags")
}

func (srv *Server) BlobSetTier(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobSetTierParams) {
	notImplemented(w, "BlobSetTier")
}

func (srv *Server) BlobUndelete(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobUndeleteParams) {
	notImplemented(w, "BlobUndelete")
}

func (srv *Server) BlobGetAccountInfo(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.Blob, _ gen.BlobGetAccountInfoParams) {
	notImplemented(w, "BlobGetAccountInfo")
}

func (srv *Server) ContainerAcquireLease(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerAcquireLeaseParams) {
	notImplemented(w, "ContainerAcquireLease")
}

func (srv *Server) ContainerBreakLease(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerBreakLeaseParams) {
	notImplemented(w, "ContainerBreakLease")
}

func (srv *Server) ContainerChangeLease(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerChangeLeaseParams) {
	notImplemented(w, "ContainerChangeLease")
}

func (srv *Server) ContainerReleaseLease(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerReleaseLeaseParams) {
	notImplemented(w, "ContainerReleaseLease")
}

func (srv *Server) ContainerRenewLease(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerRenewLeaseParams) {
	notImplemented(w, "ContainerRenewLease")
}

func (srv *Server) ContainerGetAccountInfo(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerGetAccountInfoParams) {
	notImplemented(w, "ContainerGetAccountInfo")
}

func (srv *Server) ContainerGetAccessPolicy(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerGetAccessPolicyParams) {
	notImplemented(w, "ContainerGetAccessPolicy")
}

func (srv *Server) ContainerSetAccessPolicy(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerSetAccessPolicyParams) {
	notImplemented(w, "ContainerSetAccessPolicy")
}

func (srv *Server) ContainerSubmitBatch(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerSubmitBatchParams) {
	notImplemented(w, "ContainerSubmitBatch")
}

func (srv *Server) ContainerFilterBlobs(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerFilterBlobsParams) {
	notImplemented(w, "ContainerFilterBlobs")
}

func (srv *Server) ContainerSetMetadata(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerSetMetadataParams) {
	notImplemented(w, "ContainerSetMetadata")
}

func (srv *Server) ContainerRename(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerRenameParams) {
	notImplemented(w, "ContainerRename")
}

func (srv *Server) ContainerRestore(w http.ResponseWriter, _ *http.Request, _ gen.ContainerName, _ gen.ContainerRestoreParams) {
	notImplemented(w, "ContainerRestore")
}

// Compile-time guard: gen.ServerInterface must be fully implemented.
var _ gen.ServerInterface = (*Server)(nil)
