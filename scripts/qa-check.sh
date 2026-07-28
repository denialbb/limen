#!/usr/bin/env bash
#
# qa-check.sh — run the QA checklist end to end and report pass/fail per step.
#
# This is the executable form of docs/qa/checklist.md. Steps 1-5 delegate to
# scripts/run-gates.sh (the same gate CI and `make check` run) rather than
# re-implementing `go build ./...` and friends — one definition of the gate.
#
# Usage:
#   scripts/qa-check.sh                     # every step
#   scripts/qa-check.sh --list              # print the step names and exit
#   scripts/qa-check.sh --only build,vet    # run just these steps
#   scripts/qa-check.sh --skip mutation     # run everything except these
#   scripts/qa-check.sh --strict            # a SKIP counts as a failure
#   scripts/qa-check.sh --fail-fast         # stop at the first failure
#
# Exit status:
#   0  every selected step passed (skips allowed unless --strict)
#   1  at least one step failed
#   2  bad usage
#
# Environment:
#   COVERAGE_FLOOR           minimum total coverage, percent (default: 54)
#   COVERAGE_OUT             coverage profile path (default: <repo>/coverage.out)
#   MUTATION_MAX_SURVIVORS   allowed surviving mutants (default: 2)
#   QA_MUTATION_PKG          package pattern for mutation (default: ./internal/retrieval/...)

set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

COVERAGE_FLOOR="${COVERAGE_FLOOR:-54}"
COVERAGE_OUT="${COVERAGE_OUT:-$repo_root/coverage.out}"
MUTATION_MAX_SURVIVORS="${MUTATION_MAX_SURVIVORS:-2}"
QA_MUTATION_PKG="${QA_MUTATION_PKG:-./internal/retrieval/...}"
export COVERAGE_FLOOR COVERAGE_OUT

# The ordered step list. Keep in sync with docs/qa/checklist.md.
STEPS=(build gofmt vet test coverage acceptance lint integration tui-e2e mutation)

STRICT=0
FAIL_FAST=0
ONLY=""
SKIP=""

# Results, parallel to the steps actually run.
declare -a RESULT_NAME=()
declare -a RESULT_STATUS=()
declare -a RESULT_NOTE=()

# last_status is the outcome of the most recent step. Tracked in a scalar rather
# than read back as RESULT_STATUS[-1] so the script does not require the
# negative-index support of bash 4.3+ (macOS still ships bash 3.2).
last_status=""

usage() { sed -n '2,26p' "${BASH_SOURCE[0]}"; }

die() {
	echo "qa-check: $*" >&2
	exit 2
}

# --- output helpers --------------------------------------------------------
# Colour only when stdout is a terminal, so piping to a log stays readable.
if [ -t 1 ]; then
	C_RESET=$'\033[0m'
	C_PASS=$'\033[32m'
	C_FAIL=$'\033[31m'
	C_SKIP=$'\033[33m'
	C_HEAD=$'\033[1m'
else
	C_RESET="" C_PASS="" C_FAIL="" C_SKIP="" C_HEAD=""
fi

banner() { printf '\n%s==> %s%s\n' "$C_HEAD" "$1" "$C_RESET"; }

record() { # record <name> <status> <note>
	RESULT_NAME+=("$1")
	RESULT_STATUS+=("$2")
	RESULT_NOTE+=("$3")
	case "$2" in
	PASS) printf '%sPASS%s  %s\n' "$C_PASS" "$C_RESET" "$1" ;;
	FAIL) printf '%sFAIL%s  %s — %s\n' "$C_FAIL" "$C_RESET" "$1" "$3" ;;
	SKIP) printf '%sSKIP%s  %s — %s\n' "$C_SKIP" "$C_RESET" "$1" "$3" ;;
	esac
}

# selected reports whether a step should run under --only/--skip.
selected() {
	local name="$1"
	if [ -n "$ONLY" ] && [[ ",$ONLY," != *",$name,"* ]]; then
		return 1
	fi
	if [ -n "$SKIP" ] && [[ ",$SKIP," == *",$name,"* ]]; then
		return 1
	fi
	return 0
}

# have reports whether a command exists on PATH.
have() { command -v "$1" >/dev/null 2>&1; }

