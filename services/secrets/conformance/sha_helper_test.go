package conformance_test

import (
	"crypto/sha256"
	"encoding/hex"
)

// computeSha256 — small helper for the SigV4 test to compute payload
// hashes. Kept package-local to avoid pulling crypto into every test
// file.
func computeSha256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
