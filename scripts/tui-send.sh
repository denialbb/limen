#!/usr/bin/env bash
# Send keys to the limen TUI tmux session.
# Usage: tui-send.sh [session-name] <keys>
# Keys examples: "q" "Tab" "Enter" "Up" "Down" "C-c"
# Special key names follow tmux send-keys conventions.
set -e

if [ $# -lt 2 ]; then
    echo "Usage: $0 <session-name> <keys>" >&2
    exit 1
fi

SESSION="$1"
shift

tmux send-keys -t "$SESSION" "$@"
