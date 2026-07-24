# Multi-driver agent dialects: CLI-agnostic Worker and Validator via a dialect seam

## Status

accepted

## Context

The retrieval arc left the worker driver seam proven but single-tenant:
`piWorker` (`internal/remote/pi.go`) drove `pi --mode rpc` to `COMMITTED`, with
`decodePiEvent` already extracted as a pure dialect function. The only validator
was `cliValidator` (a shell-command gate in `cmd/limen/main.go`). The PRD
(`docs/prd/real_agent_worker_validator.md`, locked decisions #3/#16/#17) calls
for **CLI-agnostic cognition**: any headless coding-agent CLI pluggable as
Worker or Validator, chosen per-role at the wiring site, with the orchestrator
untouched.

Four CLIs were spiked live (`docs/spikes/015-dialect-wire-formats.md`): pi
0.81.1, claude 2.1.195, opencode 1.18.4, agy 1.1.5. They split into two families
by their I/O shape, not by vendor.

## Decision

### 1. Drive agents via CLI subprocess + `limen` binary callbacks — no MCP server

Supersedes `.agents/docs/[STALE]_handoff_mcp_design.md`. The `limen` binary is
already the tool surface: an agent's bash call to `limen ready-for-review` /
`submit-verdict` works with every bash-capable CLI, over the proven
SQLite-mediated IPC. MCP would only cover the agent→limen callback channel;
prompt delivery, process lifecycle, and the observability stream stay per-CLI
regardless. MCP also never solved model-gating (it is a tool protocol, not a
model-access protocol), and `agy --print` reaching the gated model moots the
original motivation.

**Revisit trigger:** an agent with no shell execution, or a pull-model
retrieval contract (ADR 0007 is push-via-prompt). Either would reintroduce the
need for a dedicated callback/tool channel.

### 2. One generic `agentWorker` driver parameterized by a `dialect`

`internal/remote/agent.go` holds the process I/O extracted from `piWorker`
(launch in the worktree with the limen binary on PATH, tee stdout+stderr to
`<logDir>/<task>-worker.log`, `context.AfterFunc(stdout.Close)` on cancellation,
publish WorkerStarted/WorkerFinished around decoded events). A `dialect` value
supplies: the argv builder, an optional stdin-prompt encoder, a pure line
decoder, and a dialect-owned constraint block. Two families share the driver:

- **RPC family** (pi, claude stream-json): `promptViaArgv=false`. Prompt written
  to stdin, process stays alive, rich per-line events, explicit end event
  (`agent_end` / `result`) closes stdin so the CLI exits cleanly.
- **One-shot family** (opencode, agy): `promptViaArgv=true`. Prompt is an argv
  element, stdin closed immediately, stdout scanned to EOF (no end event).

`renderWorkerPrompt` owns the shared prompt contract (task ID, task prompt,
ADR-0007 `## Context` manifest, driver-neutral ready-for-review instruction);
each dialect owns only its constraint block, so pi's "do NOT use the edit tool"
constraint no longer leaks into dialects whose edit tools work. In-process
revision comes free on all CLIs: the blocking `ready-for-review` bash call holds
the agent loop open across verdict rounds; the orchestrator workerLoop drives
validation via the SQLite signaler, unchanged.

Per-dialect decode mapping (from the spike):

| CLI | Launch | Family | End event |
| --- | --- | --- | --- |
| pi | `pi --mode rpc --no-extensions` | RPC (stdin) | `agent_end` |
| claude | `claude -p --output-format stream-json --input-format stream-json --verbose --permission-mode bypassPermissions` | RPC (stdin) | `result` |
| opencode | `opencode run --format json --auto` | one-shot (argv) | EOF |
| agy | `agy --dangerously-skip-permissions --print-timeout 10m --print` | one-shot (argv) | EOF |

claude auth note: no `--bare` (it forces ANTHROPIC_API_KEY and breaks
OAuth/subscription auth). agy is the thinnest dialect — plain text, no events, so
only WorkerStarted/WorkerFinished surface.

The per-dialect permission-bypass flags (`--permission-mode bypassPermissions`,
opencode `--auto`, `--dangerously-skip-permissions`) run the agents unattended
and sit squarely under the trusted-posture decision (PRD #15): your machine,
your repo, your task, no sandbox this arc. They are not a new trust assumption —
just the CLI-specific spelling of the posture already accepted.

### 3. Agent validator reports via a stdout sentinel, not `submit-verdict`

`agentValidator` (`internal/remote/validator.go`) spawns the validator CLI in
the throwaway validator worktree with a Level-3 prompt (inspect `git diff`, run
the tests) and parses the **last** line of the form:

```
LIMEN_VERDICT: {"passes":true|false,"feedback":"..."}
```

**Why the sentinel and not `limen submit-verdict`:** in the synchronous
`Evaluate` flow the orchestrator owns verdict recording. A `submit-verdict`
callback write would race that bookkeeping (double `RecordValidationDecision`,
premature unblock of the worker's `ready-for-review` poll, possible spurious
"worker exited without submitting for review" escalation). The agent only
reports over stdout; the orchestrator records the decision. `submit-verdict`
stays for the truly-autonomous topology (spike 010).

A missing/garbage sentinel is a **transport error** (not a correctness verdict)
and emits no verdict event, so it never burns the retry budget. The sentinel is
format-agnostic, so claude/opencode validators run in their plain-text output
mode (no stream-json) to keep the sentinel a raw stdout line.

### 4. Per-role backend selection at the wiring site

`cmd/limen/main.go` exposes `--worker-backend pi|claude|opencode|agy|cli|mock`
and `--validator-backend shell|agy|claude|opencode|mock` (shell = current
cliValidator). One `--worker-model` / `--validator-model` string each dialect
maps onto its own model flag (sensible per-dialect defaults when empty).
Selection is factored into `selectWorker`/`selectValidator`; **the orchestrator
package is untouched** (the seam's proof).

## Consequences

- `piWorker` is migrated onto the generic driver; `decodePiEvent` is unchanged
  and its tests stay green. The scanner buffer grew to 1 MiB to tolerate
  claude's large `system/init` line.
- New dialects are ~1 file each (argv builder + pure decoder + constructor),
  unit-tested with fake-CLI scripts plus a real-CLI e2e gated behind
  `LIMEN_E2E_REAL_AGENTS=1`. A CI-safe integration test drives a fixture to
  `COMMITTED` with a fake claude worker + fake agy validator, exercising the
  reject→revise→commit loop.
- Resolved (was: claude stdin open item): the spike only exercised
  `--input-format stream-json` against a file-EOF, which mishandled instant EOF.
  The driver instead keeps the stdin pipe open and closes on `result`, matching
  the proven pi path. The gated real-binary suite
  (`LIMEN_E2E_REAL_AGENTS=1 go test ./internal/remote/ -run RealBinary`) passed
  on 2026-07-24 against claude 2.1.195, opencode 1.18.4, and agy 1.1.5 — all four
  cases green, including claude's stdin keep-open path (~28s) and a genuine agy
  validator verdict (~28s). Argv delivery (`claude -p "<prompt>"`) remains the
  documented fallback but was not needed.
- Non-goals (unchanged): session resume across retries, git-poll breadcrumbs,
  MCP server, sandboxing (trusted posture, PRD #15), opencode `serve`/ACP.
