package sigv4verifier_test

import (
	"crypto/sha256"
	"encoding/hex"
)

// sigv4SHA256Hex mirrors the package's unexported sha256Hex so the
// external test file can compute payload hashes for signing.
func sigv4SHA256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
