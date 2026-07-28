# Limen developer targets.
#
# The canonical gate lives in scripts/run-gates.sh so that local runs and CI
# (.github/workflows/test.yml) execute the same code. These targets are thin
# wrappers over it, plus a couple of conveniences that CI does not run.
#
# Run `make help` for the target list.

SHELL := /bin/bash

# Total statement-coverage floor, in percent. Keep in sync with
# .github/workflows/test.yml and scripts/run-gates.sh.
COVERAGE_FLOOR ?= 54
COVERAGE_OUT ?= coverage.out

# Pinned so local runs match CI; see .golangci.yml.
GOLANGCI_LINT_VERSION ?= v1.64.2

# Invoked via `bash` so the gate works even if the executable bit is lost
# (fresh clone on a filesystem without exec permissions, zip export, etc).
GATES := bash ./scripts/run-gates.sh

export COVERAGE_FLOOR
export COVERAGE_OUT

.DEFAULT_GOAL := help

.PHONY: help build vet gofmt-check test coverage lint lint-install check check-all all clean-coverage

help: ## List the available targets
	@echo "Limen make targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "} {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

build: ## Compile every package (go build ./...)
	@$(GATES) build

vet: ## Run go vet over every package
	@$(GATES) vet

gofmt-check: ## Fail if any Go file is not gofmt'd
	@$(GATES) gofmt

test: ## Run the short, race-enabled suite and write the coverage profile
	@$(GATES) test

coverage: ## Enforce the total coverage floor (reuses an existing profile)
	@$(GATES) coverage

lint: ## Run golangci-lint using .golangci.yml (not part of the CI gate)
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found; install it with: make lint-install"; \
		exit 1; \
	}
	golangci-lint run

lint-install: ## Install the pinned golangci-lint into $$(go env GOPATH)/bin
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

check: ## Run the full CI gate: build, vet, gofmt, test, coverage
	@$(MAKE) build
	@$(MAKE) vet
	@$(MAKE) gofmt-check
	@$(MAKE) test
	@$(MAKE) coverage

check-all: ## The full gate plus golangci-lint
	@$(MAKE) check
	@$(MAKE) lint

all: ## Alias for check
	@$(MAKE) check

clean-coverage: ## Remove the generated coverage profile
	@rm -f $(COVERAGE_OUT)
