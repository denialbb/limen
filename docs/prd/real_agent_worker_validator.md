# PRD: Real-Agent Worker + Validator (CLI-Agnostic Cognition)

Status: Drafted via grilling session 2026-06-30
Scope: Replace the mock-transcript backend with real CLI-driven cognition for the Worker and Validator roles, behind a driver-agnostic seam. Ship a working worker→validator→commit loop using Pi as the default agent, with MCP proven-but-deferred as the fallback for model-gated CLIs.

## Context

The NDJSON spike (`docs/prd/spike_ndjson_demo.md`, issues 001–006) proved the Go↔Python bidirectional contract end-to-end with a mock LLM. The system now runs `CREATED → … → COMMITTED` against a canned transcript and an empty retriever. The contract works; the cognition is fake.

This PRD takes the first step into **real cognition**: a real coding agent produces the solution, a real agent validates it by running tests, and the loop commits only on a genuine pass.

## Product Thesis (why this shape)

Limen is **role-agnostic, CLI-agnostic cognition**: any CLI agent should be pluggable as Worker or Validator. The constraint that shapes the architecture: some CLIs (e.g. `agy` / Google Gemini) **gate their models** — the only way to reach that model is through that CLI's surface. MCP is the lowest-common-denominator tool protocol those gated CLIs reliably expose. Therefore MCP is not deleted; it is the **adapter of last resort**, not the default.

## Goal

Run a real worker→validator loop, driven by Pi, that reaches `COMMITTED` on a fixture task whose correctness is anchored to a runnable test. Prove the driver seam admits a model-gated CLI without rework. Keep the orchestrator code byte-for-byte unchanged.

### Non-goals (deferred)

- Real Router (stays always-`proceed` stub — meaningful only once retrieval produces confidence/coverage signals; deferred to the retrieval arc).
- Real retrieval pipeline (BM25 + semantic). `cliRetriever` stays empty.
- Routing expand loop, conflict detection/repair (unchanged from spike non-goals).
- Full MCP adapter (designed + proven via one handshake; not built this slice).
- Sandboxing (trusted posture this slice; see Safety).
- The `events(task_id, seq, type, json, ts)` persistence table (fine-grained streams stay ephemeral by principle).
- Multi-task dashboard, HITL controls, RedisBus (v2).

## Locked Decisions

| # | Decision | Choice |
|---|---|---|
| 1 | Near-term horizon | Reach real cognition before closing infra gaps. |
| 2 | First real roles | Worker + Validator together, as parallel vertical slices, then integrate. |
| 3 | Driver — how Limen makes a CLI do a step | **Pi RPC** (`pi --mode rpc`, JSONL over stdio) is the default driver; **spawn-and-callback** is the fallback. Both sit behind the existing `orchestrator.Worker` / `Validator` interfaces; backend chosen per-role at the wiring site (`--worker-backend`, `--validator-backend`). |
| 4 | Worker model | Long-lived single process. Native filesystem access in the worktree (no `file.write` interception). Result inferred from `git diff`. |
| 5 | Completion + revision contract | Explicit **blocking** `limen ready-for-review --summary "..."` callback, driver-neutral. Returns the verdict to the worker: approved → finish; rejected → returns feedback, worker revises in-process and calls again. Retry budget still gates. |
| 6 | Pi RPC's role | Demoted to: deliver initial prompt, keep process alive across the blocking callback, stream events for observability. It does **not** drive the revision conversation. |
| 7 | Validator capability | **Level 3**: read the artifact + **run tests** (read-only execution = the hardest correctness signal). Never sees the worker's reasoning/conversation (epistemic isolation holds). |
| 8 | Validator workspace | **Own throwaway worktree** built from canonical base + the worker's diff applied (reuses `provisionTempWorktree` + `git apply`, the `CheckForConflicts` pattern). Test debris never pollutes the committed diff. Validator reports via `limen submit-verdict` (non-blocking, then exits). |
| 9 | Task delivery | Baked into the spawn prompt + cwd = worktree. No `get_task` pull. |
| 10 | Task/identity binding | Bound at the trusted spawn boundary (Limen-controlled), never via agent-supplied IDs. Agents physically cannot address another task. |
| 11 | Orchestrator wait model | Blocking, SQLite-mediated. No in-process channels across the process tree. |
| 12 | IPC substrate | **SQLite is the IPC bus** between the orchestrator (P1) and the callback subprocesses (P3). A dedicated signaling table (`task_signals` / `pending_callbacks`) carries the ready/verdict handshake; canonical state-machine tables stay the audited record. Poll interval ~200–500ms. WAL handles concurrent cross-process readers/writers. |
| 13 | Worker observability | Coarse state + final diff at the review boundary, **plus** lightweight live breadcrumbs from periodic `git status --porcelain` polling (~1.5s, gitignore-aware, bounded cost, delta-only). Pi's native `tool_execution`/`message_update` events used when available; git-poll is the agent-agnostic fallback. |
| 14 | Persistence | Fine-grained streams ephemeral **by principle** (determinism boundary: generation exhaust is transient). Canonical outcomes persist as today. `events` table deferred until replay pain is real. |
| 15 | Safety | **Trusted, no sandbox** this slice (your machine, your repo, your task). Documented as a known gap; future sandbox = a Docker instance encapsulating the whole Limen process. |
| 16 | Worker agent | **Pi** (default), via RPC. OpenCode held as the agent-agnostic proof. |
| 17 | Validator agent | Autonomous CLI (`agy`/Gemini target) reporting via `limen submit-verdict`; works for any bash-capable CLI. |
| 18 | Build order | Pi fully now; MCP designed + proven via one handshake, deferred otherwise. |