# --- steps -----------------------------------------------------------------
# Each step function returns 0 on pass and non-zero on fail; run_step grades it.
# Availability of an external tool is decided by run_step BEFORE dispatch (see
# the tool argument), never by a sentinel exit code — golangci-lint and
# go-mutesting use their own non-zero codes to mean "findings", and conflating
# those with "not installed" would turn a real failure into a silent skip.
#
# Sub-scripts are invoked through `bash` so the checklist still works if the
# executable bit did not survive checkout — the same rationale as the Makefile.

step_build() { bash ./scripts/run-gates.sh build; }
step_gofmt() { bash ./scripts/run-gates.sh gofmt; }
step_vet() { bash ./scripts/run-gates.sh vet; }
step_test() { bash ./scripts/run-gates.sh test; }
step_coverage() { bash ./scripts/run-gates.sh coverage; }

step_acceptance() {
	echo "go test -count=1 ./internal/acceptance/  (Gherkin scenarios)"
	go test -count=1 ./internal/acceptance/
}

step_integration() {
	echo "go test -count=1 ./internal/integration/  (no -short: e2e variants run)"
	go test -count=1 ./internal/integration/
}

step_tui_e2e() {
	echo "scripts/test-tui-e2e.sh  (pipe mode + tmux mode, mock backend)"
	bash ./scripts/test-tui-e2e.sh
}

step_lint() { golangci-lint run; }

# step_mutation runs go-mutesting and enforces the survivor ceiling.
#
# go-mutesting reports a trailing summary of the form:
#   The mutation score is 0.90 (18 passed; 2 failed; 0 duplicated, 0 skipped; total is 20)
# where "failed" is the count of mutants that SURVIVED (no test caught them).
# If that summary cannot be parsed the step fails loudly with the raw tail
# rather than assuming success — a silent pass here would be worse than useless.
step_mutation() {
	local out
	out="$(go-mutesting "$QA_MUTATION_PKG" 2>&1)"
	echo "$out" | tail -20

	local summary survivors
	summary="$(echo "$out" | grep -E 'The mutation score is' | tail -1)"
	if [ -z "$summary" ]; then
		echo "could not find a mutation-score summary in the output above" >&2
		return 1
	fi

	survivors="$(echo "$summary" | sed -n 's/.*; \([0-9][0-9]*\) failed;.*/\1/p')"
	if [ -z "$survivors" ]; then
		echo "could not parse the survivor count from: $summary" >&2
		return 1
	fi

	if [ "$survivors" -gt "$MUTATION_MAX_SURVIVORS" ]; then
		echo "$survivors surviving mutants, ceiling is $MUTATION_MAX_SURVIVORS" >&2
		return 1
	fi
	echo "OK: $survivors surviving mutants <= $MUTATION_MAX_SURVIVORS ceiling"
}

# run_step dispatches one step, times it, and records the outcome.
#
#   run_step <name> <fn> [required-tool] [install-hint]
#
# When required-tool is given and absent from PATH the step is not run: SKIP
# normally, FAIL under --strict. A skip is never silent — it lands in the
# summary and in the closing "skipped:" line either way.
run_step() {
	local name="$1" fn="$2" tool="${3:-}" hint="${4:-}"
	if ! selected "$name"; then
		return 0
	fi

	banner "$name"

	if [ -n "$tool" ] && ! have "$tool"; then
		local note="$tool is not installed"
		[ -n "$hint" ] && note="$note (install: $hint)"
		if [ "$STRICT" -eq 1 ]; then
			record "$name" FAIL "$note — required by --strict"
			last_status=FAIL
		else
			record "$name" SKIP "$note"
			last_status=SKIP
		fi
	else
		local start rc elapsed
		start=$SECONDS
		"$fn"
		rc=$?
		elapsed=$((SECONDS - start))
		if [ "$rc" -eq 0 ]; then
			record "$name" PASS "${elapsed}s"
			last_status=PASS
		else
			record "$name" FAIL "exit $rc after ${elapsed}s"
			last_status=FAIL
		fi
	fi

	if [ "$FAIL_FAST" -eq 1 ] && [ "$last_status" = "FAIL" ]; then
		summarize
		exit 1
	fi
	return 0
}

