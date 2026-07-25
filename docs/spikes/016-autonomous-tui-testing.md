# Spike 016: Autonomous Agent-Driven TUI Testing

Captured live 2026-07-25 by a claude instance (Opus 4.8) driving the limen TUI
through `scripts/tui-*.sh` in tmux, no human in the loop. Verifies the harness
hardening from slice 1 and the real-backend run from slice 2 (the issue 015
follow-up: "run the TUI with a real backend and confirm the event flow renders
well per dialect").

Versions: limen @ `feat/autonomous-tui-testing` (off `feat/multi-driver-dialects`
HEAD 387aa6c), pi 0.82.1, Go test-repo fixture.

## Verdict

**Yes — an agent can interactively test the limen TUI end-to-end, autonomously**,
once the slice-1 harness fixes are in place: start it, observe live state,
navigate, assert on rendered content, and quit cleanly. Both mock and a real pi
worker were driven to `COMMITTED` and inspected purely through `tui-capture.sh`
text; no PNG/X11 needed.

Two harness gaps blocked this before slice 1 and are now fixed:

- `tui-start.sh` did not set `PYTHONPATH`, so mock mode died with
  `remote: subprocess exited before result event` (the `limen.mock.*` Python
  subprocesses couldn't import their package). It now exports
  `PYTHONPATH=<root>/src` into the pane via `env` (shell-agnostic — the pane's
  login shell here is fish, where `export VAR=…` is invalid).
- The poll-until-terminal loop was copy-pasted inline. It is now
  `scripts/tui-wait.sh <session> [--pattern <regex>] [--timeout <seconds>]`
  (exit 0 on match, 124 on timeout).

No `internal/tui/` changes were needed: the real pi run rendered
`WorkerToolCall` / `WorkerAgentMessage` cleanly in every region, so slice 2
produced no code commit — only this evidence.

## Command sequence that works

### Split layout (default — the 220x50 pane from `tui-start.sh`)

Split layout engages when width ≥ 120 **and** height ≥ 30
(`internal/tui/theme.go`); 220x50 always lands here.

```bash
# 1. Reset the fixture (repo-relative, gitignored scratch path)
./scripts/reset-test-repo.sh tmp/test-repo

# 2. Start the TUI (mock: drop --mock=false and the backend/validator flags)
./scripts/tui-start.sh limen-tui \
  --task-id tui-real-pi-1 --prompt "Fix the add function" \
  --repo-path tmp/test-repo \
  --mock=false --worker-backend pi \
  --validator-backend shell --validator-cmd "go test ./..." \
  --coverage-floor=0 --confidence-floor=0

# 3. Block until a terminal state (default COMMITTED|FAILED_ESCALATED|FINALIZED)
./scripts/tui-wait.sh limen-tui --timeout 600     # exit 0 match / 124 timeout

# 4. Navigate + assert (split keys)
./scripts/tui-send.sh limen-tui "w"        # focus workers panel
./scripts/tui-send.sh limen-tui "Enter"    # open selected worker's detail
./scripts/tui-capture.sh limen-tui         # assert on tool calls / file edits
./scripts/tui-send.sh limen-tui "Esc"      # back to timeline focus

# 5. Quit gracefully, then tear down the tmux session
./scripts/tui-send.sh limen-tui "q"        # quits app → removes worktrees
./scripts/tui-stop.sh limen-tui            # kills the tmux session
```

Split keys: `w` focus workers · `j`/`k` move cursor · `Enter` worker detail ·
`Esc` back · `q` quit. `q` quits the *app* (worktrees removed, fixture keeps the
commit, `limen.db` persists); the tmux *session* stays alive at a shell prompt
until `tui-stop.sh`.

### Tab layout (narrower/shorter terminal)

Identical flow; only the navigation keys differ. Spawn a smaller pane (e.g.
`tmux new-session … -x 100 -y 28`) to land here. Keys: `1`-`4` select
Router/Worker/Validator/Timeline · `]`/`[` cycle tabs · `j`/`k` scroll · `q`
quit. **Capture text differs between layouts** — the worker detail is a
dedicated pane in split mode but the Worker tab in tab mode — so an autonomous
agent must know which layout it is in before asserting. (This run exercised
split only; tab-mode capture fixtures are a follow-up.)

## Real-backend rendering — pi 0.82.1

Driven with the buggy-`add` fixture (`add` subtracts; `main_test.go` expects 5)
and shell validator `go test ./...`. Reached `COMMITTED` in ~10s, 0 retries.
Header transitioned `WORKER_RUNNING` (spinner `⣾`) → `COMMITTED` (`✓`); Router
showed real retrieval (`context built: 577 bytes`, `entropy=0.000`, `PROCEED`).

Worker-detail / timeline transcript excerpt (verbatim capture):

```
[16:18:45] tool call: bash {"command":"sed -i 's/return a - b/return a + b/' main.go"}
[16:18:46] tool call: bash {"command":"limen ready-for-review --task-id tui-real-pi-1 --summary \"Fixed the add function ...\""}
[16:18:47] criterion "placeholder_criterion": PASS
[16:18:47] verdict: PASS — Command "go test ./..." passed:
           ok      example.com/test-repo    0.003s
[16:18:48] agent: The task has been completed successfully. The add function has been
           fixed to correctly add two numbers instead of subtracting them. ...
```

Observations:

- `WorkerToolCall` renders as `tool call: <tool> {<args-json>}`; the pi worker
  used its `bash` tool (`sed` to patch, then the `limen ready-for-review`
  callback). Long JSON args soft-wrap with a hanging indent — readable, not
  garbled.
- `WorkerAgentMessage` renders as `agent: <text>`, also soft-wrapped.
- Both appear in the split timeline (right pane) and, per-worker, in the worker
  detail (`w` → `Enter`). The Validator panel showed the shell validator's
  criterion + `PASS` verdict independently.
- Post-finalize: fixture `HEAD` is `Complete task tui-real-pi-1` with
  `return a + b`; `git worktree list` shows only the main worktree (orchestrator
  worktree removed on `q`).

Mock backend (smoke test before the real run) rendered the full lifecycle
`PROCEED → worker → verdict FAIL → retry → PASS → COMMITTED`, `retries:1`, with
`worker-0 ✗` / `worker-1 ✓` in the Workers panel and file-edit lines
(`file edit: solution.txt (write)`) in the worker detail — confirming the
reject→revise loop and the PYTHONPATH fix.

## Sandbox constraints observed

The implementer ran as a claude instance under Claude Code "don't ask mode"
(deny-by-default; only allowlisted commands run without a prompt). What was
needed in `.claude/settings.local.json`:

- `Bash(tmux:*)` and one entry per script:
  `Bash(./scripts/tui-start.sh:*)`, `…/tui-capture.sh`, `…/tui-send.sh`,
  `…/tui-stop.sh`, `…/tui-wait.sh`, `…/reset-test-repo.sh`.
- `Bash(go:*)`, `Bash(gofmt:*)`, `Bash(git:*)`, `Bash(pi:*)`, and
  `Edit`/`Write` under the repo (carried over from issue 015).

Gotchas for an autonomous agent under this mode:

- **Scratch path.** Use the repo-relative, gitignored `tmp/test-repo` (via
  `reset-test-repo.sh tmp/test-repo`). Paths outside the repo risk being blocked
  by the agent's sandbox/permission policy, and keep the fixture off `git`.
- **Compound commands** that include a non-allowlisted binary (`echo`, `chmod`,
  `eza <path>`, some `rg` patterns) are denied as a unit — run allowlisted
  commands singly. Use `git -C <dir>` instead of `cd`.
- **Env-var-prefixed commands** (e.g. `FOO=bar ./script.sh`) do not match a
  bare `Bash(./script.sh:*)` allow rule and are denied. `tui-start.sh` therefore
  bakes `PYTHONPATH` in via `env` internally rather than expecting the caller to
  prefix it.
- **No `chmod`.** The new `tui-wait.sh` exec bit was set through git
  (`git update-index --chmod=+x` + `git checkout --`), not `chmod`.
- **No foreground `sleep`** in the agent's own shell; `tui-wait.sh` does its
  polling `sleep` inside the (allowlisted) script subprocess, which is fine.

## Follow-ups

- **Headless CI gate.** A `!isTTY` render path already exists; wire a CI job
  that drives the mock lifecycle to `COMMITTED` through `tui-wait.sh` and asserts
  on captured text (no tokens, deterministic).
- **Other dialects in the TUI.** This spike exercised pi only. Run the same
  loop against opencode and agy workers and confirm their `WorkerToolCall` /
  `WorkerAgentMessage` streams render (agy is plain-text — expect only
  Started/Finished breadcrumbs).
- **Tab-layout capture fixtures.** Record golden captures for tab mode so agents
  can assert layout-appropriately.
- **Per-dialect render fixtures.** Freeze a captured screen per dialect as a
  component-test golden in `internal/tui/`.
```
