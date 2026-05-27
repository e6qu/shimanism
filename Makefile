# shimanism — Makefile
#
# Phase 1.1 baseline. Each target should be cheap and side-effect-free
# enough to run on every PR. Phase-specific targets (codegen, conformance)
# get added as their sub-phases land.

.PHONY: all build test vet lint typecheck fmt check clean fetch-specs license-check codegen codegen-check spec-freshness inject-provenance sockerless sockerless-storage help

# Default: the full local pre-push lane.
all: vet test build

# Print a one-line summary of every PHONY target.
help:
	@echo "shimanism Makefile targets"
	@echo ""
	@echo "Build + test:"
	@echo "  all                 vet + test + build (default)"
	@echo "  build               go build ./... → bin/"
	@echo "  test                go test ./..."
	@echo "  vet                 go vet ./..."
	@echo "  lint                golangci-lint run ./..."
	@echo "  typecheck           go build + go vet (fast no-test check)"
	@echo "  fmt                 gofmt -w ."
	@echo "  check               repo hygiene (rebased + symlinks)"
	@echo "  clean               rm -rf bin/"
	@echo ""
	@echo "Codegen:"
	@echo "  codegen             regenerate every services/<svc>/gen/ from SOURCES.md"
	@echo "  codegen-check       codegen + assert no diff (CI's determinism guard)"
	@echo "  inject-provenance   re-sync every spec's \`_provenance\` from SOURCES.md"
	@echo "  fetch-specs         re-fetch every vendored spec (review diff before commit)"
	@echo "  spec-freshness      report drift between vendored specs and upstream HEAD"
	@echo ""
	@echo "Licensing:"
	@echo "  license-check       verify every linked dep carries an AGPL-compatible license"
	@echo ""
	@echo "Sockerless validation lane (opt-in; requires a local clone of github.com/e6qu/sockerless):"
	@echo "  sockerless          build sims + run shim's TestSockerless_* lanes"
	@echo "  sockerless-storage  compatibility alias for the same lane"

# Build every package + the shim binary into ./bin/.
build:
	@mkdir -p bin
	go build -o bin/ ./...

# Test every package. Phase 1.1 has none yet; this is a placeholder that
# also doubles as a guard against accidentally breaking the test target.
test:
	go test ./...

# Static analysis with the toolchain's built-in vet.
vet:
	go vet ./...

# Linting. golangci-lint config lives in .golangci.yml at the repo
# root. The CI lint job pins the version; if golangci-lint isn't on
# PATH locally, install it from the binary release the version
# pinned there expects.
lint:
	@if ! command -v golangci-lint >/dev/null 2>&1 && [ ! -x "$$(go env GOPATH)/bin/golangci-lint" ]; then \
		echo "golangci-lint not found. Install via:"; \
		echo "  curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b \$$(go env GOPATH)/bin v2.10.1"; \
		exit 1; \
	fi
	@golangci-lint run ./...

# Type-check every package without running tests. Catches build
# errors faster than `go test` in pre-commit / pre-push hooks.
typecheck:
	go build ./...
	go vet ./...

fmt:
	gofmt -w .

# Repo hygiene checks that mirror the GitHub Actions `checks` workflow.
# Useful to run locally before pushing.
check:
	@bash scripts/check-rebased.sh
	@bash scripts/check-symlinks.sh

clean:
	rm -rf bin/

# Re-fetch every vendored spec from its upstream, updating the pinned SHA
# in each services/<svc>/spec/SOURCES.md. Review the diff before
# committing. Per-service refresh: call the matching scripts/fetch-*.sh
# directly.
#
# Three pipelines exist today:
#   - scripts/fetch-aws-spec.sh   <aws-service> <local-dir> [<ref>]
#   - scripts/fetch-azure-spec.sh <upstream-path> <local-dir> <filename> [<ref>]
#   - scripts/fetch-gcp-discovery.sh <host> <local-dir> <filename>
#
# Each runs cmd/inject-provenance after download so the spec's
# `_provenance` top-level key stays current.
fetch-specs:
	bash scripts/fetch-aws-spec.sh s3 services/storage