# summarize prints the results table and returns 1 if anything failed.
summarize() {
	local failures=0 skips=0 passes=0
	printf '\n%s==> QA summary%s\n' "$C_HEAD" "$C_RESET"
	local i
	for i in "${!RESULT_NAME[@]}"; do
		case "${RESULT_STATUS[$i]}" in
		PASS)
			passes=$((passes + 1))
			printf '  %sPASS%s  %-12s %s\n' "$C_PASS" "$C_RESET" "${RESULT_NAME[$i]}" "${RESULT_NOTE[$i]}"
			;;
		SKIP)
			skips=$((skips + 1))
			printf '  %sSKIP%s  %-12s %s\n' "$C_SKIP" "$C_RESET" "${RESULT_NAME[$i]}" "${RESULT_NOTE[$i]}"
			;;
		FAIL)
			failures=$((failures + 1))
			printf '  %sFAIL%s  %-12s %s\n' "$C_FAIL" "$C_RESET" "${RESULT_NAME[$i]}" "${RESULT_NOTE[$i]}"
			;;
		esac
	done

	printf '\n  %d passed, %d failed, %d skipped\n' "$passes" "$failures" "$skips"

	if [ "$skips" -gt 0 ]; then
		local skipped=""
		for i in "${!RESULT_NAME[@]}"; do
			[ "${RESULT_STATUS[$i]}" = "SKIP" ] && skipped="$skipped ${RESULT_NAME[$i]}"
		done
		printf '  skipped:%s (a skip is not a pass; re-run with --strict to require them)\n' "$skipped"
	fi

	if [ "$failures" -gt 0 ]; then
		printf '\n%sQA FAILED%s\n' "$C_FAIL" "$C_RESET"
		return 1
	fi
	printf '\n%sQA PASSED%s\n' "$C_PASS" "$C_RESET"
	return 0
}

# --- argument parsing ------------------------------------------------------
while [ $# -gt 0 ]; do
	case "$1" in
	--list)
		printf '%s\n' "${STEPS[@]}"
		exit 0
		;;
	--only)
		[ $# -ge 2 ] || die "--only needs a comma-separated step list"
		ONLY="$2"
		shift 2
		;;
	--skip)
		[ $# -ge 2 ] || die "--skip needs a comma-separated step list"
		SKIP="$2"
		shift 2
		;;
	--strict)
		STRICT=1
		shift
		;;
	--fail-fast)
		FAIL_FAST=1
		shift
		;;
	-h | --help | help)
		usage
		exit 0
		;;
	*) die "unknown argument: $1 (try --help)" ;;
	esac
done

# Reject unknown step names rather than silently running everything.
validate_names() {
	local list="$1" label="$2" name found known
	[ -z "$list" ] && return 0
	local IFS=','
	for name in $list; do
		found=0
		for known in "${STEPS[@]}"; do
			[ "$name" = "$known" ] && found=1 && break
		done
		[ "$found" -eq 1 ] || die "$label: unknown step '$name' (see --list)"
	done
}
validate_names "$ONLY" "--only"
validate_names "$SKIP" "--skip"

# --- run -------------------------------------------------------------------
printf '%sQA check — %s%s\n' "$C_HEAD" "$(date '+%Y-%m-%d %H:%M:%S')" "$C_RESET"
printf 'repo: %s\n' "$repo_root"
[ -n "$ONLY" ] && printf 'only: %s\n' "$ONLY"
[ -n "$SKIP" ] && printf 'skip: %s\n' "$SKIP"
[ "$STRICT" -eq 1 ] && printf 'mode: strict (skips count as failures)\n'

run_step build step_build
run_step gofmt step_gofmt
run_step vet step_vet
run_step test step_test
run_step coverage step_coverage
run_step acceptance step_acceptance
run_step lint step_lint golangci-lint "make lint-install"
run_step integration step_integration
run_step tui-e2e step_tui_e2e
run_step mutation step_mutation go-mutesting \
	"go install github.com/zimmski/go-mutesting/cmd/go-mutesting@latest"

if [ "${#RESULT_NAME[@]}" -eq 0 ]; then
	die "no steps selected"
fi

summarize
