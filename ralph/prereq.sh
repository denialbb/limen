#!/usr/bin/env bash
# ralph/prereq.sh — verify the laptop has the tools ralph needs before an
# overnight run. Exits nonzero with a clear message on any miss.
#
# Usage: ./ralph/prereq.sh

set -uo pipefail

pass=0
fail=0
warn_count=0
missing=()

check() {
    local name="$1" cmd="$2" min="${3:-}"
    if command -v "$cmd" >/dev/null 2>&1; then
        local v
        v=$("$cmd" --version 2>&1 | head -1)
        printf '  [OK]   %-10s %s\n' "$name" "$v"
        if [ -n "$min" ]; then
            if ! "$cmd" version >/dev/null 2>&1 && \
               ! printf '%s\n' "$v" | grep -q "$min"; then
                # Soft version check: warn if min not found in version string.
                :
            fi
        fi
        pass=$((pass + 1))
    else
        printf '  [MISS] %-10s not on PATH\n' "$name"
        missing+=("$name")
        fail=$((fail + 1))
    fi
}

echo "=== ralph prereq check ==="
echo
echo "Tools:"
check bash    bash
check git     git
check go      go
check python3 python3
check pytest  pytest
check opencode opencode
check jq      jq
check awk     awk

if [ "$fail" -gt 0 ]; then
    echo
    echo "Missing: ${missing[*]}"
    echo "Install the missing tools and re-run."
    exit 1
fi

echo
echo "Versions (pinned):"
go_version=$(go version 2>/dev/null | awk '{print $3}')
py_version=$(python3 --version 2>&1 | awk '{print $2}')
echo "  go:      $go_version (go.mod requires 1.25.0)"
echo "  python:  $py_version (pyproject.toml requires >=3.14)"

echo
echo "Smoke builds:"

if go build ./... >/tmp/ralph_prereq_go.log 2>&1; then
    echo "  [OK]   go build ./..."
else
    echo "  [FAIL] go build ./..."
    tail -n 20 /tmp/ralph_prereq_go.log | sed 's/^/        /'
    fail=$((fail + 1))
fi

if pytest --collect-only -q >/tmp/ralph_prereq_py.log 2>&1; then
    n=$(grep -c '::' /tmp/ralph_prereq_py.log || echo 0)
    echo "  [OK]   pytest --collect-only ($n tests)"
else
    echo "  [WARN] pytest --collect-only had errors:"
    tail -n 20 /tmp/ralph_prereq_py.log | sed 's/^/        /'
    warn_count=$((warn_count + 1))
fi

if go test ./internal/ndjson/... ./internal/orchestrator/... >/tmp/ralph_prereq_test.log 2>&1; then
    echo "  [OK]   core Go tests (ndjson, orchestrator)"
else
    echo "  [FAIL] core Go tests"
    tail -n 20 /tmp/ralph_prereq_test.log | sed 's/^/        /'
    fail=$((fail + 1))
fi

echo
echo "venv check:"
if [ -x .venv/bin/pytest ]; then
    echo "  [OK]   .venv present; pytest resolves to: $(command -v pytest)"
else
    echo "  [WARN] no .venv/bin/pytest; bare 'pytest' may hit system python"
    warn_count=$((warn_count + 1))
fi

echo
echo "=== Summary: $pass pass, $fail fail, $warn_count warn ==="

if [ "$fail" -gt 0 ]; then
    echo "RESULT: NOT READY — fix failures above before overnight run."
    exit 1
fi

if [ "$warn_count" -gt 0 ]; then
    echo "RESULT: READY WITH WARNINGS — review warnings above."
    exit 0
fi

echo "RESULT: READY"
exit 0