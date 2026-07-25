#!/usr/bin/env bash
# Headless end-to-end gate for the limen TUI mock lifecycle.
#
# Deterministic and zero-token: drives the built-in Python mock backend
# (all-mock router/worker/validator, spike transcript) to a terminal state and
# asserts on the observable output. No agent CLIs, no network, no API keys.
#
# Two modes run back to back:
#
#   pipe  — the non-TTY fallback (main.go runTaskOneShot -> runTaskWithConfig).
#           Piping ./limen's stdout makes isTTY() false, so Bubble Tea is
#           skipped and the run emits log-style lines. We assert the mock
#           lifecycle reaches COMMITTED. Always runs (no tmux needed).
#
#   tmux  — the full interactive TUI via the tui-*.sh harness at 220x50 (split
#           layout). We block on tui-wait.sh, capture the screen, assert the
#           COMMITTED header + timeline lines, then quit with q and tear the
#           session down. Runs only when tmux is on PATH; skipped otherwise.
#
# Exits non-zero with a clear "FAIL: ..." message on the first failed assertion.
#
# Env knobs: FIXTURE (fixture repo path, default tmp/test-repo-e2e),
# TUI_E2E_TIMEOUT (tmux wait seconds, default 120).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

FIXTURE="${FIXTURE:-tmp/test-repo-e2e}"
TIMEOUT="${TUI_E2E_TIMEOUT:-120}"
SESSION="limen-e2e-$$"
# ./limen passes its environment to the `python -m limen.mock.*` subprocesses,
# which must import the package under src/. Bake PYTHONPATH in here (same
# rationale as tui-start.sh) so the mock backend can start.
PYPATH="$ROOT/src${PYTHONPATH:+:$PYTHONPATH}"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

cleanup() {
    tmux kill-session -t "$SESSION" 2>/dev/null || true
}
trap cleanup EXIT

# Build the binary if it is missing or stale-by-absence. CI builds it first, but
# a bare local invocation should still work.
if [[ ! -x ./limen ]]; then
    echo "== building ./limen =="
    go build -o limen ./cmd/limen || fail "go build failed"
fi

# ---------------------------------------------------------------------------
# pipe mode (always)
# ---------------------------------------------------------------------------
echo "== pipe mode =="
./scripts/reset-test-repo.sh "$FIXTURE" >/dev/null 2>&1 || fail "fixture reset failed"

# Pipe through `tee` so stdout is not a TTY (forces runTaskOneShot) and we still
# see the output live. Log lines land on stderr, so fold 2>&1 into the pipe.
PIPE_LOG="$FIXTURE/.e2e-pipe.log"
set +e
env PYTHONPATH="$PYPATH" ./limen \
    --task-id e2e-pipe \
    --prompt 'Fix the add function' \
    --repo-path "$FIXTURE" \
    --mock=true \
    --coverage-floor=0 \
    --confidence-floor=0 2>&1 | tee "$PIPE_LOG"
rc=${PIPESTATUS[0]}
set -e
[[ $rc -eq 0 ]] || fail "pipe: ./limen exited $rc"

grep -q "Starting task e2e-pipe" "$PIPE_LOG" || fail "pipe: missing 'Starting task' log line"
grep -q "Task completed with state: COMMITTED" "$PIPE_LOG" \
    || fail "pipe: lifecycle did not reach COMMITTED"
pass "pipe mode reached COMMITTED"

# ---------------------------------------------------------------------------
# tmux mode (when tmux is present)
# ---------------------------------------------------------------------------
if ! command -v tmux >/dev/null 2>&1; then
    echo "== tmux mode: SKIPPED (tmux not on PATH) =="
    echo "ALL PASS (pipe only)"
    exit 0
fi

echo "== tmux mode =="
./scripts/reset-test-repo.sh "$FIXTURE" >/dev/null 2>&1 || fail "fixture reset failed"

./scripts/tui-start.sh "$SESSION" \
    --task-id e2e-tmux \
    --prompt 'Fix the add function' \
    --repo-path "$FIXTURE" \
    --mock=true \
    --coverage-floor=0 \
    --confidence-floor=0 >/dev/null

# Block until the lifecycle reaches a terminal state (or time out).
if ! ./scripts/tui-wait.sh "$SESSION" --timeout "$TIMEOUT"; then
    echo "---- capture on timeout ----" >&2
    ./scripts/tui-capture.sh "$SESSION" >&2 || true
    fail "tmux: did not reach a terminal state within ${TIMEOUT}s"
fi

CAP="$(./scripts/tui-capture.sh "$SESSION")"
echo "$CAP"

echo "$CAP" | grep -q "COMMITTED" || fail "tmux: header/timeline never showed COMMITTED"
# The reject->revise mock run edits solution.txt; the timeline records the flow.
echo "$CAP" | grep -Eq "PROCEED|context built|Context built" \
    || fail "tmux: timeline missing router activity"
pass "tmux mode reached COMMITTED with rendered timeline"

# Quit the app cleanly (worktrees removed) then kill the session.
./scripts/tui-send.sh "$SESSION" "q"
./scripts/tui-stop.sh "$SESSION" >/dev/null 2>&1 || true

echo "ALL PASS"
