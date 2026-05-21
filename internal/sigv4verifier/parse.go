package sigv4verifier

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
		return "", parsedAuth{}, fmt.Errorf("authorization header missing scheme/body separator")
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
		return "", parsedAuth{}, fmt.Errorf("authorization header missing Credential / SignedHeaders / Signature")
	}
	return scheme, parsed, nil
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

