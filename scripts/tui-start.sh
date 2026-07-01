#!/usr/bin/env bash
# Start limen TUI in a named tmux session.
# Usage: tui-start.sh [session-name] [limen-args...]
# Default session name: limen-tui
set -e

SESSION="${1:-limen-tui}"
shift || true

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

tmux has-session -t "$SESSION" 2>/dev/null && tmux kill-session -t "$SESSION"

tmux new-session -d -s "$SESSION" -x 220 -y 50

# Quote arguments safely for tmux send-keys
ARGS=""
for arg in "$@"; do
    printf -v quoted "%q" "$arg"
    ARGS="$ARGS $quoted"
done

tmux send-keys -t "$SESSION" "cd '$ROOT' && ./limen $ARGS" Enter

echo "$SESSION"
