// Package azuresharedkey implements the Azure Storage SharedKey
// signature verifier. SharedKey is used by Blob, Queue, Table, and
// File services (not by Key Vault, which uses Bearer — see
// `internal/azurebearer`).
//
// Wire format:
//
//	Authorization: SharedKey <account>:<base64-signature>
//
// The signature is HMAC-SHA256 of the canonical string using the
// account's primary or secondary key, with the result base64-encoded.
// Canonical string format per Microsoft Learn:
//
//	HTTP-Verb + \n
//	Content-Encoding + \n
//	Content-Language + \n
//	Content-Length + \n
//	Content-MD5 + \n
//	Content-Type + \n
//	Date + \n
//	If-Modified-Since + \n
//	If-Match + \n
//	If-None-Match + \n
//	If-Unmodified-Since + \n
//	Range + \n
//	CanonicalizedHeaders +
//	CanonicalizedResource
//
// CanonicalizedHeaders: all `x-ms-*` headers, lowercased, sorted by
// name, each `name:value\n` with values trimmed of leading/trailing
// whitespace and internal newlines replaced by space.
//
// CanonicalizedResource: `/<account>` + URL path + query parameters
// lowercased, sorted, joined as `\nparam:value`.
package azuresharedkey

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// AccountStore looks up an Azure Storage account's primary key by
// account name. Returns ok=false for unknown accounts.
type AccountStore interface {
	Lookup(account string) (key []byte, ok bool)
}

// StaticStore is a single-account AccountStore — handy for tests.
type StaticStore struct {
	Account string
	Key     []byte
}

func (s StaticStore) Lookup(account string) ([]byte, bool) {
	if account != s.Account {
		return nil, false
	}
	return s.Key, true
}

// Error is the structured failure type. HTTP handlers map it onto
// Azure's AuthenticationFailed envelope.
type Error struct {
	HTTPStatus int
	Code       string // "AuthenticationFailed", "InvalidAuthenticationInfo".
	Message    string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Verifier verifies SharedKey-signed requests.
type Verifier struct {
	store AccountStore
}

// New constructs a verifier.
func New(store AccountStore) *Verifier { return &Verifier{store: store} }

// Verify checks the request's Authorization header. Returns nil on
// success, *azuresharedkey.Error on failure.
//
// Limitations of this initial verifier (Phase 11 follow-on):
//
//   - Lite signing variant not supported (only the "full" Authorization
//     scheme; the legacy `SharedKeyLite` is deprecated).
//   - SAS-token verification not supported (those have a separate
//     signature format using `Authorization` or query parameters; the
//     existing storage frontend handles them out-of-band).
//   - URI encoding edge cases (% escapes in query values) are honoured
//     as Azure expects via `url.QueryUnescape`.
func (v *Verifier) Verify(r *http.Request) error {
	authHdr := r.Header.Get("Authorization")
	if authHdr == "" {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "AuthenticationFailed", Message: "Server failed to authenticate the request. Make sure the value of Authorization header is formed correctly including the signature."}
	}
	// Two schemes accepted: full "SharedKey" (used by Blob/Queue/File
	// data planes) and "SharedKeyLite" (used by Tables — different
	// canonical form, see buildTablesCanonicalString below). The
	// presented account name + base64 signature follow either prefix.
	var rest string
	var scheme string
	switch {
	case strings.HasPrefix(authHdr, "SharedKeyLite "):
		rest = strings.TrimPrefix(authHdr, "SharedKeyLite ")
		scheme = "SharedKeyLite"
	case strings.HasPrefix(authHdr, "SharedKey "):
		rest = strings.TrimPrefix(authHdr, "SharedKey ")
		scheme = "SharedKey"
	default:
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationInfo", Message: "Authorization scheme must be SharedKey"}
	}
	colonAt := strings.IndexByte(rest, ':')
	if colonAt < 0 {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationInfo", Message: "Authorization header missing account:signature separator"}
	}
	account := rest[:colonAt]
	presented := rest[colonAt+1:]

	key, ok := v.store.Lookup(account)
	if !ok {
		return &Error{HTTPStatus: http.StatusForbidden, Code: "AuthenticationFailed", Message: "account name is not recognised"}
	}

	var canonical string
	if scheme == "SharedKeyLite" {
		canonical = buildTablesCanonicalString(r, account)
	} else {
		canonical = buildCanonicalString(r, account)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(canonical))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if subtleConstTimeEq([]byte(expected), []byte(presented)) {
		return nil
	}
	return &Error{HTTPStatus: http.StatusForbidden, Code: "AuthenticationFailed", Message: fmt.Sprintf("computed signature does not match presented signature for account %q (scheme=%s)", account, scheme)}
}

