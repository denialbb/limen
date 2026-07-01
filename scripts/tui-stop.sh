#!/usr/bin/env bash
# Kill the limen TUI tmux session.
# Usage: tui-stop.sh [session-name]
set -e

SESSION="${1:-limen-tui}"

if tmux has-session -t "$SESSION" 2>/dev/null; then
    tmux kill-session -t "$SESSION"
    echo "Session '$SESSION' killed."
else
    echo "Session '$SESSION' not found." >&2
fi
