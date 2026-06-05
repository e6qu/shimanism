# Code Health Audits

shimanism's normal quality gate already catches common unused-code and correctness problems:

- `golangci-lint` runs `staticcheck`, `unused`, `errcheck`, `govet`, and `ineffassign`.
- `make typecheck` runs `go build ./...` and `go vet ./...`.
- generated code under `services/*/gen/` is excluded from lint noise; generator problems are fixed in the generator, not in generated output.
- `peers/` is a nested module and is intentionally outside the root-module lint pass.

This page defines the heavier audit lane for two recurring maintenance risks: unreachable code and copy-pasted code.

## Policy

These audits are advisory until their baselines are burned down.

```sh
make code-health
make duplication-audit
make deadcode-audit
```

By default, the audit targets print findings but exit zero. This keeps them useful during cleanup without making every unrelated PR red. To make either audit strict:

```sh
DUPLICATION_AUDIT_STRICT=1 make duplication-audit
DEADCODE_AUDIT_STRICT=1 make deadcode-audit
```

Do not delete code solely because a tool reported it. First classify the finding:

| Class | Action |
|---|---|
| Real dead code | Delete it and run the focused package tests plus `make typecheck`. |
| Generated code | Ignore the generated file; fix the generator only if the emitted API should not exist. |
| Public/library entry point | Keep it if external callers, CLI wiring, tests, or planned phase contracts need it. |
| Interface-shaped method | Keep it if the method satisfies a domain/backend/frontend contract, even when no direct call appears. |
| Copy-paste with identical cloud semantics | Refactor only when the abstraction preserves source-cloud fidelity and does not hide errors. |
| Copy-paste with intentionally different clouds | Leave it separate if unifying would blur source-specific behavior. |

If a finding exposes a fake, stub, silent fallback, or fidelity bug, file it in [BUGS.md](../BUGS.md) before fixing. Plain cleanup does not need a BUG entry.

## Tools

### Existing Always-On Lane

`staticcheck` covers many "code does nothing" cases, including unreachable statements, self-assignments, ignored side-effect-free calls, and unused writes. Its `unused` companion reports unused constants, variables, functions, and types. This stays in the normal lint gate.

Sources:

- <https://staticcheck.dev/docs/checks/>
- <https://golangci-lint.run/docs/linters/>

### Duplicate Code

The duplication audit uses `golangci-lint`'s `dupl` linter:

```sh
golangci-lint run --enable-only=dupl ./...
```

Why this first:

- already available in the pinned `golangci-lint` binary.
- respects the repo's existing path exclusions.
- no new Node or Java toolchain.
- reports Go fragments with enough file/line detail to triage.

Current baseline from the first audit:

- `cmd/shim/cache.go` and `cmd/shim/rdbms.go` duplicate the service-runner shape.
- `services/compute/backends/inmem/inmem.go` has repeated list/describe filtering loops.
- `services/compute/backends/k8scompute/k8s.go` has repeated NetworkPolicy ingress/egress conversion.
- `services/secrets/conformance/sockerless_test.go` has repeated Terraform apply test bodies.

`jscpd` and PMD CPD remain valid future options if the repo grows non-Go code or needs HTML/JSON clone reports. They are not the first choice today because they add Node or Java dependency surface for a Go-only cleanup lane.

Sources:

- <https://golangci-lint.run/docs/linters/>
- <https://jscpd.dev/>
- <https://pmd.github.io/pmd/pmd_userdocs_cpd.html>

### Dead Code

The dead-code audit uses the official Go `deadcode` command from `golang.org/x/tools`:

```sh
go run golang.org/x/tools/cmd/deadcode@v0.45.0 -test ./...
```

The wrapper filters `services/*/gen/` output by default:

```sh
DEADCODE_EXCLUDE_RE='/gen/' make deadcode-audit
```

Why audit-only:

- `deadcode` is call-graph based. It reports functions unreachable from the selected roots, not necessarily APIs that are safe to remove.
- shimanism has many library-style backend/frontend packages that are used by CLI configuration, conformance harnesses, or future phase contracts rather than a single obvious `main`.
- generated server stubs intentionally contain more API surface than one test run may call.

Current hand-written findings need triage before deletion. Examples include unused domain error constructors, passthrough harness helpers, and backend packages that the call graph does not currently reach from selected roots.

Sources:

- <https://pkg.go.dev/golang.org/x/tools/cmd/deadcode>
- <https://go.dev/blog/deadcode>
- <https://go.dev/gopls/analyzers>

## Cleanup Order

1. Refactor the `cmd/shim/*` service-runner duplication if it can be done without hiding service-specific frontend/backend errors.
2. Extract focused helpers for repeated Terraform apply bodies in conformance tests.
3. Review compute inmem and K8s duplicate helpers; generic helpers are acceptable only when they preserve clear domain-specific behavior.
4. Triage hand-written `deadcode` findings one package at a time. Prefer deleting a small, verified cluster over broad speculative removals.
5. After duplicate findings reach zero or an intentional baseline, decide whether `dupl` becomes part of `make lint` / CI.

The desired end state is a strict duplicate gate and a periodic dead-code audit, not automatic deletion.
