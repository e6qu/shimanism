# shimanism — Makefile
#
# Phase 1.1 baseline. Each target should be cheap and side-effect-free
# enough to run on every PR. Phase-specific targets (codegen, conformance)
# get added as their sub-phases land.

.PHONY: all build test vet lint fmt check clean fetch-specs license-check codegen

# Default: the full local pre-push lane.
all: vet test build

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

# Linting. Phase 1.1 has no Go code worth linting; golangci-lint gets
# wired in alongside the first translation code (Phase 1.5).
lint:
	@echo "lint: no Go lint configured yet (Phase 1.1 placeholder — see PLAN.md)"

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
# committing. Per-service refresh: call scripts/fetch-aws-spec.sh
# directly.
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
	@SOURCE_COMMIT=$$(grep -oE '`[0-9a-f]{40}`' services/storage/spec/SOURCES.md | head -1 | tr -d '`'); \
	OPS=$$(jq -r '.operations | join(",")' services/storage/codegen.json); \
	go run ./cmd/codegen \
		-spec=services/storage/spec/aws-s3.smithy.json \
		-out=services/storage/gen/aws_s3.gen.go \
		-pkg=gen \
		-ops="$$OPS" \
		-commit="$$SOURCE_COMMIT"

# Verify every linked Go dependency carries a license on the allowlist in
# doc/COMPATIBLE_LICENSES.md. Uses Google's go-licenses tool. Installed on
# demand if not present.
#
# Allowlist must stay in sync with doc/COMPATIBLE_LICENSES.md by hand —
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
