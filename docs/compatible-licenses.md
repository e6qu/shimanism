# Compatible Licenses

shimanism is licensed **AGPL-3.0-only**. Every dependency that gets *linked into* a shimanism binary must use a license compatible with AGPL-3.0. CI enforces this; this document is the source of truth for the allowlist.

> License compatibility is one dimension; broader dependency hygiene (minimum release age, cgo preference, npm via pnpm, no pre-install scripts) is in [`dependency-policy.md`](dependency-policy.md). Both must be satisfied before adding a dep.

> The constraint is on *linked* dependencies — Go modules listed in `go.mod` / `go.sum`. Services we *connect to* over the wire (Vault, MinIO, Postgres, Terraform CLI, the cloud providers themselves) carry no copyleft obligation. Their licenses do not affect shimanism's license. See *Linked vs connected* below.

## TL;DR

**Allowed** — these can ship as dependencies:

| SPDX ID | Common name | Notes |
|---|---|---|
| `0BSD` | BSD Zero Clause | Permissive |
| `Apache-2.0` | Apache License 2.0 | Permissive with explicit patent grant — preferred for new permissive code |
| `BSD-2-Clause` | Simplified BSD | Permissive |
| `BSD-3-Clause` | New BSD | Permissive |
| `ISC` | ISC License | Permissive |
| `MIT` | MIT | Permissive |
| `MIT-0` | MIT No Attribution | Permissive |
| `MPL-2.0` | Mozilla Public License 2.0 | Weak copyleft (file-level). Compatible with AGPL via MPL §3.3. |
| `LGPL-2.1-or-later` | LGPL 2.1+ | Weak copyleft |
| `LGPL-3.0-or-later` | LGPL 3.0+ | Weak copyleft |
| `GPL-2.0-or-later` | GPL 2.0+ ("or later") | Strong copyleft — the "or later" lets us combine under v3 |
| `GPL-3.0-or-later` | GPL 3.0+ | Strong copyleft |
| `GPL-3.0-only` | GPL 3.0 only | Strong copyleft, same family |
| `AGPL-3.0-only` | AGPL 3.0 | shimanism's own license |
| `AGPL-3.0-or-later` | AGPL 3.0+ | Same family |
| `Unlicense` | The Unlicense | Public-domain equivalent |
| `CC0-1.0` | Creative Commons Zero | Public-domain dedication |
| `Zlib` | zlib | Permissive |

**Deprecated-form SPDX IDs also allowlisted** (some tools emit the unsuffixed form): `GPL-3.0` (= `GPL-3.0-only`), `LGPL-2.1` (= `LGPL-2.1-only`), `LGPL-3.0` (= `LGPL-3.0-only`), `AGPL-3.0` (= `AGPL-3.0-only`). These map unambiguously to compatible licenses. **`GPL-2.0` without a suffix is deliberately NOT allowlisted** because it is ambiguous between the compatible "-or-later" form and the incompatible "-only" form.

**Forbidden** — using these in a linked dependency would break shimanism's license:

| SPDX ID / pattern | Why |
|---|---|
| `GPL-2.0-only` | GPLv2 without "or later" is **not** GPLv3-compatible. AGPLv3 inherits this. |
| `LGPL-2.0-only` / `LGPL-2.0` | Same — v2 only is not v3-compatible. |
| `MPL-1.0`, `MPL-1.1` | Pre-2.0 MPL is GPL-incompatible. (MPL 2.0 is fine — verify the version.) |
| `CDDL-1.0`, `CDDL-1.1` | GPL-incompatible by design (chosen as such by Sun). |
| `EPL-1.0`, `EPL-2.0` | Eclipse Public License — patent retaliation makes it GPL-incompatible. |
| `SSPL-1.0` | Server Side Public License (MongoDB) — non-free per OSI/FSF; not GPL-compatible. |
| `BUSL-1.1` | Business Source License — proprietary until the change-date. Not OSI-approved. |
| `Elastic-2.0` | Elastic License v2 — not OSI-approved; field-of-use restrictions. |
| `CC-BY-NC-*` | Non-commercial restriction is non-free. |
| `CC-BY-ND-*` | No-derivatives restriction is non-free. |
| `OSL-3.0` | Open Software License — explicitly GPL-incompatible per the FSF. |
| `Proprietary` / `Unknown` / `NOASSERTION` | Default to "no" unless reviewed. |

