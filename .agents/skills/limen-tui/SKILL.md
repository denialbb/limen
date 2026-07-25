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
| `scripts/tui-start.sh [session] [limen-args...]` | Launch TUI in detached tmux session (220x50 → split layout; sets `PYTHONPATH` for mock mode) |
| `scripts/tui-send.sh <session> <keys>` | Send keystrokes to the session |
| `scripts/tui-capture.sh [session] [--png /path.png]` | Capture current screen as text (+ optional PNG) |
| `scripts/tui-wait.sh <session> [--pattern <regex>] [--timeout <seconds>]` | Block until the screen matches a pattern (default terminal states); exit 0 on match, 124 on timeout |
| `scripts/tui-stop.sh [session]` | Kill the session |
| `scripts/test-tui-e2e.sh` | Headless, zero-token e2e gate: drives the mock lifecycle to `COMMITTED` in pipe mode (no tmux) and tmux mode; exits non-zero on any failed assertion. Run by `.github/workflows/tui-e2e.yml` on push/PR to `main`. |

Default session name: `limen-tui`.

`scripts/reset-test-repo.sh <path> [lang]` scaffolds the fixture repo:
`lang=go` (default) → `main.go`/`main_test.go`, validate with `go test ./...`;
`lang=python` → `calc.py`/`test_calc.py`, validate with `python -m pytest -x`.

`tui-start.sh` exports `PYTHONPATH=<root>/src` into the pane before launching
`./limen`, so **mock mode works out of the box**. Without it the mock backend
dies with `remote: subprocess exited before result event` (the Python
`limen.mock.*` subprocesses can't import their package). If you launch `./limen`
by hand for mock mode, set `PYTHONPATH=src` yourself.

## Layout & keybindings

The TUI picks its layout from the terminal size at startup:

- **Split layout** when width ≥ 120 **and** height ≥ 30 (thresholds in
  `internal/tui/theme.go`). `tui-start.sh` spawns a 220x50 pane, so it is
  **always split**.
- **Tab layout** otherwise (a narrower/shorter terminal).

Capture text differs between the two — know which layout you are in before
asserting on rendered content.

| Action | Split keys | Tab keys |
|---|---|---|
| Focus / select region | `w` (focus workers panel) | `1` router · `2` worker · `3` validator · `4` timeline |
| Cycle regions | — | `]` next · `[` prev |
| Move cursor / scroll | `j` / `k` (or ↓/↑) | `j` / `k` (or ↓/↑) |
| Open worker detail | `enter` (when workers focused) | — (worker tab shows detail inline) |
| Back to timeline focus | `esc` | — |
| Help overlay (toggle) | `?` (Esc/`?` to close) | `?` (Esc/`?` to close) |
| Quit (graceful) | `q` (or `ctrl+c`) | `q` (or `ctrl+c`) |

Split-mode drill-down: `w` → `j`/`k` to pick a worker → `enter` for its detail
(tool calls, file edits, criterion results) → `esc` back.

## Workflow

### 1. Start

Use a **repo-relative** scratch path so agent sandboxes (pi policy / claude
permission mode) don't block it — `tmp/` is gitignored:

```bash
./scripts/reset-test-repo.sh tmp/test-repo
./scripts/tui-start.sh limen-tui \
  --task-id test-fix-add \
  --prompt "Fix add function" \
  --repo-path tmp/test-repo \
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
# Reset repo (repo-relative, gitignored scratch path)
./scripts/reset-test-repo.sh tmp/test-repo

# Start TUI
./scripts/tui-start.sh limen-tui --task-id test-fix-add --prompt "Fix add function" --repo-path tmp/test-repo --mock=false --worker-backend pi --validator-cmd "go test ./..."

# Block until a terminal state (default pattern COMMITTED|FAILED_ESCALATED|FINALIZED)
./scripts/tui-wait.sh limen-tui --timeout 600   # exit 0 on match, 124 on timeout

# Capture and inspect final state
./scripts/tui-capture.sh limen-tui

# ALWAYS send q to quit the app gracefully before stopping the session
./scripts/tui-send.sh limen-tui "q"
sleep 1
./scripts/tui-stop.sh limen-tui
```

For the mock backend the same loop works with `--mock` (the default) and no
`--worker-backend`/`--validator-cmd`; `tui-wait.sh` still blocks on the same
terminal states.

## Notes

- `tui-capture.sh` strips ANSI escape codes -> clean text for agent parsing.
- Prefer `tui-wait.sh` over a hand-rolled `until ... grep ...; do sleep; done`
  loop: one blocking call, a timeout, and a clear non-zero exit on failure.
- PNG capture requires a running X display (`$DISPLAY` set). In headless CI, skip `--png`.
- Session name must be unique per concurrent run; pass a distinct name if running multiple tasks.
- `tui-start.sh` kills any existing session with the same name before starting.
- **Always send `q` before stopping**: `./scripts/tui-send.sh limen-tui "q"` gracefully quits the app and cleans up worktrees. `tui-stop.sh` only kills the tmux session — if you call it without `q` first, the orchestrator goroutine and Pi subprocess may not be cleaned up. Always use `tui-send.sh q` then `tui-stop.sh`.
