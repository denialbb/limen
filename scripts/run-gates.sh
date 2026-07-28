#!/usr/bin/env bash
#
# run-gates.sh — reproduce the CI gate locally, exactly.
#
# The CI workflow (.github/workflows/test.yml) invokes the same subcommands, so
# a green run here means a green run there (modulo runner differences).
#
# Usage:
#   scripts/run-gates.sh            # everything: build, vet, gofmt, test, coverage
#   scripts/run-gates.sh build      # go build ./...
#   scripts/run-gates.sh vet        # go vet ./...
#   scripts/run-gates.sh gofmt      # gofmt -l . must emit nothing
#   scripts/run-gates.sh test       # go test -short -race, writes the coverage profile
#   scripts/run-gates.sh coverage   # coverage floor check only
#
# Environment:
#   COVERAGE_FLOOR  minimum total statement coverage, percent (default: 54)
#   COVERAGE_OUT    coverage profile path (default: <repo>/coverage.out)
#
# Exit status is non-zero as soon as any gate fails.

set -euo pipefail

# Always operate from the repo root regardless of the caller's cwd.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

COVERAGE_FLOOR="${COVERAGE_FLOOR:-54}"
COVERAGE_OUT="${COVERAGE_OUT:-$repo_root/coverage.out}"

log() { printf '\n==> %s\n' "$1"; }

gate_build() {
	log "build: go build ./..."
	go build ./...
}

gate_vet() {
	log "vet: go vet ./..."
	go vet ./...
}

gate_gofmt() {
	log "gofmt: gofmt -l ."
	local unformatted
	unformatted="$(gofmt -l .)"
	if [ -n "$unformatted" ]; then
		echo "FAIL: the following files are not gofmt'd:"
		echo "$unformatted"
		echo ""
		echo "Fix with: gofmt -w ."
		return 1
	fi
	echo "OK: all Go files are gofmt'd"
}

# gate_test runs the short, race-enabled suite and writes the coverage profile
# consumed by gate_coverage. -short skips the long e2e and real-git stress
# tests, which gate themselves on testing.Short().
gate_test() {
	log "test: go test -short -race -count=1 ./..."
	go test -short -race -count=1 -coverprofile="$COVERAGE_OUT" ./...
}

# gate_coverage enforces the total statement-coverage floor. It reuses an
# existing profile when one is present so CI can run it as its own step after
# the test step, without paying for a second suite run.
gate_coverage() {
	log "coverage: total >= ${COVERAGE_FLOOR}%"

	if [ ! -f "$COVERAGE_OUT" ]; then
		echo "no coverage profile at $COVERAGE_OUT; generating one"
		go test -short -count=1 -coverprofile="$COVERAGE_OUT" ./...
	fi

	local total
	total="$(go tool cover -func="$COVERAGE_OUT" | awk '/^total:/ {print $3}')"
	total="${total%\%}"

	if [ -z "$total" ]; then
		echo "FAIL: could not read a total from $COVERAGE_OUT"
		return 1
	fi

	# awk rather than bc: bc is not installed on every runner, awk is POSIX.
	if awk -v t="$total" -v f="$COVERAGE_FLOOR" 'BEGIN { exit !(t < f) }'; then
		echo "FAIL: coverage ${total}% is below the ${COVERAGE_FLOOR}% floor"
		return 1
	fi

	echo "OK: coverage ${total}% >= ${COVERAGE_FLOOR}% floor"
}

gate_all() {
	gate_build
	gate_vet
	gate_gofmt
	gate_test
	gate_coverage
	log "all gates passed"
}

case "${1:-all}" in
build) gate_build ;;
vet) gate_vet ;;
gofmt) gate_gofmt ;;
test) gate_test ;;
coverage) gate_coverage ;;
all) gate_all ;;
-h | --help | help)
	sed -n '2,25p' "${BASH_SOURCE[0]}"
	;;
*)
	echo "unknown gate: $1" >&2
	echo "expected one of: all build vet gofmt test coverage" >&2
	exit 2
	;;
esac
