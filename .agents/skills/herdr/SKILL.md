---
name: herdr
description: >
  Drive AI coding agents running in herdr terminal workspace panes. Use when
  delegating a task to another agent instance (claude, pi, opencode), watching
  an agent's progress, responding to its blockers, or coordinating
  planner/implementer multi-agent workflows. Covers: discover, prompt, submit,
  wait, read, permission-mode pitfalls.
---

# herdr operations

herdr manages interactive agent sessions in panes. All agent commands return
JSON on stdout — pipe through `python3 -c` or `jq` to extract `result`.

## Command reference

| Command | Purpose |
| --- | --- |
| `herdr agent list` | All agents: `agent`, `agent_status` (idle/working/blocked/done), `pane_id`, `cwd`, session info |
| `herdr agent get <target>` | One agent's detail |
| `herdr agent prompt <target> "<text>"` | **Stage** a prompt into the input box — does NOT submit |
| `herdr agent send-keys <target> Enter` | Submit the staged prompt (also: `C-c` interrupt, etc.) |
| `herdr agent wait <target> --until idle --until blocked --timeout <ms>` | Block until a matching state; without `--timeout` waits indefinitely |
| `herdr agent read <target>` | Current terminal screen text (raw, may include ANSI noise — `tail -c` it) |
| `herdr agent explain <target>` | Debug agent detection state |
| `herdr tab list` / `herdr session list` | Tabs / named sessions |

`<target>` is a `pane_id` (e.g. `w4:p1`) or a unique agent name. Parse `list`
output for status:

```bash
herdr agent list | python3 -c "import json,sys; d=json.load(sys.stdin); \
  [print(a['agent'], a['pane_id'], '→', a['agent_status'], a['cwd']) for a in d['result']['agents']]"
```

## Delegation workflow (proven pattern)

1. **Write the spec to a file in the repo first** (issue/ADR/doc). The agent
   only knows what is in its context — a durable file beats a long prompt and
   survives context compaction.
2. **Find the target**: `herdr agent list` — verify `cwd` is the right repo and
   status is `idle`.
3. **Task it**: short prompt pointing at the spec file + branch name +
   conventions (TDD, commit per slice, "stop and ask if X contradicts Y").

   ```bash
   herdr agent prompt w4:p1 "Read issues/015-....md and implement it. Branch feat/x, TDD, one commit per slice. Report per slice."
   herdr agent send-keys w4:p1 Enter   # REQUIRED — prompt alone does not submit
   ```

4. **Confirm it started**: `agent list` shows `working`; `agent read` shows
   sensible first actions (reading the spec, branching). If still `idle` after
   ~15s, the Enter didn't land — resend.
5. **Watch loop**:

   ```bash
   herdr agent wait w4:p1 --until idle --until blocked --timeout 300000
   herdr agent read w4:p1 | tail -c 2000
   git log --oneline -n 5   # ground truth on progress
   ```

   On each wake: read the screen, answer questions, unblock, repeat. Commits are
   a better progress signal than screen chatter.
6. **Review independently when `done`**: run the test suite yourself, check the
   diffs, verify claimed acceptance criteria. Then send feedback as a prompt
   (numbered action items, what NOT to touch), verify the fixup commit.

## Gotchas (hard-won)

- **`prompt` stages, `send-keys Enter` submits.** Forgetting Enter is the #1
  failure mode — the prompt sits in the input box and the agent stays `idle`.
- **"don't ask" permission mode = auto-DENY, not auto-approve.** A Claude Code
  instance in this mode silently refuses non-allowlisted tools and then stops
  to ask for permission it cannot obtain. Symptom: agent reports "auto-denied"
  or stalls. Fix: edit the repo's `.claude/settings.local.json`
  (`"Bash(go:*)"`, `"Bash(git:*)"`, `"Edit(//abs/path/**)"`,
  `"Write(//abs/path/**)"`); grants take effect without a restart. Grant
  progressively (Bash first, Edit/Write when it hits the write wall) so each
  grant is a deliberate decision.
- **Env-var-prefixed commands bypass allowlist prefixes**:
  `LIMEN_E2E_REAL_AGENTS=1 go test ...` does not match `Bash(go:*)`. Run gated
  suites yourself, or allowlist the exact prefixed form.
- **`agent wait` does not track turns**: if the agent is already working, the
  current turn's completion matches — you may wake mid-task. Always confirm
  with `agent read` + `git log`.
- **Long turns are normal** (10–25 min for a multi-slice arc). Use generous
  `--timeout` (5–10 min) and treat timeout-with-`working` as "still going",
  not failure.
- **Interrupt politely**: `send-keys <target> Escape` (or `C-c`) rather than
  killing the pane; the agent keeps its session and can resume.
- **pi↔pi coordination**: use the `intercom` tool instead (`action: list/send/
  ask`) — herdr is for driving foreign agent CLIs.

## Planner + implementer topology

Keep one writer per repo. The herdr-driven agent writes code; you plan, spec,
review, and give feedback — never edit the same files concurrently. If the
implementer asks a design question mid-run, answer decisively via prompt or
update the spec file and point it at the new section.