// buildTablesCanonicalString assembles the SharedKeyLite canonical
// string per the Tables variant — see Azure's docs and the aztables
// SDK's buildStringToSign. Strictly:
//
//	StringToSign = x-ms-date "\n" CanonicalizedResource
//
// CanonicalizedResource = "/" account u.EscapedPath() [?comp=<v>]
//
// Only the `comp=` query parameter participates in the canonical
// form for Tables; other query parameters (Top, NextRowKey, $filter,
// etc.) are not signed.
func buildTablesCanonicalString(r *http.Request, account string) string {
	var b strings.Builder
	b.WriteString(r.Header.Get("x-ms-date"))
	b.WriteString("\n")
	b.WriteString("/")
	b.WriteString(account)
	if r.URL.Path == "" {
		b.WriteString("/")
	} else {
		b.WriteString(r.URL.EscapedPath())
	}
	if comp := r.URL.Query().Get("comp"); comp != "" {
		b.WriteString("?comp=")
		b.WriteString(comp)
	}
	return b.String()
}

// buildCanonicalString assembles the canonical signing string per
// Azure Storage's SharedKey specification.
func buildCanonicalString(r *http.Request, account string) string {
	var b strings.Builder
	b.WriteString(r.Method)
	b.WriteString("\n")
	b.WriteString(r.Header.Get("Content-Encoding"))
	b.WriteString("\n")
	b.WriteString(r.Header.Get("Content-Language"))
	b.WriteString("\n")
	cl := r.Header.Get("Content-Length")
	// Azure expects an empty Content-Length to be the empty string,
	// not "0".
	if cl == "0" {
		cl = ""
	}
	b.WriteString(cl)
	b.WriteString("\n")
	b.WriteString(r.Header.Get("Content-MD5"))
	b.WriteString("\n")
	b.WriteString(r.Header.Get("Content-Type"))
	b.WriteString("\n")
	// When x-ms-date is present, Date is empty in the canonical string.
	if r.Header.Get("x-ms-date") != "" {
		b.WriteString("")
	} else {
		b.WriteString(r.Header.Get("Date"))
	}
	b.WriteString("\n")
	b.WriteString(r.Header.Get("If-Modified-Since"))
	b.WriteString("\n")
	b.WriteString(r.Header.Get("If-Match"))
	b.WriteString("\n")
	b.WriteString(r.Header.Get("If-None-Match"))
	b.WriteString("\n")
	b.WriteString(r.Header.Get("If-Unmodified-Since"))
	b.WriteString("\n")
	b.WriteString(r.Header.Get("Range"))
	b.WriteString("\n")
	b.WriteString(canonicalizedHeaders(r))
	b.WriteString(canonicalizedResource(r, account))
	return b.String()
}

func canonicalizedHeaders(r *http.Request) string {
	type kv struct{ k, v string }
	var hs []kv
	for k, vs := range r.Header {
		lk := strings.ToLower(k)
		if !strings.HasPrefix(lk, "x-ms-") {
			continue
		}
		v := strings.Join(vs, ",")
		// Newlines inside values get squashed to spaces per spec.
		v = strings.ReplaceAll(v, "\n", " ")
		v = strings.TrimSpace(v)
		hs = append(hs, kv{lk, v})
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].k < hs[j].k })
	var b strings.Builder
	for _, h := range hs {
		b.WriteString(h.k)
		b.WriteString(":")
		b.WriteString(h.v)
		b.WriteString("\n")
	}
	return b.String()
}

func canonicalizedResource(r *http.Request, account string) string {
	var b strings.Builder
	b.WriteString("/")
	b.WriteString(account)
	// Azure's SDK signs over u.EscapedPath() — the percent-encoded
	// form — so the verifier must do the same. r.URL.Path is
	// pre-decoded by net/http, so paths containing %-escaped slashes
	// (like greetings%2Fhello.txt) would otherwise diverge.
	b.WriteString(r.URL.EscapedPath())
	if len(r.URL.Query()) == 0 {
		return b.String()
	}
	type kv struct{ k, v string }
	var qs []kv
	for k, vs := range r.URL.Query() {
		lk := strings.ToLower(k)
		// Multiple values are joined comma-separated, sorted.
		sort.Strings(vs)
		v := strings.Join(vs, ",")
		qs = append(qs, kv{lk, v})
	}
	sort.Slice(qs, func(i, j int) bool { return qs[i].k < qs[j].k })
	for _, q := range qs {
		b.WriteString("\n")
		b.WriteString(q.k)
		b.WriteString(":")
		b.WriteString(q.v)
	}
	return b.String()
}

// subtleConstTimeEq is hmac.Equal's wrapper kept here so importers
// don't need to import crypto/subtle directly.
func subtleConstTimeEq(a, b []byte) bool { return hmac.Equal(a, b) }
