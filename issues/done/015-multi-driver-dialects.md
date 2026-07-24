# Issue 015: Multi-Driver Agent Dialects (claude, opencode, agy)

Status: Ready for implementation
Plan author: pi (planner session), approved by denial
Implementer: claude instance (herdr pane w4:p1)

## Context

The retrieval arc is complete (TDD slices 1–12, ADRs 0001–0008). The worker
driver seam is proven: `piWorker` (`internal/remote/pi.go`) drives
`pi --mode rpc` to `COMMITTED`, with `decodePiEvent` already extracted as a
pure dialect function (arch-4). The validator is only `cliValidator`
(shell-command gate in `cmd/limen/main.go`).

This arc makes cognition CLI-agnostic per the PRD
(`docs/prd/real_agent_worker_validator.md`, locked decisions #3, #16, #17):
any headless coding-agent CLI pluggable as Worker or Validator, backend
chosen per-role at the wiring site. Orchestrator code stays untouched.

## Locked: CLI calls, not MCP

Decision: **drive agents via CLI subprocess + `limen` binary callbacks. No
MCP server.** Reasoning (supersedes `[STALE]_handoff_mcp_design.md`):

- MCP covers only the agent→limen callback channel. Prompt delivery, process
  lifecycle, and the observability stream remain per-CLI regardless.
- The `limen` binary already is the tool surface: `ready-for-review` /
  `submit-verdict` via the agent's bash tool works with every bash-capable
  agent, with SQLite-mediated IPC already proven.
- MCP never solved model-gating (it is a tool protocol, not a model-access
  protocol); `agy --print` exists, so the original motivation is moot.
- Revisit trigger (document in ADR): an agent with no shell execution, or a
  pull-model retrieval contract (ADR 0007 is push-via-prompt).

## Verified CLI surfaces (verified locally 2026-07-24; re-spike anyway)

| CLI | Headless | Events | Permission bypass |
| --- | --- | --- | --- |
| pi 0.80.2 | `pi --mode rpc` | NDJSON stdio (implemented) | `--no-extensions` |
| claude 2.1.195 | `claude -p --output-format stream-json --input-format stream-json` | stream-json NDJSON in/out; `result` event = end | `--permission-mode bypassPermissions` |
| opencode 1.18.4 | `opencode run --format json "<prompt>"` | JSON events on stdout | `--auto` |
| agy 1.1.5 | `agy --print "<prompt>" --print-timeout 10m` | none (text only) | `--dangerously-skip-permissions` |

Notes:

- All drivers: `cmd.Dir = wt.Path`, prepend limen binary dir to PATH (pattern
  in `pi.go`) so agents can call `limen ready-for-review`.
- claude: do NOT use `--bare` by default — it forces ANTHROPIC_API_KEY auth
  and breaks OAuth/subscription auth. Spike auth before committing to flags.
- opencode: `run` takes the prompt as argv; session resume exists
  (`-s <id> --continue`) but is a non-goal for v1.
- agy: `--print-timeout` is a Go duration (default 5m0s). No event stream →
  no WorkerToolCall breadcrumbs (git-poll breadcrumbs are a future slice).

## Architecture

### 1. Dialect seam + generic worker driver (`internal/remote/`)

Extract what `pi.go` does into a generic `agentWorker` parameterized by a
dialect value (argv builder, stdin-prompt encoder optional, pure line
decoder). Two families share one driver:

- **RPC family** (pi, claude stream-json): prompt written to stdin, process
  stays alive, rich per-line events, explicit end event (`agent_end` /
  `result`).
- **One-shot family** (opencode, agy): prompt in argv, scan stdout until EOF
  (no end event), decode what events exist (opencode: JSON; agy: none).

Key behaviors (all already patterned in `pi.go`): tee stdout+stderr to
`<logDir>/<task>-worker.log`; `context.AfterFunc` closes the stdout pipe on
cancellation (exec kills only the direct child); publish
WorkerStarted/WorkerFinished + decoded events; on end event close stdin so
the CLI exits cleanly.

In-process revision comes for free on all three CLIs: the agent's bash call
to `limen ready-for-review` blocks inside its agent loop, keeping the
process alive across verdict rounds (orchestrator workerLoop drives
validation via the SQLite signaler — unchanged).

Prompt contract: same shape as piWorker (task ID, task prompt, rendered
`## Context` manifest section per ADR 0007, constraints, ready-for-review
instructions). The pi-specific "do NOT use the edit tool" constraint must
NOT leak into other drivers — each dialect owns its constraint block; the
ready-for-review contract is shared.

Keep `piWorker` working byte-for-byte; it may be migrated onto the generic
driver only if tests stay green, otherwise leave it and note the duplication.

### 2. Agent validator (`agentValidator`) — agy first

`Evaluate(ctx, task, tempWt, em)` spawns the validator CLI with
`cmd.Dir = tempWt.Path` and a Level-3 validator prompt: inspect
`git diff`, run the project's tests, then **end the final message with
exactly one sentinel line**:

```
LIMEN_VERDICT: {"passes":true|false,"feedback":"..."}
```

Evaluate captures stdout, parses the LAST sentinel line, emits
ValidatorExamining/ValidatorVerdict bus events, returns (passes, feedback).

**Why a stdout sentinel and not `limen submit-verdict`:** `submit-verdict`
writes the callback verdict directly, which in the synchronous Evaluate flow
races the orchestrator's own bookkeeping (double `RecordValidationDecision`,
premature unblock of the worker's `ready-for-review` poll, possible
"worker exited without submitting for review" escalation). The orchestrator
owns verdict recording; the agent only reports over stdout. `submit-verdict`
stays for the truly-autonomous topology (spike 010).

