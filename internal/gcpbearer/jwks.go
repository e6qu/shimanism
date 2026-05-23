// Phase 13.C — production RS256 JWKS path for the GCP Bearer
// verifier. The shim accepts RS256-signed JWTs whose `kid` matches
// a key the JWKS publishes, runs `crypto/rsa.VerifyPKCS1v15` against
// the reconstructed RSA public key, and validates the standard
// iss/aud/exp/iat claims. Test mode (HS256 with a project-owned key)
// stays the default — production mode is opted into by setting
// `Options.JWKSURL` (remote, fetched + cached) or `Options.JWKS`
// (in-process).
//
// The Verify path branches on the JWT header's `alg`:
//
//   - `HS256` → HS256 path (Options.TestKey must be set).
//   - `RS256` → RS256 path (Options.JWKS or JWKSURL must be set).
//
// For the production Google ID-token path, point `JWKSURL` at
// `https://www.googleapis.com/oauth2/v3/certs` (the Google-published
// JWKS). For Workload Identity Federation tokens, the JWKS URL is
// per-pool. Real-cloud lanes (Track A) exercise both paths.

package gcpbearer

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// JWKS is a minimal JSON Web Key Set carrying only the RSA fields
// the verifier needs (RFC 7517). Fields not used by RS256 (octet
// keys, EC keys, x5c chains) are intentionally omitted — the shim
// declines anything that isn't `kty=RSA + alg=RS256 + use=sig`.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWK is one entry in a JWKS.
type JWK struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// RSAPublicKey reconstructs the RSA public key from the JWK's N + E
// (base64url-encoded big-endian, per RFC 7518 § 6.3.1).
func (k JWK) RSAPublicKey() (*rsa.PublicKey, error) {
	if k.Kty != "RSA" {
		return nil, fmt.Errorf("JWK kty %q is not RSA", k.Kty)
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode RSA modulus N: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode RSA exponent E: %w", err)
	}
	// Pad/extend exponent to int. AWS / Google use 3-byte exponent
	// (`AQAB` = 65537); RFC 7518 doesn't bound the size.
	e := new(big.Int).SetBytes(eBytes).Int64()
	if e <= 0 {
		return nil, errors.New("RSA exponent E must be positive")
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(e),
	}, nil
}

// remoteJWKS fetches + caches a JWKS by URL. The shim doesn't ship
// background-refresh; the first lookup of an unknown `kid` triggers
// a re-fetch (handles signer-key rotation transparently as long as
// the deploy can tolerate one extra round-trip per new kid).
type remoteJWKS struct {
	url        string
	httpClient *http.Client
	cacheTTL   time.Duration

	mu         sync.Mutex
	cached     *JWKS
	fetched    time.Time
	fetchedKid map[string]bool // set of kids we've seen via the cache
}

// newRemoteJWKS constructs a JWKS fetcher.
func newRemoteJWKS(url string, ttl time.Duration) *remoteJWKS {
	if ttl == 0 {
		ttl = 1 * time.Hour
	}
	return &remoteJWKS{
		url:        url,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		cacheTTL:   ttl,
		fetchedKid: map[string]bool{},
	}
}

// lookup returns the JWK matching kid, fetching the JWKS if the
// cache is stale or the kid is unseen.
func (r *remoteJWKS) lookup(kid string) (*JWK, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Cache hit + within TTL → return cached match.
	if r.cached != nil && time.Since(r.fetched) < r.cacheTTL && r.fetchedKid[kid] {
		return r.findLocked(kid)
	}
	// Cache miss or unknown kid → fetch.
	if err := r.fetchLocked(); err != nil {
		return nil, err
	}
	return r.findLocked(kid)
}

func (r *remoteJWKS) findLocked(kid string) (*JWK, error) {
	if r.cached == nil {
		return nil, errors.New("JWKS not loaded")
	}
	for i := range r.cached.Keys {
		if r.cached.Keys[i].Kid == kid {
			return &r.cached.Keys[i], nil
		}
	}
	return nil, fmt.Errorf("no JWK matching kid %q", kid)
}

func (r *remoteJWKS) fetchLocked() error {
	resp, err := r.httpClient.Get(r.url)
	if err != nil {
		return fmt.Errorf("fetch JWKS %s: %w", r.url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch JWKS %s: HTTP %d", r.url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read JWKS body: %w", err)
	}
	var jwks JWKS
	if err := json.Unmarshal(body, &jwks); err != nil {
		return fmt.Errorf("parse JWKS JSON: %w", err)
	}
	r.cached = &jwks
	r.fetched = time.Now()
	r.fetchedKid = map[string]bool{}
	for _, k := range jwks.Keys {
		r.fetchedKid[k.Kid] = true
	}
	return nil
}