## Process Topology (Pi RPC mode)

```
P1: limen run-task  (orchestrator, parent)
     ├─ spawns ─► P2: pi --mode rpc  (worker; cwd = worktree)
     │                 └─ bash tool runs ─► P3: limen ready-for-review  (blocking callback)
     └─ spawns ─► PV: validator CLI  (cwd = throwaway worktree)
                       └─ bash tool runs ─► limen submit-verdict
```

P1, P3, PV share no memory — only SQLite + filesystem. All cross-process signaling is SQLite-mediated:

1. Worker finishes → P3 (`ready-for-review`) writes "ready + summary" to the signaling table, transitions task to `AWAITING_VALIDATION`, then polls for a verdict.
2. P1 sees the ready signal, captures the diff, provisions the validator's throwaway worktree, applies the diff, spawns the validator (PV).
3. PV runs tests, calls `limen submit-verdict --passes … --feedback …`, exits.
4. P1 records the verdict (canonical `validation_decisions`), writes the verdict to the signaling table.
5. P3's poll returns the verdict to the worker. Approved → worker finishes, P1 commits, task `COMMITTED`. Rejected → worker revises (same process, full context) and calls `ready-for-review` again, bounded by the retry budget.

## Driver Seam

The existing `orchestrator.Worker.ProduceSolution(ctx, task, wt, feedback, em) error` and `orchestrator.Validator.Evaluate(ctx, task, wt, em) (bool, string, error)` are synchronous-blocking from the orchestrator's view. Two implementation families satisfy them:

- **Pi driver** (this slice): drives `pi --mode rpc`; the blocking `ready-for-review` callback owns the revision loop; Pi events feed observability.
- **MCP / spawn-and-callback driver** (proven, deferred): spawns a model-gated CLI, serves the callback (CLI-via-bash or MCP tool), blocks until the verdict lands.

Orchestrator code is untouched. Backend selected per-role at `cmd/limen/main.go` via flags.

## Acceptance Criteria

- A tiny fixture repo with a runnable test (e.g. "implement `add(a,b)` so `test_add.py` passes").
- `limen run-task` against the fixture, **Pi as worker**, reaches `COMMITTED`.
- The committed diff makes the test pass — the validator ran the test for real (Level 3).
- The event stream shows the real sequence: worker ready-for-review → validator ran tests → verdict → commit.
- **Rejection-path test** (separate): a strict criterion or a deliberately-botched task confirms the blocking callback returns feedback and the worker revises — proving the loop *handles* rejection even though an LLM can't be forced to fail on demand.
- `go test ./...` and `pytest` pass.

## First Moves (throwaway de-risking spikes, in order)

1. **Pi RPC handshake** — spawn `pi --mode rpc` in a temp dir, send one prompt, confirm Limen reads `agent_end` and the event stream. Riskiest unknown first.
2. **Blocking callback round-trip** — worker calls `limen ready-for-review`; P1 relays a canned verdict via the SQLite signaling table; the callback returns it. Proves the IPC.
3. **`limen submit-verdict` from an autonomous CLI** — the tiny proof that the seam admits a model-gated CLI (the deferred-but-proven MCP/bash handshake).

Then build the real Pi Worker + Validator against the fixture.

## Doc Tasks

- Reword the `determinism_boundary.md` / `interactive_tui.md` ephemeral-events exception: streams stay ephemeral **by principle** now, not because L1/L2/L3 are placeholders.
- Add the Safety/sandbox known-gap note (trusted posture; future Docker encapsulation).
- Document the driver seam + process topology in `.agents/docs/` (new doc or extend `current_architecture.md`).

## References

- `docs/prd/spike_ndjson_demo.md` — the proven contract this builds on.
- `.agents/docs/role_boundaries.md` — epistemic isolation (validator never sees worker reasoning).
- `.agents/docs/determinism_boundary.md` — what persists vs. transient exhaust.
- `.agents/docs/git_worktree_contract.md` — No Git Noise (drives the throwaway-validator-worktree decision).
- Pi RPC mode: https://pi.dev/docs/latest/rpc — JSONL over stdio, `agent_end`, `tool_execution_*` events, cwd inherits from spawn.
- "What if you don't need MCP?": https://mariozechner.at/posts/2025-11-02-what-if-you-dont-need-mcp/ — the CLI-as-tool rationale behind `limen` callbacks over an MCP server.
</content>
</invoke>