No sentinel → return an error (transport failure, not a correctness verdict;
must not burn retry budget).

### 3. Wiring (`cmd/limen/main.go`)

Extend `newRuntimeWiring`: `--worker-backend pi|claude|opencode|agy|cli|mock`,
`--validator-backend shell|agy|claude|opencode|mock` (shell = current
cliValidator). Model selection: one `--worker-model` / `--validator-model`
string each dialect maps to its own flag (sensible per-dialect defaults when
empty). Test hook mirroring `newPiWorkerCmd`: inject explicit argv.

## Slices (TDD, one commit each, per repo convention)

0. Branch `feat/multi-driver-dialects`. Spikes (throwaway): confirm each
   CLI's exact wire format and auth (claude stream-json in/out envelope;
   opencode `--format json` event shapes; agy `--print` behavior). Record
   findings as comments in the spike commit or the ADR.
1. Dialect seam + generic `agentWorker` driver, pi parity kept; unit tests
   with fake-CLI scripts (pattern: `newPiWorkerCmd` + `pi_test.go`).
2. claude worker dialect (decode stream-json: assistant text/thinking →
   WorkerAgentMessage, tool_use → WorkerToolCall, result → end).
3. opencode worker dialect.
4. agy worker dialect (thinnest: no events).
5. `agentValidator` + agy dialect, sentinel parser unit tests (last-sentinel
   wins, missing sentinel → error, garbage JSON → error).
6. Wiring flags + integration tests with fake CLIs in
   `internal/integration/`; real-CLI e2e gated behind
   `LIMEN_E2E_REAL_AGENTS=1` env var.
7. Docs: `docs/adr/0009-multi-driver-dialects.md` (incl. the MCP decision +
   revisit trigger), update `.agents/docs/current_architecture.md`, README
   checklist. Move this issue to `issues/done/`.

## Acceptance

- `go test ./...` green; gofmt clean (`.githooks` guards exist).
- Fixture task reaches `COMMITTED` with `--worker-backend claude` (fake CLI
  in CI; real CLI manually verified).
- Fixture task validated by `--validator-backend agy`: verdict reflects a
  real test run in the throwaway worktree; rejection path returns feedback
  and the worker revises.
- Orchestrator package diff: zero bytes (prove the seam).

## Non-goals

Session resume across retries, git-poll breadcrumbs, MCP server, sandboxing
(trusted posture, PRD #15), opencode `serve`/ACP evaluation (future spike).

## Gotchas

- `README.md` is locally modified in the working tree — do not touch or
  revert it.
- `exec.CommandContext` kills only the direct child; keep the
  `AfterFunc(stdout.Close)` pattern.
- agy/claude/opencode edit tools work — the pi edit-tool constraint is
  pi-only.
- If context runs low: stop after a completed slice, committed, with a
  handoff comment in this file.
