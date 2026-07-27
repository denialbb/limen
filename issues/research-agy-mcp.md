# Research Brief: agy + MCP as an integration layer

## Background (the repo, today)

Limen is a correctness-oriented workflow engine that drives coding-agent CLIs as
cognitive workers/validators. The current architecture (ADR 0009) spawns each
agent CLI as a subprocess and uses `limen` binary callbacks over SQLite for IPC.
**No MCP server** — this was an explicit decision that superseded an earlier
design (`[STALE]_handoff_mcp_design.md`, a "Dual MCP Server Architecture").

Read these for full context:

- `docs/adr/0009-multi-driver-dialects.md` — the current decision + the
  revisit trigger recorded in §1.
- `docs/prd/real_agent_worker_validator.md` — locked decisions, esp. #13
  (git-poll breadcrumbs) and #15 (trusted posture / future sandbox).
- `.agents/docs/[STALE]_handoff_mcp_design.md` — the rejected MCP design.
- `internal/remote/{agent,agt,validator}.go` — the dialect seam + agy driver.
- `internal/orchestrator/orchestrator.go` — the state machine + callback model.

## The agent we care about: agy

agy is the Antigravity CLI (`agy --print "<prompt>" --print-timeout 10m`).
Today it's a **one-shot** dialect — prompt in argv, scan stdout to EOF, no
mid-run event stream. So while agy runs, the TUI shows `WorkerStarted` → (black
box) → `WorkerFinished`. There's no status, no progress, no breadcrumbs.

## The question (research THIS independently)

> agy (Antigravity CLI) is an MCP **client** — it loads MCP servers during a
> run. Could driving agy through MCP — i.e. limen exposing MCP tools that agy
> calls mid-task — work **better** as an integration layer, specifically for
> status updates, progress breadcrumbs, and structured tool calls, than the
> current subprocess-callback model?

Investigate:

1. **Mechanical feasibility.** Does `agy --print` actually invoke MCP tools
   *mid-run* (not just at prompt/answer boundaries)? Is there evidence, or is
   it unverified? Check the agy/Antigravity docs and any community projects
   wrapping `agy` over MCP.
2. **Scoping problem.** agy only loads MCP servers from the **HOME-level**
   config (`~/.gemini/...`); project-local config is discovered but ignored
   (GitHub issue #60 antigravity-cli). What does that mean for a limen-owned
   MCP server — can it be scoped to a task/repo, or does it load for every agy
   invocation on the machine? Is that acceptable?
3. **What it buys vs costs** versus the documented alternative (PRD #13:
   periodic `git status --porcelain` polling for agy breadcrumbs). Think
   specifically about: structured vs guessed events; push vs pull cadence;
   coupling tightness; dispatch asymmetry across pi/claude/opencode (which
   already emit native events) vs agy; maintenance cost; and the relationship
   to the deferred sandbox arc (PRD #15).
4. **Does this fire ADR 0009's revisit trigger?** The trigger was *"an agent
   with no shell execution, or a pull-model retrieval contract."* agy has shell
   access and uses it. Is the "status updates" need a *different* need than the
   one the trigger anticipated? Worth distinguishing.
5. **Your recommendation.** Is MCP-as-integration-layer for agy worth building
   now, deferred to a specific future arc, or rejected? Give your honest
   assessment with reasoning — don't try to agree with any prior conclusion in
   the repo; form your own.

## Output

Write your findings to `docs/spikes/agy-mcp-integration.md` as a spike doc
(same style as the other files in `docs/spikes/`). Include: verified facts with
sources, the mechanical-feasibility assessment, the scoping analysis, the
buy/cost trade-off, your read on the ADR 0009 trigger, and a clear
recommendation. Keep it skimmable.

Use web search to verify agy's MCP capabilities and the project-local config
issue. Then form your judgment from the codebase + your research. Don't
implement anything — this is research only. Commit the spike doc when done.
