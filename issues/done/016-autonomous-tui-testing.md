# Issue 016: Autonomous Agent-Driven TUI Testing

Status: Ready for implementation
Plan author: pi (planner session, w3:p1)
Implementer: claude instance (herdr pane w4:p1)

## Context

The multi-driver dialects arc (issue 015, branch `feat/multi-driver-dialects`)
landed `WorkerAgentMessage` / `WorkerToolCall` events per dialect, but the TUI
has only ever been exercised against the mock backend and gated Go tests.
Handoff next action #5: run the TUI with a real backend and confirm the event
flow renders well per dialect.

This issue turns that into a repeatable capability: **an agent must be able to
interactively test the limen TUI end-to-end, autonomously** — start it, observe
live state, navigate, assert on rendered content, and quit cleanly, with no
human in the loop.

## Spike findings so far (pi planner, mock backend, 2026-07-24)

Verified working autonomously (via `scripts/tui-*.sh` in tmux):

- Full mock lifecycle renders: PROCEED → worker → verdict FAIL → retry → PASS
  → COMMITTED; auto-switch to timeline on finalize; `✓` in header.
- Split-mode navigation: `w` focus workers panel, `j/k` cursor, `enter` worker
  detail (tool calls, file edits, criterion results), `esc` back.
- `q` quits gracefully: worktree removed, fixture repo left with the
  orchestrator's commit, `limen.db` persisted.
- Terminal-state detection by polling `tui-capture.sh` output for
  `COMMITTED|FAILED_ESCALATED|FINALIZED`.

Gaps found (these are the work):

1. **`tui-start.sh` does not set `PYTHONPATH=src`.** Mock backend crashes with
   `remote: subprocess exited before result event`. The integration test
   injects `PYTHONPATH=<root>/src` (`internal/integration/integration_test.go`);
   the script must too.
2. **Scratch-repo path constraint.** Agent sandboxes (pi policy, claude
   permission mode) may block paths outside the repo. Use a repo-relative
   fixture path: `./scripts/reset-test-repo.sh tmp/test-repo` (`tmp/` is
   gitignored). Document this in the skill.
3. **Layout-dependent keybindings.** Terminals ≥ 120 wide get the split
   layout (keys: `w`/`enter`/`esc`/`j`/`k`); narrower gets tab layout
   (keys: `1`-`4`, `[`, `]`). `tui-start.sh` spawns 220x50 → always split.
   The limen-tui skill doc documents neither the split-mode keys nor the
   layout threshold. An autonomous agent must know which layout it is in;
   document both, and note capture text differs per layout.
4. **No wait helper.** The poll-until-terminal loop from the skill is
   copy-pasted inline each time. Codify it as `scripts/tui-wait.sh
   <session> <pattern> [--timeout N]` so agents have one blocking call.

## Scope

### Slice 1 — harness hardening (TDD where sensible)

- Fix `tui-start.sh`: export `PYTHONPATH="$ROOT/src${PYTHONPATH:+:$PYTHONPATH}"`
  in the tmux command so mock mode works out of the box.
- Add `scripts/tui-wait.sh <session> [--pattern <regex>] [--timeout <seconds>]`:
  polls `tui-capture.sh`, exits 0 on match (default pattern
  `COMMITTED|FAILED_ESCALATED|FINALIZED`), non-zero on timeout. Bash + a small
  bats-style or sh test if the repo has a pattern for script tests; if not,
  manual verification + documented usage is acceptable for scripts.
- Update `.agents/skills/limen-tui/SKILL.md`: split-mode vs tab-mode keys,
  220x50 default → split, repo-relative scratch path (`tmp/test-repo`),
  `tui-wait.sh` usage replacing the inline poll loop, `PYTHONPATH` note.

### Slice 2 — real-backend TUI run (the 015 follow-up)

Run the TUI interactively against a real worker dialect and verify rendering:

```
./scripts/reset-test-repo.sh tmp/test-repo
./scripts/tui-start.sh limen-tui --task-id tui-real-pi-1 \
  --prompt "Fix the add function" --repo-path tmp/test-repo \
  --mock=false --worker-backend pi \
  --validator-backend shell --validator-cmd "go test ./..." \
  --coverage-floor=0 --confidence-floor=0
./scripts/tui-wait.sh limen-tui --timeout 600
```

Acceptance:

- Terminal state reached (COMMITTED expected; the fixture is the known
  buggy-`add` repo, validator `go test ./...`).
- During the run, captures show `WorkerAgentMessage`/`WorkerToolCall` activity
  in the worker regions (split mode: workers panel + worker detail via
  `w`/`enter`; timeline lines for tool calls).
- After finalize: capture the full screen and each focus region for the
  report; verify the fixture repo has the fix commit and worktrees are
  cleaned after `q`.
- If rendering gaps are found (missing events, garbled lines, wrong region),
  fix them TDD-style in `internal/tui/` (component tests exist:
  `internal/tui/component_test.go`, `tui_test.go`) and re-run.

### Slice 3 — spike report

Write `docs/spikes/016-autonomous-tui-testing.md`:

- Verdict: can an agent interactively test the TUI autonomously? (expected
  yes, with the harness fixes)
- The exact command sequence that works (start → wait → navigate → capture →
  q → stop), per layout mode.
- Sandbox constraints observed (path policy, claude permission allowlist for
  `scripts/*` + tmux — note the grants that were needed in
  `.claude/settings.local.json`).
- Real-backend rendering observations per dialect exercised (pi at minimum),
  with a short transcript excerpt.
- Follow-ups: e.g. gate a headless variant in CI (`!isTTY` path already
  exists), opencode/agy dialect TUI passes, per-dialect render fixtures.

## Constraints

- Branch: `feat/autonomous-tui-testing` off `feat/multi-driver-dialects`
  (not yet merged; do not rebase or touch main).
- TDD, one commit per slice. Run `go test ./...` and `gofmt -l .` before
  each commit.
- Do NOT touch `README.md` (uncommitted user changes) or the orchestrator.
- Do NOT commit `tmp/`, `limen.db*`, or the fixture repo.
- Real pi worker runs cost tokens: keep to 1–2 real runs; iterate against
  mock + component tests first.
- The planner (pi) holds merge decisions; stop after slice 3 and report.

## Out of scope

- Merging 015 branch, PR creation.
- TUI v2 control features (pause/approve/reject) from `interactive_tui.md`.
- Multi-dialect matrix beyond pi (opencode/agy are follow-ups).
