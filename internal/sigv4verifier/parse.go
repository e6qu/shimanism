package sigv4verifier

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// parsedAuth holds the three fields the Authorization header carries
// for SigV4: Credential, SignedHeaders, Signature.
type parsedAuth struct {
	Credential    string
	SignedHeaders string
	Signature     string
}

// parseAuthHeader parses an Authorization header like:
//
//	AWS4-HMAC-SHA256 Credential=AKID/20260521/us-east-1/secretsmanager/aws4_request, SignedHeaders=content-type;host;x-amz-date, Signature=abcd...
//
// Returns the scheme, parsed fields, and an error for malformed input.
func parseAuthHeader(h string) (scheme string, parsed parsedAuth, err error) {
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 {
		return "", parsedAuth{}, fmt.Errorf("Authorization header missing scheme/body separator")
	}
	scheme = parts[0]
	for _, kv := range strings.Split(parts[1], ",") {
		kv = strings.TrimSpace(kv)
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k, v := kv[:eq], kv[eq+1:]
		switch k {
		case "Credential":
			parsed.Credential = v
		case "SignedHeaders":
			parsed.SignedHeaders = v
		case "Signature":
			parsed.Signature = v
		}
	}
	if parsed.Credential == "" || parsed.SignedHeaders == "" || parsed.Signature == "" {
		return "", parsedAuth{}, fmt.Errorf("Authorization header missing Credential / SignedHeaders / Signature")
	}
	return scheme, parsed, nil
}

// parseAuthHeaderShort extracts the Signature field only — used to
// pull the re-signed signature out of the SDK's signer output.
func parseAuthHeaderShort(h string) (signature string, signedHeaders string, err error) {
	_, p, err := parseAuthHeader(h)
	if err != nil {
		return "", "", err
	}
	return p.Signature, p.SignedHeaders, nil
}

// parseAmzDate parses the X-Amz-Date header. AWS SDKs format it as
// `YYYYMMDDTHHMMSSZ` (ISO 8601 basic).
func parseAmzDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("missing")
	}
	t, err := time.Parse("20060102T150405Z", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("could not parse %q as YYYYMMDDTHHMMSSZ", s)
	}
	return t, nil
}

// sha256Hex returns the lowercase hex of SHA-256(body).
func sha256Hex(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

// filterToSignedHeaders returns a copy of `h` containing only the
// headers named in `signedHeaders` (a `;`-separated lowercase list
// from the original Authorization header's `SignedHeaders=...`
// field). The signer needs this exact set on its clone so the
// canonical-request it builds matches what the client built; any
// extra headers (Accept-Encoding added by Go's net/http transport,
// Authorization/X-Amz-Date that the signer itself owns) get
// dropped.
func filterToSignedHeaders(h http.Header, signedHeaders string) http.Header {
	keep := map[string]bool{}
	for _, name := range strings.Split(signedHeaders, ";") {
		keep[strings.ToLower(strings.TrimSpace(name))] = true
	}
	out := http.Header{}
	for k, v := range h {
		if keep[strings.ToLower(k)] {
			out[k] = v
		}
	}
	return out
}