# Regenerate every services/<svc>/gen/*.gen.go from the vendored specs.
# Output is deterministic: same spec + same manifest = byte-identical
# output. CI's codegen-determinism test guards against drift.
#
# Operation list comes from services/<svc>/codegen.json (the manifest
# the determinism test also reads). The codegen tool emits only the
# operations listed there — the intersection of what exists across
# AWS / GCP / Azure / Kubernetes peer for this service. See
# services/storage/OPERATIONS.md for the rationale.
codegen:
	@for manifest in $$(find services -maxdepth 2 -name codegen.json | sort); do \
		svc_dir=$$(dirname $$manifest); \
		spec=$$(jq -r '.spec' $$manifest); \
		pkg=$$(jq -r '.package' $$manifest); \
		out=$$(jq -r '.out' $$manifest); \
		ops=$$(jq -r '.operations | join(",")' $$manifest); \
		if [ ! -f $$spec ]; then \
			echo "codegen: skipping $$manifest (spec $$spec not vendored yet)"; \
			continue; \
		fi; \
		commit=$$(grep -oE '`[0-9a-f]{40}`' $$svc_dir/spec/SOURCES.md 2>/dev/null | head -1 | tr -d '`'); \
		if [ -z "$$commit" ]; then commit=0000000000000000000000000000000000000000; fi; \
		echo "codegen: $$manifest -> $$out"; \
		go run ./cmd/codegen \
			-spec=$$spec \
			-out=$$out \
			-pkg=$$pkg \
			-ops="$$ops" \
			-commit="$$commit" || exit $$?; \
	done
	@for manifest in $$(find services -maxdepth 2 -name 'azure*codegen.json' | sort); do \
		svc_dir=$$(dirname $$manifest); \
		spec=$$(jq -r '.spec' $$manifest); \
		pkg=$$(jq -r '.package' $$manifest); \
		out=$$(jq -r '.out' $$manifest); \
		if [ ! -f $$spec ]; then \
			echo "azure-codegen: skipping $$manifest (spec $$spec not vendored yet)"; \
			continue; \
		fi; \
		spec_base=$$(basename $$spec); \
		spec_dir=$$(dirname $$spec); \
		commit=$$(grep -F "$$spec_base" $$spec_dir/SOURCES.md 2>/dev/null | grep -oE '`[0-9a-f]{40}`' | head -1 | tr -d '`'); \
		if [ -z "$$commit" ]; then commit=0000000000000000000000000000000000000000; fi; \
		echo "azure-codegen: $$manifest -> $$out"; \
		go run ./cmd/azure-codegen \
			-spec=$$spec \
			-out=$$out \
			-pkg=$$pkg \
			-commit="$$commit" || exit $$?; \
	done
	@for manifest in $$(find services -maxdepth 2 -name gcp-codegen.json | sort); do \
		spec=$$(jq -r '.spec' $$manifest); \
		pkg=$$(jq -r '.package' $$manifest); \
		out=$$(jq -r '.out' $$manifest); \
		if [ ! -f $$spec ]; then \
			echo "gcp-codegen: skipping $$manifest (spec $$spec not vendored yet)"; \
			continue; \
		fi; \
		echo "gcp-codegen: $$manifest -> $$out"; \
		go run ./cmd/gcp-codegen \
			-spec=$$spec \
			-out=$$out \
			-pkg=$$pkg || exit $$?; \
	done

# Regenerate every gen file and assert no diff against the
# committed copy. Useful both locally before pushing and in CI
# (the `codegen deterministic` job). A diff here means either
# an emitter introduced non-determinism, a vendored spec was
# bumped without committing the regenerated output, or a
# SOURCES.md row was edited without re-running inject-provenance.
# All three warrant a `make codegen inject-provenance && git add
# services/ && git commit` cycle.
codegen-check: codegen inject-provenance
	@git diff --exit-code -- services >/dev/null || ( \
		echo "regenerated gen / provenance files differ from committed copy:"; \
		git diff --stat -- services; \
		echo ""; \
		echo "fix: run 'make codegen inject-provenance' and commit the result"; \
		exit 1; \
	)

# Refresh the `_provenance` top-level key in every vendored spec
# from each services/<svc>/spec/SOURCES.md (and the common-types
# tree's SOURCES.md). Idempotent — files already current are left
# alone. Use after manually editing a SOURCES.md row.
inject-provenance:
	@for sources in $$(find services -name SOURCES.md | sort); do \
		dir=$$(dirname $$sources); \
		echo "==> $$sources"; \
		go run ./cmd/inject-provenance -sources="$$sources" -dir="$$dir" || exit $$?; \
	done

# Report drift between vendored specs and their upstream HEADs. Reads
# the SOURCES.md table in every services/<svc>/spec/ directory, asks
# GitHub for the latest commit touching each upstream path, and prints
# a line per spec (ok / DRIFT / skip). Discovery revisions skip (no
# SHA to compare). Requires `gh` + `jq` on PATH.
#
# Informational only; use to gate fetch-specs PRs. CI integration
# lives under .github/workflows/spec-freshness.yml when it lands.
spec-freshness:
	@bash scripts/check-spec-freshness.sh

# Verify every linked Go dependency carries a license on the allowlist in
# docs/compatible-licenses.md. Uses Google's go-licenses tool. Installed on
# demand if not present.
#
# Allowlist must stay in sync with docs/compatible-licenses.md by hand —
# this list is what tooling reads; the doc is what humans read.
LICENSE_ALLOWLIST := Apache-2.0,BSD-2-Clause,BSD-3-Clause,0BSD,ISC,MIT,MIT-0,MPL-2.0,LGPL-2.1-or-later,LGPL-3.0-or-later,GPL-2.0-or-later,GPL-3.0-or-later,GPL-3.0-only,GPL-3.0,AGPL-3.0-only,AGPL-3.0-or-later,AGPL-3.0,LGPL-2.1,LGPL-3.0,Unlicense,CC0-1.0,Zlib

# Deprecated-form SPDX IDs included above (GPL-3.0, AGPL-3.0, LGPL-2.1,
# LGPL-3.0): some tools emit these unsuffixed forms even though the
# current SPDX list canonicalises them as "*-only". They map unambiguously
# to compatible licenses. GPL-2.0 (without -only / -or-later suffix) is
# deliberately NOT allowlisted because it is ambiguous between the
# compatible "-or-later" form and the incompatible "-only" form.

license-check:
	@if ! command -v go-licenses >/dev/null 2>&1 && [ ! -x "$$(go env GOPATH)/bin/go-licenses" ]; then \
		echo "Installing go-licenses..."; \
		go install github.com/google/go-licenses@latest; \
	fi
	@echo "Allowed licenses: $(LICENSE_ALLOWLIST)"
	@PATH="$$(go env GOPATH)/bin:$$PATH" \
		go-licenses check --include_tests --allowed_licenses="$(LICENSE_ALLOWLIST)" ./...
	@echo "OK: all dependencies carry allowlisted licenses."

# Sockerless validation lane. Opt-in; requires a local clone of
# github.com/e6qu/sockerless (set SOCKERLESS_DIR to override the
# default /tmp/sockerless). See docs/sockerless-validation.md and
# scripts/run-sockerless-storage.sh for details.
sockerless: sockerless-storage

sockerless-storage:
	@bash scripts/run-sockerless-storage.sh