When in doubt, consult the [FSF's GPL compatibility matrix](https://www.gnu.org/licenses/license-list.html) and the [SPDX license list](https://spdx.org/licenses/).

## Why these rules

AGPL-3.0 is a strong-copyleft license that inherits its compatibility constraints from GPL-3.0. A dependency we *link into* a shimanism binary forms part of a combined work; if that dependency's license is incompatible with GPL-3.0, we cannot legally distribute the combined binary.

Concretely, this means three things:

1. **Permissive licenses are always fine** (MIT, BSD, Apache 2.0, ISC, etc.) — they impose no terms incompatible with copyleft.
2. **Weak-copyleft licenses are fine if compatible** (MPL 2.0, LGPL 2.1+, LGPL 3.0+) — copyleft applies to their files but not to the linked work as a whole.
3. **Strong-copyleft licenses must be from the GPL family with "or later" or GPL-3 onwards** — GPL-2.0-only is incompatible because the FSF added an explicit upgrade path in GPL-3 that v2-only can't use.

## Linked vs connected

This distinction matters more than the license tables above:

- **Linked** = imported as a Go module via `go.mod`. Compiled into our binary. **License must be on the allowlist.**
- **Connected** = communicated with over a wire protocol (HTTP, gRPC, TCP, AMQP, RESP, …). **No license constraint** — the other process's license never reaches shimanism.

Examples relevant to shimanism:

| Thing | Status | Allowed? |
|---|---|---|
| `aws/aws-sdk-go-v2` (Apache 2.0) | Linked dependency (potential) | ✅ |
| Vault server (MPL 2.0 historically; check current) | Connected service (Phase 2 backend) | ✅ regardless |
| HashiCorp Terraform CLI (BUSL 1.1 since 2023) | Conformance-test driver, separate binary | ✅ — we shell out to it; we never link Terraform code |
| MinIO server (AGPL 3.0) | Connected service (Phase 1 backend) | ✅ |
| HashiCorp Vault Go client library | Linked dependency (if we use it) | Check — MPL 2.0 is fine, but verify the current upstream license |
| NATS server (Apache 2.0) | Connected service (Phase 3/4 backend) | ✅ |
| Smithy Go runtime libraries | Linked dependency (if we use them) | ✅ — Apache 2.0 |

**Heuristic:** if removing the dependency means changing a `go.mod` line, it's linked. If it means changing a network endpoint or shutting down a process, it's connected.

## Vendored specs

We vendor cloud API specs under `services/<svc>/spec/` for reproducibility (see [`services/storage/README.md`](../services/storage/README.md)). Each vendored file retains its upstream license:

| Spec | Upstream license | Notes |
|---|---|---|
| AWS Smithy JSON models (S3, Secrets Manager, SQS, SNS, RDS, ElastiCache, Lambda, API Gateway v2) | Apache 2.0 (from `aws/aws-sdk-go-v2`) | Apache 2.0 is allowlisted. Each `services/<svc>/spec/SOURCES.md` records provenance. |
| GCP protobufs (when added in Phase 9) | Apache 2.0 (from `googleapis/googleapis`) | Same. |
| Azure OpenAPI specs (when added in Phase 10) | MIT (from `Azure/azure-rest-api-specs`) | Same. |

**Generated code derived from a vendored spec inherits the spec's license clause that applies to derivative works.** For Apache-2.0 / MIT specs, that's no practical constraint — generated code can be re-licensed AGPLv3 freely. Each spec's `SOURCES.md` records the upstream URL + pinned commit SHA so attribution is preserved.

## How to add a dependency

1. Check the dep's license before adding it (`go list -m -json <dep>` shows the license string the module declares).
2. Verify SPDX ID against the allowlist above.
3. Run `make license-check` locally — it runs `go-licenses check` with the same allowlist.
4. If a dep's license is **unclassified** or **on the forbidden list**, do not add it. File a BUG explaining the missing capability so we can pick a different dep (or a different approach).

CI enforces the same check on every PR.

## Currently approved transitive dependencies

*(empty — shimanism has no Go dependencies as of Phase 1.2)*

This list updates automatically when `go-licenses report` runs in CI; the maintained file is generated, not hand-edited.

## Notes on common Go ecosystem licenses

- **`google.golang.org/...`** — almost always Apache 2.0. ✅
- **`github.com/aws/...`** — Apache 2.0. ✅
- **`cloud.google.com/go/...`** — Apache 2.0. ✅
- **`github.com/Azure/...`** — MIT. ✅
- **`github.com/hashicorp/...`** — historically MPL 2.0 (still fine — MPL 2.0 is allowlisted), but check current — HashiCorp moved several products to BUSL in 2023 (Terraform itself, Vault, Consul, etc.). Their *client libraries* may or may not have moved. **Always check current upstream**.
- **`github.com/golang/...`** — BSD-3-Clause. ✅
- **`go.uber.org/...`** — MIT. ✅
- **`github.com/spf13/...`** — Apache 2.0 (cobra/viper/pflag). ✅

## When SPDX is ambiguous

Some upstreams ship a `LICENSE` file without an SPDX header. `go-licenses` will report `NOASSERTION` or `Unknown` for these. Treat as forbidden until manually classified. Update this doc with the manual classification + a link to the resolution.
