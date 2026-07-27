# Empirical Spike: does `agy --print` invoke a limen MCP tool mid-run, and how reliably?

Status: spike (throwaway, NOT wired into the orchestrator)
Requester: pi planner (w3:p1)
Implementer: claude instance (herdr pane w4:p1), TDD per repo convention
References: `docs/adr/0009-multi-driver-dialects.md` (addendum), `docs/spikes/agy-mcp-integration.md`

## Why

`docs/spikes/agy-mcp-integration.md` rejects MCP as an integration layer for
agy **status breadcrumbs** on four design-principle grounds, the load-bearing
one being that MCP tool calls are **pull, model-gated** — the model must
*voluntarily* call a `report_progress` tool, so limen cannot guarantee a
breadcrumb every N seconds. That rejection leans on an **inference**: "models
don't reliably self-narrate progress via tool calls." This spike **proves the
inference empirically** rather than asserting it. (Design principle: Prove the
Pain; Replayability.)

## Goal

Empirically determine, against the installed `agy` (1.1.5 lineage):

> When a limen-owned MCP server exposes a `limen_breadcrumb(message)` tool and
> `agy --print` runs a coding prompt with that server loaded, **does the model
> call the tool during the print-mode agentic loop, and how often/reliably
> across trials?**

Two prompt conditions, both informative:

- **Unaligned**: a normal coding prompt where the tool is *available but never
  requested*. Tests whether the model self-narrates unprompted (the pessimistic
  expectation).
- **Aligned**: a prompt that *explicitly asks* the model to report progress via
  the tool. Bounds the best case — the prompt-discipline case the design
  principle says not to rely on.

## Hard constraints

- **Do NOT write to the user's global `~/.gemini/config/mcp_config.json`.** agy
  loads MCP config from HOME only. Scope the spike by running agy with
  `HOME=<tmpdir>` (and `XDG_CONFIG_HOME` / `GEMINI_HOME` if agy uses them —
  probe; the spike owns creating the tmp home + its `mcp_config.json`). Add a
  unit test that asserts **no mutation of the real `~/.gemini`**. This is
  non-negotiable: the scoping problem (ADR 0009 addendum) is mechanism-verified.
- **Stateless adapter (`design_principles.md`: No Hidden State in MCP).** The
  MCP server holds no task state; `limen_breadcrumb(message)` only **appends**
  `<iso8601-ts>\t<message>` to a log file the spike owns. No task registry, no
  caching, no routing.
- **Throwaway.** Nothing is wired into `cmd/limen`, `internal/remote`, or the
  orchestrator. The harness lives under an isolated, clearly-spike directory
  (e.g. `scripts/spike-agy-mcp/`). Do not touch the orchestrator package.
- **Gated real run.** The live `agy --print` invocation runs only when
  `LIMEN_SPIKE_REAL_AGY=1` is set (match the repo's `LIMEN_E2E_REAL_AGENTS=1`
  pattern). Without the gate, only the fake-CLI unit/integration tests run (CI-
  safe).

## TDD slices (one commit each, red-green-refactor)

0. **Scaffold + breadcrumb recorder.** Minimal MCP server (Python or Go —
   thinnest wins) exposing one tool `limen_breadcrumb(message)` that appends a
   timestamped line to a log path. Unit test: call the tool, assert the log
   line (red → green). Stateless-adapter property test: two unrelated calls
   are independent.
1. **Fake-CLI harness.** A fake-CLI script that connects to the server, calls
   `limen_breadcrumb` N times mid-"run", then exits. Integration test asserts
   the recorder captured exactly N mid-run lines in order (red → green). This
   proves the plumbing so the real run is the only unknown.
2. **HOME-scoped config + no-pollution test.** Test that the spike builds agy's
   MCP config under a tmp `HOME` and that the real `~/.gemini` is untouched
   (red → green). Abort and ask the planner if agy ignores `HOME`-override and
   the spike cannot scope safely.
3. **Real gated run + verdict capture.** With `LIMEN_SPIKE_REAL_AGY=1`, run
   ≥5 trials per condition (unaligned, aligned), count tool calls per trial,
   record cadence. Append an `## Empirical run` section to
   `docs/spikes/agy-mcp-integration.md` with the raw numbers and a one-line
   verdict (confirm/refute the model-gated-pull inference).

## Gotchas

- Env-var-prefixed commands bypass the allowlist prefixes (`LIMEN_SPIKE_REAL_AGY=1
  agy ...` does not match `Bash(agy:*)`). Either allowlist the exact prefixed
  form in `.claude/settings.local.json` or hand the gated command to the
  planner (me) to run — **do not** disable the gate.
- agy's `--print` (`-p`) timeout: keep the prompt tiny (e.g. write a one-file
  fixture) so each trial is seconds, not minutes. `--print-timeout 3m` cap.
- Only stable trial inputs are useful — fixture prompt + repo must be
  deterministic across trials so call-count variance is attributable to model
  behavior, not setup.
- One writer per repo: you write the harness + spike doc; the planner does the
  ADR addendum and reviews. Stop and ask if a constraint here contradicts the
  addendum — the addendum is authoritative.

## Acceptance

- `go test`/`pytest` of the harness green without the gate.
- Real run verdict (call-count + cadence, both conditions) appended to
  `docs/spikes/agy-mcp-integration.md`.
- Zero mutation of the user's real `~/.gemini` (tested).
- Orchestrator package diff: zero bytes (it's a spike; don't touch it).
