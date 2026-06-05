package ocidistribution

import (
	"encoding/json"
	"net/http"
	"strings"
)

// pathKind classifies a parsed /v2/ sub-path.
type pathKind int

const (
	kindUnknown         pathKind = iota
	kindBlobUploadStart          // {name}/blobs/uploads/   (POST)
	kindBlobUpload               // {name}/blobs/uploads/{id} (PATCH/PUT)
	kindBlob                     // {name}/blobs/{digest}
	kindManifest                 // {name}/manifests/{reference}
	kindTagsList                 // {name}/tags/list
)

// parsePath splits a /v2/ sub-path (everything after "/v2/") into the
// repository name, the operation kind, and the operation argument (upload
// id, blob digest, or manifest reference). Repository names may contain
// slashes, so the fixed operation markers are matched to find the
// boundary. The most specific marker (blobs/uploads) is checked first.
func parsePath(rest string) (name string, kind pathKind, arg string, ok bool) {
	rest = strings.TrimSuffix(rest, "/")

	// {name}/tags/list
	if strings.HasSuffix(rest, "/tags/list") {
		name = strings.TrimSuffix(rest, "/tags/list")
		return name, kindTagsList, "", name != ""
	}

	// {name}/blobs/uploads  and  {name}/blobs/uploads/{id}
	if i := strings.Index(rest, "/blobs/uploads"); i >= 0 {
		name = rest[:i]
		tail := strings.TrimPrefix(rest[i+len("/blobs/uploads"):], "/")
		if tail == "" {
			return name, kindBlobUploadStart, "", name != ""
		}
		return name, kindBlobUpload, tail, name != ""
	}

	// {name}/blobs/{digest}
	if i := strings.Index(rest, "/blobs/"); i >= 0 {
		name = rest[:i]
		arg = rest[i+len("/blobs/"):]
		return name, kindBlob, arg, name != "" && arg != ""
	}

	// {name}/manifests/{reference}
	if i := strings.Index(rest, "/manifests/"); i >= 0 {
		name = rest[:i]
		arg = rest[i+len("/manifests/"):]
		return name, kindManifest, arg, name != "" && arg != ""
	}

	return "", kindUnknown, "", false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
