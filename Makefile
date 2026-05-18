# shimanism — Makefile
#
# Phase 1.1 baseline. Each target should be cheap and side-effect-free
# enough to run on every PR. Phase-specific targets (codegen, conformance)
# get added as their sub-phases land.

.PHONY: all build test vet lint fmt check clean

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
