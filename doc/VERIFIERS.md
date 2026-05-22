# Signature verifiers

Reference for the four per-cloud signature-verifier packages the shim runs at every frontend's decode boundary. These were designed during Phase 11.0–11.1 and closed BUG-18 in Phase 11.14.

## AWS SigV4 — `internal/sigv4verifier/`

Manual canonical-request reconstruction using the building blocks from `aws-sdk-go-v2/aws/signer/v4` (the SDK's signer is signer-only; the verifier is ours).

1. Parses `Authorization` (or the presigned-URL query string) → algorithm + credential scope + signed-headers list + signature.
2. Looks up the access-key against an allowed-credentials store (tests: a deterministic project-owned key the shim trusts only when `SHIMANISM_TEST_TRUSTED_KEY` is set; production: deploy-time config).
3. Recomputes the canonical request, signs with the looked-up secret, constant-time compares.
4. Validates signed time within ±15 min of server time.
5. Special cases: `x-amz-content-sha256: UNSIGNED-PAYLOAD` (streaming uploads), presigned URLs (signature in query string), `X-Amz-Security-Token` (session token included in canonical request).
6. On failure: source-cloud error envelope (`InvalidSignatureException` / `SignatureDoesNotMatch` / `MissingAuthenticationTokenException`).

`canonical.go` accepts both Go-SDK and boto3 / `aws` CLI signing shapes (the two differ in the `SignedHeaders` list — the Go SDK auto-includes `Content-Length`, boto3 doesn't).

## GCP Bearer — `internal/gcpbearer/`

Bifurcated by token type:

- **ID tokens (JWT, signed by Google).** Validated via `google.golang.org/api/idtoken.Validate(ctx, token, audience)` — JWKS fetch + signature + `iss` / `aud` / `exp`. Works for Workload Identity Federation.
- **OAuth2 access tokens (opaque).** Cannot be verified offline. `gcloud auth print-access-token` emits opaque tokens; verifying them requires `https://oauth2.googleapis.com/tokeninfo?access_token=…` per request. Documented gap; the test-mode key emits ID-token-shaped JWTs to exercise the verifier.

Test mode: well-formed JWTs signed by the project-owned test key with `iss` / `aud` / `exp`; the shim validates against the test JWKS via `TestJWT` helper.

`golang.org/x/oauth2` is **not** the verifier — it's client-side token acquisition.

## Azure Bearer — `internal/azurebearer/`

Used by Key Vault, Service Bus, and ARM frontends.

1. Frontend issues the `WWW-Authenticate: Bearer authorization=...` challenge on first request.
2. Extracts the Bearer JWT from the `Authorization` header.
3. Validates the signature against Microsoft's published JWKS at `https://login.microsoftonline.com/common/discovery/v2.0/keys` (cached locally; refreshed on `kid` miss).
4. Validates `iss` matches a configured Entra tenant URI, `aud` matches the resource URI (e.g. `https://vault.azure.net` for Key Vault), `exp` / `nbf` within window.
5. On failure: Azure 401 envelope with the appropriate WWW-Authenticate hint.

Test mode: project-owned signing key + well-formed JWT with the right claims; `TestJWT` helper.

Production: Phase 13.C wires the real Microsoft JWKS path.

## Azure SharedKey — `internal/azuresharedkey/`

Storage-only (Key Vault doesn't use SharedKey; Service Bus uses SAS / Entra ID).

1. Extracts the SharedKey signature from `Authorization: SharedKey <account>:<sig>` or SAS query parameters.
2. Reconstructs the canonical string per Azure Blob's SharedKey signing rules (verb, headers, canonical resource).
3. Recomputes HMAC-SHA256 with the configured account key, constant-time compares.
4. On failure: `AuthenticationFailed` envelope (HTTP 403 + canonical XML body).

Uses `EscapedPath()` (not `Path`) to match the azblob SDK's canonicalisation.

## What the verifiers explicitly don't do

- Call AWS STS to validate temporary credentials.
- Propagate the caller's credential to the backend (the shim uses its own backend-configured identity).
- Trust any header beyond what the canonical request covers.
- Cache claims, open sessions, or carry caller credentials past the decode boundary.

## GCP gRPC vs REST

REST is canonical for the shim today. `google.golang.org/api/<svc>/v1` is the conformance contract. gRPC conformance via `cloud.google.com/go/<svc>` is future expansion (out of scope for Phases 11–13) — adding a gRPC frontend requires a Go gRPC server + protobuf serialization + HTTP/2 multiplexing per service.

Where a gRPC-only operation matters cross-cloud (e.g. Pub/Sub streaming pull), the shim returns the source cloud's own `Unimplemented` envelope on the gRPC path; the REST path remains the conformance contract.

## Production deployment path (Phase 13.C)

Wire the real Microsoft JWKS + `google.golang.org/api/idtoken.Validate` paths when a deployment target requires real-cloud auth. The verifier comments document the exact call sites; no architectural change is required, just config + key-rollover plumbing.
