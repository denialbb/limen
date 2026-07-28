# TUI testing

How to drive the limen TUI under test: the automated gate, and the manual
harness for when a failure needs investigating.

Everything here runs against the built-in **Python mock backend** — mock router,
worker and validator replaying a spike transcript. No agent CLIs, no network, no
API keys, no tokens spent.

## The automated gate

```bash
./scripts/test-tui-e2e.sh
```

This is step 9 of [the QA checklist](checklist.md) and the only TUI command that
CI-adjacent tooling runs. It builds `./limen` if missing, then runs two modes
back to back:

| Mode | What it exercises | Requires |
|---|---|---|
| `pipe` | The non-TTY fallback: piping stdout makes `isTTY()` false, so Bubble Tea is skipped and the run emits log lines | nothing beyond Go + git |
| `tmux` | The real interactive TUI at 220x50, driven through the `tui-*.sh` harness | `tmux` on PATH |

When tmux is absent the second mode is skipped and the script prints
`ALL PASS (pipe only)` — still exit 0. On any failed assertion it prints
`FAIL: <what>` and exits non-zero; on a tmux timeout it also dumps the captured
screen to stderr.

**Env knobs:**

| Variable | Default | Meaning |
|---|---|---|
| `FIXTURE` | `tmp/test-repo-e2e` | Fixture repo path |
| `TUI_E2E_TIMEOUT` | `120` | Seconds to wait for a terminal state in tmux mode |

## The manual harness

Six scripts, composable. Use them when the gate fails and you need to watch the
run, or when developing a new TUI view.

### 1. Scaffold a fixture repo

```bash
./scripts/reset-test-repo.sh [repo-path] [lang]
```

Creates a throwaway git repo containing a deliberately buggy `add` function
(it subtracts) plus a test pinning the correct behavior — so a worker has
something real to fix and a validator has something real to check.

- `repo-path` — default `/tmp/test-repo`
- `lang` — `go` (default) writes `main.go` + `main_test.go`, validated with
  `go test ./...`; `python` writes `calc.py` + `test_calc.py`, validated with
  `python -m pytest -x`

It deletes the path first and clears limen artifacts (`limen.db*`, `.limen/`),
so it is safe to re-run between attempts. Anything unsaved under that path is
destroyed — point it at a scratch directory, never a real checkout.

### 2. Start the TUI

```bash
./scripts/tui-start.sh [session-name] [limen-args...]
```

Starts `./limen` inside a detached tmux session (default name `limen-tui`) and
echoes the session name. Two details it handles for you:

- **Geometry is 220x50**, which clears the split-layout thresholds (width ≥ 120,
  height ≥ 30), so the TUI always comes up in split mode. A smaller pane silently
  changes the layout and your assertions with it.
- **`PYTHONPATH` includes `src/`**, baked in via `env` so it survives whatever
  login shell the pane uses. Without it, mock mode dies with
  `remote: subprocess exited before result event`.

Typical invocation:

```bash
./scripts/reset-test-repo.sh /tmp/test-repo
./scripts/tui-start.sh qa-session \
    --task-id qa-1 \
    --prompt 'Fix the add function' \
    --repo-path /tmp/test-repo \
    --mock=true \
    --coverage-floor=0 \
    --confidence-floor=0
```

`--coverage-floor=0 --confidence-floor=0` keep the router from escalating on the
tiny fixture corpus; drop them when you are specifically testing escalation.

### 3. Wait for a terminal state

```bash
./scripts/tui-wait.sh <session> [--pattern <regex>] [--timeout <seconds>]
```

Polls the capture until its text matches, then exits 0. **Exits 124 on timeout.**
Use this instead of `sleep` — a fixed sleep is the single biggest source of flake
in TUI tests.

- `--pattern` — extended regex, default `COMMITTED|FAILED_ESCALATED|FINALIZED`
- `--timeout` — default `600` seconds
- `TUI_WAIT_INTERVAL` — poll interval, default `3` seconds
- `TUI_CAPTURE_CMD` — capture command to poll, default `scripts/tui-capture.sh`

Waiting for an intermediate state works the same way:

```bash
./scripts/tui-wait.sh qa-session --pattern 'WORKER_RUNNING' --timeout 60
```

### 4. Capture the screen

```bash
./scripts/tui-capture.sh [session] [--png /path/out.png]
```

Prints the pane as plain text on stdout with ANSI escapes stripped, which is what
makes the output greppable and diffable. `--png` additionally screenshots the
window via ImageMagick `import` (needs X and `xdotool`; falls back to a
full-screen grab).

### 5. Send keys

```bash
./scripts/tui-send.sh <session> <keys>
```

Key names follow tmux conventions: `q`, `Tab`, `Enter`, `Up`, `Down`, `C-c`.
Both arguments are required.

### 6. Stop

```bash
./scripts/tui-stop.sh [session]
```

Sends `q` so the app exits cleanly (worktrees are removed on a clean quit), then
kills the session. Safe to call when nothing is running — it says so and exits 0.

**Always stop your sessions.** A leaked session holds the fixture repo's
worktrees and will confuse the next run.

## Asserting on captured output

Capture to a variable, then grep. This is exactly what `test-tui-e2e.sh` does:

```bash
CAP="$(./scripts/tui-capture.sh qa-session)"

echo "$CAP" | grep -q "COMMITTED"            || { echo "FAIL: never committed"; exit 1; }
echo "$CAP" | grep -Eq "PROCEED|[Cc]ontext built" || { echo "FAIL: no router activity"; exit 1; }
```

Patterns the gate relies on, and what each one proves:

| Pattern | Where | Proves |
|---|---|---|
| `Starting task <id>` | pipe-mode log | The run actually started under the expected task ID |
| `Task completed with state: COMMITTED` | pipe-mode log | The lifecycle reached a successful terminal state |
| `COMMITTED` | tmux capture | The header/timeline rendered the terminal state |
| `PROCEED\|context built\|Context built` | tmux capture | The timeline recorded router activity, not just an empty frame |
| `FAILED_ESCALATED` | either | The escalation path terminated as intended |

### Writing assertions that do not flake

- **Wait on a pattern, never on a duration.** `tui-wait.sh` exists for this.
- **Assert on a state word, not on layout.** Box-drawing characters, column
  positions and wrapping all move when the view changes; `COMMITTED` does not.
- **Capture once into a variable** and grep it repeatedly, rather than capturing
  per assertion — otherwise two assertions can observe different frames.
- **Case-tolerant patterns for prose.** Timeline text has been both
  `context built` and `Context built`; match `[Cc]ontext built`.
- **Do not assert on absence right after an action.** "X is not on screen" is
  true before X renders, so it passes for the wrong reason. Wait for a positive
  marker first, then assert the absence.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `remote: subprocess exited before result event` | `PYTHONPATH` missing `src/` | Use `tui-start.sh` rather than launching `./limen` directly in the pane |
| `tui-wait.sh` exits 124 | Never reached a terminal state | Capture the screen and look at the last timeline line; check the fixture repo exists |
| Layout looks wrong / split view missing | Pane smaller than 120x30 | Start via `tui-start.sh`, which forces 220x50 |
| tmux mode skipped entirely | `tmux` not on PATH | Install tmux; pipe mode alone still gates the lifecycle |
| Stale results between runs | Fixture repo or session left over | Re-run `reset-test-repo.sh` and `tui-stop.sh` before starting |
| `no server running on /tmp/tmux-*/default` | Session already killed | Harmless from `tui-stop.sh`; otherwise the app exited early — check the pipe-mode log |
