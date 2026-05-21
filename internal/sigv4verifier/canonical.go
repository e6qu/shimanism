package sigv4verifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// computeSigV4Signature reproduces the AWS SigV4 signature for an
// incoming request using ONLY the header names the original signer
// declared in its `SignedHeaders` field. We can't reuse
// aws-sdk-go-v2's signer for verification because it auto-includes
// headers the original signer may not have (Content-Length is the
// classic divergence between aws-sdk-go-v2 and boto3) — the only
// honest path is to compute the canonical request ourselves, using
// the original signed-headers list verbatim.
//
// Algorithm per https://docs.aws.amazon.com/IAM/latest/UserGuide/create-signed-request.html.
func computeSigV4Signature(r *http.Request, body []byte, signedHeaders, payloadHash, accessKey, secret, sessionToken, service, region, signedTimeYYYYMMDD, signedTimeFull string) string {
	canonicalReq := buildCanonicalRequest(r, body, signedHeaders, payloadHash)
	hashedCR := sha256.Sum256([]byte(canonicalReq))

	credScope := signedTimeYYYYMMDD + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" +
		signedTimeFull + "\n" +
		credScope + "\n" +
		hex.EncodeToString(hashedCR[:])

	// Derive the signing key.
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(signedTimeYYYYMMDD))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))
	_ = accessKey
	_ = sessionToken
	return signature
}

// computePresignedSigV4Signature reproduces the SigV4 signature for
// a presigned URL request. The canonical query string excludes the
// X-Amz-Signature parameter (the signature is what we're computing).
// Payload hash is the caller-supplied value (typically
// UNSIGNED-PAYLOAD for presigned URLs).
func computePresignedSigV4Signature(r *http.Request, signedHeaders, payloadHash, secret, service, region, signedTimeYYYYMMDD, signedTimeFull string) string {
	method := r.Method
	canonURI := canonicalURI(r.URL.Path)
	q := r.URL.Query()
	q.Del("X-Amz-Signature")
	canonQuery := canonicalQueryString(q)
	canonHeaders, signedHeadersList := canonicalHeaders(r, signedHeaders)
	canonicalReq := method + "\n" +
		canonURI + "\n" +
		canonQuery + "\n" +
		canonHeaders + "\n" +
		signedHeadersList + "\n" +
		payloadHash
	hashedCR := sha256.Sum256([]byte(canonicalReq))
	credScope := signedTimeYYYYMMDD + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" +
		signedTimeFull + "\n" +
		credScope + "\n" +
		hex.EncodeToString(hashedCR[:])
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(signedTimeYYYYMMDD))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))
}

// buildCanonicalRequest builds the SigV4 canonical request from the
// incoming HTTP request + the original signed-headers list +
// pre-computed payload hash.
func buildCanonicalRequest(r *http.Request, body []byte, signedHeaders, payloadHash string) string {
	method := r.Method
	canonURI := canonicalURI(r.URL.Path)
	canonQuery := canonicalQueryString(r.URL.Query())
	canonHeaders, signedHeadersList := canonicalHeaders(r, signedHeaders)
	return method + "\n" +
		canonURI + "\n" +
		canonQuery + "\n" +
		canonHeaders + "\n" +
		signedHeadersList + "\n" +
		payloadHash
}

// canonicalURI URI-encodes each path segment per RFC 3986. The "/"
// separator is kept literal.
func canonicalURI(p string) string {
	if p == "" {
		return "/"
	}
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		parts[i] = awsURIEncode(seg, false)
	}
	return strings.Join(parts, "/")
}

// awsURIEncode encodes per AWS's URI-encoding rules. unreserved is
// per RFC 3986: A-Z a-z 0-9 - _ . ~ . If encodeSlash=false, "/" is
// passed through (used for canonical URI path); if true, "/" is
// encoded as %2F (used for query values).
func awsURIEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'),
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteString("%")
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return b.String()
}

// canonicalQueryString builds the canonical query string: keys + values
// URI-encoded, sorted by key (then by value for repeated keys), and
// joined by `&`.
func canonicalQueryString(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string{}, q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, awsURIEncode(k, true)+"="+awsURIEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// canonicalHeaders builds the canonical-headers block + signed-headers
// list from the request and the original signed-headers spec
// (semicolon-separated, lowercase). Only headers in `signedHeaders`
// are included; their names are lowercased and their values are
// trimmed of leading/trailing whitespace and sequential whitespace
// is collapsed.
func canonicalHeaders(r *http.Request, signedHeaders string) (canonicalBlock, signedHeadersList string) {
	names := strings.Split(signedHeaders, ";")
	for i := range names {
		names[i] = strings.ToLower(strings.TrimSpace(names[i]))
	}
	sort.Strings(names)
	var b strings.Builder
	for _, lname := range names {
		var v string
		switch lname {
		case "host":
			// AWS uses the request Host header verbatim; net/http
			// strips it into r.Host and replays r.URL.Host as a
			// fallback.
			v = r.Host
			if v == "" {
				v = r.URL.Host
			}
		case "content-length":
			// http.Request stores Content-Length out-of-band; the
			// header map may not include it.
			if r.ContentLength >= 0 {
				v = intToString(r.ContentLength)
			} else if h := r.Header.Get("Content-Length"); h != "" {
				v = h
			}
		default:
			v = strings.TrimSpace(r.Header.Get(lname))
		}
		v = collapseWhitespace(v)
		b.WriteString(lname)
		b.WriteString(":")
		b.WriteString(v)
		b.WriteString("\n")
	}
	return b.String(), strings.Join(names, ";")
}

func collapseWhitespace(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return b.String()
}

func intToString(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

