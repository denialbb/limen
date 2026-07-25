#!/usr/bin/env bash
# Start limen TUI in a named tmux session.
# Usage: tui-start.sh [session-name] [limen-args...]
# Default session name: limen-tui
set -e

SESSION="${1:-limen-tui}"
shift || true

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# The mock backend (--mock, default) launches `python -m limen.mock.*`
# subprocesses that import the package under src/. `./limen` passes its own
# environment to those subprocesses, so PYTHONPATH must include src/ or mock
# mode dies with "remote: subprocess exited before result event". Compute the
# full value here (bash) and hand it to the pane via `env`, which works
# regardless of the pane's login shell (bash, fish, ...).
PYPATH="$ROOT/src${PYTHONPATH:+:$PYTHONPATH}"

tmux has-session -t "$SESSION" 2>/dev/null && tmux kill-session -t "$SESSION"

# 220x50 clears the split-layout thresholds (width >= 120, height >= 30), so the
# TUI always comes up in split mode. See the limen-tui skill for the keys.
tmux new-session -d -s "$SESSION" -x 220 -y 50

# Quote arguments safely for tmux send-keys
ARGS=""
for arg in "$@"; do
    printf -v quoted "%q" "$arg"
    ARGS="$ARGS $quoted"
done

tmux send-keys -t "$SESSION" "cd '$ROOT' && env PYTHONPATH='$PYPATH' ./limen $ARGS" Enter

echo "$SESSION"
