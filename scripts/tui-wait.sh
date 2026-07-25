#!/usr/bin/env bash
# Block until the limen TUI reaches a terminal (or matching) state.
#
# Usage: tui-wait.sh <session> [--pattern <regex>] [--timeout <seconds>]
#
# Polls `tui-capture.sh <session>` and exits 0 as soon as its text matches the
# pattern (extended regex, default COMMITTED|FAILED_ESCALATED|FINALIZED). Exits
# 124 on timeout (default 600s). This replaces the copy-pasted inline
# `until ... grep ...; do sleep; done` loop from the limen-tui skill.
#
# Env knobs (mostly for testing): TUI_WAIT_INTERVAL (poll seconds, default 3),
# TUI_CAPTURE_CMD (capture command to poll, default scripts/tui-capture.sh).
set -euo pipefail

SESSION="${1:-limen-tui}"
shift || true

PATTERN='COMMITTED|FAILED_ESCALATED|FINALIZED'
TIMEOUT=600
INTERVAL="${TUI_WAIT_INTERVAL:-3}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --pattern) PATTERN="$2"; shift 2 ;;
        --timeout) TIMEOUT="$2"; shift 2 ;;
        *) echo "tui-wait: unknown arg: $1" >&2; exit 2 ;;
    esac
done

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CAPTURE="${TUI_CAPTURE_CMD:-$ROOT/scripts/tui-capture.sh}"

deadline=$(( $(date +%s) + TIMEOUT ))
while true; do
    if "$CAPTURE" "$SESSION" 2>/dev/null | grep -Eq "$PATTERN"; then
        exit 0
    fi
    if (( $(date +%s) >= deadline )); then
        echo "tui-wait: timed out after ${TIMEOUT}s waiting for /$PATTERN/ in session '$SESSION'" >&2
        exit 124
    fi
    sleep "$INTERVAL"
done
