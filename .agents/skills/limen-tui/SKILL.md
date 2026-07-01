---
name: limen-tui
description: >
  Drive the limen TUI inside a tmux session. Use when testing TUI behavior,
  verifying a feature works in the real app, or automating a limen task run.
  Covers: start, send keys, capture output, stop.
---

## Scripts

All scripts live in `scripts/` at the repo root. Run from repo root.

| Script | Purpose |
|---|---|
| `scripts/tui-start.sh [session] [limen-args...]` | Launch TUI in detached tmux session |
| `scripts/tui-send.sh <session> <keys>` | Send keystrokes to the session |
| `scripts/tui-capture.sh [session] [--png /path.png]` | Capture current screen as text (+ optional PNG) |
| `scripts/tui-stop.sh [session]` | Kill the session |

Default session name: `limen-tui`.

## Workflow

### 1. Start

```bash
./scripts/tui-start.sh limen-tui \
  --task-id test-fix-add \
  --prompt "Fix add function" \
  --repo-path /tmp/test-repo \
  --mock=false \
  --worker-backend pi \
  --validator-cmd "go test ./..."
```

Wait ~1s for TUI to render before capturing.

### 2. Inspect

```bash
./scripts/tui-capture.sh limen-tui
```

Returns plain text of current screen. Read it to determine TUI state (active tab, timeline events, error messages).

For image:
```bash
./scripts/tui-capture.sh limen-tui --png /tmp/limen-snap.png
```

### 3. Send keys

```bash
./scripts/tui-send.sh limen-tui "Tab"        # switch tab
./scripts/tui-send.sh limen-tui "q"          # quit
./scripts/tui-send.sh limen-tui "Enter"      # confirm
./scripts/tui-send.sh limen-tui "C-c"        # interrupt
```

Key names follow tmux `send-keys` conventions.

### 4. Stop

```bash
./scripts/tui-stop.sh limen-tui
```

## Typical verify loop

```bash
# Reset repo
./scripts/reset-test-repo.sh

# Start TUI
./scripts/tui-start.sh limen-tui --task-id test-fix-add --prompt "Fix add function" --repo-path /tmp/test-repo --mock=false --worker-backend pi --validator-cmd "go test ./..."

# Poll until terminal state (COMMITTED, FAILED_ESCALATED, or "done")
until ./scripts/tui-capture.sh limen-tui | grep -q "COMMITTED\|FAILED_ESCALATED\|done"; do sleep 3; done

# Capture and inspect final state
./scripts/tui-capture.sh limen-tui

# ALWAYS send q to quit the app gracefully before stopping the session
./scripts/tui-send.sh limen-tui "q"
sleep 1
./scripts/tui-stop.sh limen-tui
```

## Notes

- `tui-capture.sh` strips ANSI escape codes -> clean text for agent parsing.
- PNG capture requires a running X display (`$DISPLAY` set). In headless CI, skip `--png`.
- Session name must be unique per concurrent run; pass a distinct name if running multiple tasks.
- `tui-start.sh` kills any existing session with the same name before starting.
- **Always send `q` before stopping**: `./scripts/tui-send.sh limen-tui "q"` gracefully quits the app and cleans up worktrees. `tui-stop.sh` only kills the tmux session — if you call it without `q` first, the orchestrator goroutine and Pi subprocess may not be cleaned up. Always use `tui-send.sh q` then `tui-stop.sh`.
