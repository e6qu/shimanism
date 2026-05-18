# Known Bugs

**1 filed · 0 fixed · 1 open · 0 false positives.**

Status [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · roadmap [PLAN.md](PLAN.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · rules [AGENTS.md](AGENTS.md).

> **Standing rule:** every CI failure, conformance-test failure, fidelity gap, fake/stub, placeholder, silent fallback, skipped test, or incomplete implementation lands here with a one-liner **before** any fix attempt. Workarounds are bugs and get the same treatment. Per-bug fix detail beyond the one-liner: `git log <commit>` or the linked PR.
>
> When a bug surfaces during a coding-agent session, file the BUG before fixing — even if the fix is a single line. The audit trail is what makes "never lie" enforceable.

## Open

| ID | Sev | Area | Source-API | One-liner |
|----|-----|------|------------|-----------|
| 1 | P2 | restxml router | AWS S3 | After x-id stripping, `GET /{Bucket}/{Key+}?tagging=` routes to `GetObject` because GetObject's route templates to `/{Bucket}/{Key+}` and the router doesn't reject extra-query mismatches. Same risk for `?acl=`, `?versioning=` on objects. Fix: have routes also declare "forbidden" query params (or use stricter `=` matching with `x-id` retained when present). Triggered by Terraform AWS provider's bucket-resource read of object tagging. Currently shadowed because the SDK includes `x-id` and the tagging probe ignores the response body. |

## False positives

| Area | Finding | Why it's not a bug |
|------|---------|--------------------|

*(empty)*

## Class-of-bug rules (carried forward)

These are the failure patterns that recur across services. When a new bug fits one of these, tag it with the rule; when a new pattern emerges across two or more bugs, add a rule.

- **Fidelity-to-source-API is P0.** If the shim's response shape, header set, status code, error envelope, or async-operation semantics diverge from the cloud's published API, that is a P0 bug. The spec is the contract; deviation isn't a feature.
- **No fakes, no fallbacks, no skips.** Synthetic responses, hardcoded values, in-memory state where a real backend was specified, conditional `t.Skip` for missing config — all file as bugs and get real fixes. Tests run or fail loud; never silent.
- **Out-of-intersection features must fail loud in the source cloud's error vocabulary.** Fabricating a success response, returning a generic 500, or silently degrading to a partial result are all bugs. The correct answer is the cloud's own "feature not supported" or equivalent.
- **Each shimmed operation requires SDK + CLI + Terraform conformance in the same commit.** A merged operation without all three driver tests is a bug (per [AGENTS.md](AGENTS.md) testing contract).
- **K8s peer parity.** When a shimmed operation works against AWS / GCP / Azure backends but not the K8s peer (and no documented platform limitation explains the gap), that is a bug — not an "optional surface."
- **Spec drift.** When the upstream cloud spec changes shape (new fields, renamed operations, deprecated paths), the codegen pipeline must regenerate and the translation table must be updated in the same PR. Stale generated code is a bug.
- **Cross-backend sweep on every find.** When a translation gap or fidelity defect appears in one (source, backend) pair, the same code paths in the other backend pairs for that service get checked in the same commit.
- **Recorded-interaction drift.** When a cassette / VCR recording silently masks a real-cloud behavior change, the test is a bug. Nightly live runs are the authoritative tier.
- **Incompatible-license dependency.** Adding a Go module whose license is not on the [`doc/COMPATIBLE_LICENSES.md`](doc/COMPATIBLE_LICENSES.md) allowlist is a bug — it would silently break shimanism's AGPL-3.0 license. CI's `licenses` job blocks it. Connected services (not linked) are exempt; the distinction is in the doc.

## Resolved history (compressed)

*(empty — no bugs filed yet)*
