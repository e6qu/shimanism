# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #17 at `ebc30f7` on `origin/main`, 2026-05-21. Phase 10 closed.
- **Active branch:** `phase-11`. PR #18 (draft) — **all Phase 11 work landed. Ready for review + merge.**
- **Phase 11 — CLOSED.** 8/8 AWS frontends spec-driven via Smithy emitters (REST-XML / awsJson1_x / restJson1 / awsQuery; 3853 LOC of hand-written wire deleted). Azure Key Vault oapi-codegen pilot landed (cmd/azure-codegen: v2→v3 via kin-openapi → oapi-codegen library; SetSecret handler decodes via spec-driven gen.SecretSetParameters). BUG-18 CLOSED — all 24 service-frontends verifier-wrapped (sigv4verifier / gcpbearer / azurebearer / azuresharedkey); bypass dropped; every conformance test signs end-to-end across all 3 clouds. Three collateral defects fixed during closure: awsQuery map-shape XML marshal (`MarshalXML` emitter); SNS attribute fidelity (canonical default policy + AWS-only attribute allowlist for terraform-provider-aws's unconditional SetTopicAttributes calls); kin-openapi empty `AllOf` workaround in cmd/azure-codegen.
- **Phase 12 sub-task table:** lives in [PLAN.md § Phase 12](PLAN.md#phase-12--cross-cloud-migration-cell-expansion). Doesn't depend on Phase 11.

## Next concrete actions (in priority order)

1. **Push final Phase 11 commits + flip PR #18 from draft to ready.** Local serial conformance run is green across all 8 services; CI on push.
2. **Phase 12 — Cross-cloud migration cell expansion.** See [PLAN.md § Phase 12](PLAN.md#phase-12--cross-cloud-migration-cell-expansion). Independent of Phase 11.
3. **Phase 12 follow-on tracks (absorbed Phase 11 deferrals — see [PLAN.md § Phase 12](PLAN.md#phase-12--cross-cloud-migration-cell-expansion--phase-11-follow-ons)):**
   - **12.A — Broader Azure spec-driven migration.** Pilot landed for SetSecret in `azure_keyvault`; migrate the rest of `azure_keyvault`'s handlers + the other 7 Azure frontends to the generated `ServerInterface`. Pattern + manifest format established (`cmd/azure-codegen/main.go` + `services/secrets/azure-codegen.json`).
   - **12.B — GCP routing emitter + 8 adapter migrations.** Discovery JSON → routing-only Go reusing `google.golang.org/api/<svc>/v1` wire types.
   - **12.C — Production RS256 JWKS** for real Google + Microsoft Entra tokens. Verifiers run test-mode HS256 today; verifier comments document the production paths (`google.golang.org/api/idtoken.Validate`, Microsoft's JWKS).

## Invariants snapshot

- Never auto-merge; user merges every PR.
- **One PR at a time.** Phase 11 = one PR; all sub-phases on `phase-11`.
- **One plan file.** PLAN.md is the only roadmap doc; per-phase plans live inline as a section.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API; real backends only; tests from official client surfaces.
- **Reuse over reinvention.**

## Open bugs (2)

- **BUG-8** (P3) — apigateway/gcp-tf-frontend. `hashicorp/google` API Gateway endpoint-override + real OAuth signing. **Track A only** (real-cloud lanes).
- **BUG-15** (P3) — queue/gcp-frontend. GCP Pub/Sub retention plan/apply asymmetry. Partial fix landed in Phase 10.3.

## Resumable tracks

- **Track A — Cloud test accounts.** Real-cloud Apply lanes against real AWS / GCP / Azure accounts; also the home for real-signed signature-verification conformance. Blocks BUG-8 closure.
- **Track B — Coding-agent automation.**
- **Renovate coverage of vendored specs.** Renovate tracks Go modules + GitHub Actions today; vendored specs in `services/<svc>/spec/` are manual. Wire spec freshness into CI (compare vendored hash vs upstream HEAD; alert on drift).

## Session-resume checklist

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open` — Phase 11 PR (`#18`) is the open item; verify CI status.
3. `git checkout phase-11`.
4. Read STATUS snapshot + this file's "Where we are".
5. Skim BUGS open (2 entries).
6. If Phase 11 is merged, read [PLAN.md § Phase 12](PLAN.md#phase-12--cross-cloud-migration-cell-expansion) to pick the next ◻ sub-task.
