#!/usr/bin/env bash
# Kill the limen TUI tmux session.
# Usage: tui-stop.sh [session-name]
set -e

SESSION="${1:-limen-tui}"

if ! tmux has-session -t "$SESSION" 2>/dev/null; then
    echo "Session '$SESSION' is not running."
    exit 0
fi

echo "Session '$SESSION' is running — stopping..."
# Send 'q' to let the TUI exit cleanly, then kill the session.
tmux send-keys -t "$SESSION" "q" "" 2>/dev/null
sleep 0.5
tmux kill-session -t "$SESSION" 2>/dev/null || true
echo "Session '$SESSION' stopped."
