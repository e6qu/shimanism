// Phase 13.C — production RS256 JWKS path for the Azure Bearer
// verifier. Mirror of internal/gcpbearer/jwks.go; same JWKS shape +
// reconstruction + in-memory and URL-fetched + cached lookup.
//
// For Microsoft Entra (Azure AD) production deployments, set
// `Options.JWKSURL` to
// `https://login.microsoftonline.com/common/discovery/v2.0/keys` (or
// the tenant-scoped equivalent). The verifier caches the JWKS for
// JWKSCacheTTL and re-fetches on a fresh `kid` (handles signer-key
// rotation transparently).

package azurebearer

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

// JWKS is a minimal RFC 7517 JSON Web Key Set. The verifier only
// honours RSA / RS256 / sig entries.
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

// RSAPublicKey reconstructs the RSA public key from base64url N + E.
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
	e := new(big.Int).SetBytes(eBytes).Int64()
	if e <= 0 {
		return nil, errors.New("RSA exponent E must be positive")
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(e),
	}, nil
}

// remoteJWKS fetches + caches a JWKS by URL.
type remoteJWKS struct {
	url        string
	httpClient *http.Client
	cacheTTL   time.Duration

	mu         sync.Mutex
	cached     *JWKS
	fetched    time.Time
	fetchedKid map[string]bool
}

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

func (r *remoteJWKS) lookup(kid string) (*JWK, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cached != nil && time.Since(r.fetched) < r.cacheTTL && r.fetchedKid[kid] {
		return r.findLocked(kid)
	}
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
