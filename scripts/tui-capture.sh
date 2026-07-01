#!/usr/bin/env bash
# Capture current TUI state from a tmux session.
# Outputs plain text to stdout (readable by agents).
# Optionally saves a PNG screenshot alongside.
#
# Usage: tui-capture.sh [session-name] [--png /path/to/out.png]
set -e

SESSION="${1:-limen-tui}"
PNG=""

shift || true
while [[ $# -gt 0 ]]; do
    case "$1" in
        --png) PNG="$2"; shift 2 ;;
        *) echo "Unknown arg: $1" >&2; exit 1 ;;
    esac
done

# Text capture — preserves ANSI escape codes; strip them for clean agent read.
tmux capture-pane -t "$SESSION" -p -e | sed 's/\x1b\[[0-9;]*[mKHJABCDGfhilnpqrs]//g'

if [[ -n "$PNG" ]]; then
    # Grab the tmux pane window id and screenshot it via import (ImageMagick).
    WINDOW_ID=$(xdotool search --name "$(tmux display-message -t "$SESSION" -p '#{window_name}')" 2>/dev/null | head -1 || true)
    if [[ -n "$WINDOW_ID" ]]; then
        import -window "$WINDOW_ID" "$PNG"
        echo "[screenshot saved: $PNG]" >&2
    else
        # Fallback: screenshot the whole screen
        import -window root "$PNG"
        echo "[screenshot saved (full screen): $PNG]" >&2
    fi
fi
