package ocidistribution

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"
)

// Digest matches an OCI content digest "<algorithm>:<hex>". sha256 is the
// canonical algorithm the shim verifies in-flight; sha512 is permitted by
// the spec but passes through opaque (N34).
type Digest string

// Algorithm returns the digest's algorithm prefix ("sha256"), or "" if
// the digest is malformed.
func (d Digest) Algorithm() string {
	if i := strings.IndexByte(string(d), ':'); i > 0 {
		return string(d)[:i]
	}
	return ""
}

// Valid reports whether d is a well-formed "<alg>:<hex>" digest.
func (d Digest) Valid() bool {
	i := strings.IndexByte(string(d), ':')
	if i <= 0 {
		return false
	}
	hexpart := string(d)[i+1:]
	if hexpart == "" {
		return false
	}
	if _, err := hex.DecodeString(hexpart); err != nil {
		return false
	}
	return true
}

// digestWriter computes a content digest as bytes are written through it.
// For sha256 it hashes; for any other (e.g. sha512) it does not verify —
// callers treat a non-sha256 digest as opaque (N34).
type digestWriter struct {
	alg string
	h   hash.Hash // nil for unverifiable algorithms
	n   int64
}

func newDigestWriter(alg string) *digestWriter {
	dw := &digestWriter{alg: alg}
	if alg == "sha256" {
		dw.h = sha256.New()
	}
	return dw
}

func (dw *digestWriter) Write(p []byte) (int, error) {
	if dw.h != nil {
		dw.h.Write(p)
	}
	dw.n += int64(len(p))
	return len(p), nil
}

// verifiable reports whether this writer can produce a digest to compare.
func (dw *digestWriter) verifiable() bool { return dw.h != nil }

// sum returns the computed "<alg>:<hex>" digest. Only meaningful when
// verifiable() is true.
func (dw *digestWriter) sum() Digest {
	return Digest(dw.alg + ":" + hex.EncodeToString(dw.h.Sum(nil)))
}

// VerifyReader streams r while computing its sha256, returning the bytes'
// total size and an error if the content does not match want. A non-sha256
// `want` is not verified (passes through) per N34. The supplied sink
// receives the streamed bytes (e.g. the backend's upload writer).
func VerifyReader(r io.Reader, want Digest, sink io.Writer) (int64, error) {
	if !want.Valid() {
		return 0, fmt.Errorf("%w: malformed digest %q", ErrInvalidDigest, want)
	}
	dw := newDigestWriter(want.Algorithm())
	tee := io.TeeReader(r, dw)
	n, err := io.Copy(sink, tee)
	if err != nil {
		return n, err
	}
	if dw.verifiable() && dw.sum() != want {
		return n, fmt.Errorf("%w: computed %s, claimed %s", ErrDigestMismatch, dw.sum(), want)
	}
	return n, nil
}
