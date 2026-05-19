// Package shimakit is the in-tree K8s-peer framework for shimmed
// services whose OSS-peer slot doesn't have a good third-party
// fit. It exposes the common-denominator API every cloud-service
// phase reduces to: versioned, named binary objects with
// structured metadata and a soft-delete lifecycle, multi-namespace
// addressing so one peer serves many shim services. See README.md.
//
// Naming convention: this is the **framework**. Concrete per-service
// peers built on top of shimakit are named `shima<service>` —
// `shimasecret` for a secrets peer, `shimastore` for an object-
// storage peer, etc. Phase 1 (storage) and Phase 2 (secrets) used
// MinIO and Vault respectively; no `shima<service>` peer has
// shipped yet. The framework will get its first user when a phase
// surfaces a real gap.
//
// The contract is intentionally minimal: any operation that can't
// be reduced to a list-versioned-objects + read-bytes + write-bytes
// + delete pair doesn't belong here. The shim service's frontend
// handles the per-cloud shape translation; this peer holds the
// bytes.
package shimakit

import (
	"context"
	"io"
	"time"
)

// Store is the common denominator every shim-built K8s peer
// exposes. Implementations may be in-process (for tests),
// filesystem-backed (single-node deployments), object-storage-
// backed (when the peer is fronting a different backing layer), or
// anything else that satisfies the contract.
//
// Implementations must be safe for concurrent use across goroutines.
// All operations are namespace-scoped: one peer instance serves
// multiple shim services by giving each its own namespace.
type Store interface {
	// Put writes a new version of the named object in the given
	// namespace. Returns the monotonic version number assigned (≥ 1).
	// Putting against a soft-deleted name reactivates the name with a
	// new version number; the recovered-versions decision (whether to
	// preserve the pre-delete history) lives in the implementation,
	// per [README.md](README.md).
	Put(ctx context.Context, ns, name string, body io.Reader, meta map[string]string) (Version, error)

	// Get returns the bytes + metadata of one specific version of an
	// object. Version 0 means "the most recent live version". Caller
	// must Close the returned Body.
	Get(ctx context.Context, ns, name string, version uint64) (Object, error)

	// Head returns metadata + version info without the body.
	Head(ctx context.Context, ns, name string) (ObjectInfo, error)

	// Delete soft-deletes (force=false) or permanently removes
	// (force=true) the named object. Soft-deleted objects can be
	// listed via List with IncludeDeleted=true; force-deleted ones
	// can't be recovered.
	Delete(ctx context.Context, ns, name string, force bool) error

	// List enumerates objects in a namespace, optionally filtered by
	// name prefix.
	List(ctx context.Context, ns string, opt ListOptions) (ListResult, error)

	// ListVersions enumerates every version of one object in
	// ascending monotonic-version order.
	ListVersions(ctx context.Context, ns, name string) ([]Version, error)
}

// Object is a single retrieved version: bytes + metadata.
type Object struct {
	Name      string
	Version   uint64
	Body      io.ReadCloser
	Metadata  map[string]string
	Size      int64
	CreatedAt time.Time
}

// ObjectInfo is metadata + current-version info without the body.
type ObjectInfo struct {
	Name           string
	Metadata       map[string]string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CurrentVersion uint64
	SoftDeleted    bool
}

// Version is one specific version's metadata.
type Version struct {
	Number    uint64
	Size      int64
	CreatedAt time.Time
}

// ListOptions controls a List call.
type ListOptions struct {
	Prefix         string
	MaxResults     int
	NextToken      string
	IncludeDeleted bool
}

// ListResult is the List response.
type ListResult struct {
	Objects   []ObjectInfo
	NextToken string
}
