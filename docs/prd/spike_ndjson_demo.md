# PRD: NDJSON Spike and End-to-End Demo

Status: Approved via grilling session 2026-06-23
Scope: Implement the missing Go-Python boundary via a thin vertical spike that proves the bidirectional NDJSON contract end-to-end with a mock LLM, and fix only the latent bugs that sit on the spike's path.

## Context

The Go core (state machine, SQLite WAL store, git worktree manager, event bus, NDJSON protocol, TUI) is implemented and well-tested. The Python execution layer (the "thin client") is essentially absent: three stub files plus an orphaned `FastMCP` prototype, all written for a *pull* model that the architecture has rejected. The Go-Python contract is broken both ways:

- Python clients invoke `limen worker ...` / `limen validator ...` subcommands that `cmd/limen/main.go` does not implement.
- The NDJSON interactive-mode RPC (the documented v1 tool-call inversion at `.agents/docs/interactive_tui.md:84-93`) has no production wiring; `internal/ndjson/protocol.go` is unit-tested but caller-less.

The binary's happy path is "real but fake": `cliWorker` writes a literal `dummy_solution.txt` (`cmd/limen/main.go:90-93`) that the real `WorktreeManager` commits to `main`. The pipeline shape is correct; the cognition and the boundary that enables it are absent.

## Goal

Prove the bidirectional NDJSON contract end-to-end with a real Python subprocess backend driven by a mock LLM, exercising the happy path plus one validator-retry. Deliver a deterministic, reproducible TUI demo. Lock down only the latent bugs that sit on the spike's path; defer everything else as additive future work.

### Non-goals (deferred)

- Routing expand loop (`maxExpandIterations > 0`).
- Conflict detection and the `parseConflictMarkers` repair path.
- Real LLM integration; the mock LLM transcript is the only cognitive backend.
- Multi-task dashboard, human-in-the-loop controls, `RedisBus` (v2).
- Canonical-branch configurability (remains hardcoded `"main"`).
- `cliGit.IsValid` error swallowing (orthogonal; `cli*` placeholders are throwaway).

## Direction

Build the real Go-Python boundary with a mock LLM backend. Stage it as a thin vertical spike with bugs fixed as tripped. Do not mock in-process inside Go (the Go core is already proven; an in-process mock would polish a demo without moving toward the real thing).

## Spike Path

The spike exercises exactly one branch set through `RunTask`:

1. Router decides `proceed` (no expand).
2. Retriever returns empty.
3. Worker produces one file edit via a `file.write` tool callback, emits a result event.
4. Validator fails once with feedback.
5. Worker retries with the validator's feedback, produces a corrected file edit, emits a result event.
6. Validator passes.
7. No conflict; `CommitWorktree` advances `main`; task finalizes `COMMITTED`.

No expand loop. No conflict. No retry exhaustion.

## Pinned Design Decisions

### Push model, NDJSON-driven

Go owns the orchestration loop (`internal/orchestrator.RunTask`); Python is a passive tool-consuming worker that responds to NDJSON envelopes. This is the architecturally correct model per `.agents/docs/interactive_tui.md:84-93` and matches the existing (caller-less) `internal/ndjson/protocol.go`.

The pull model is rejected wholesale. The existing `worker_server.py` / `validator_server.py` invoke `limen worker ...` / `limen validator ...` subcommands that will never be implemented.

### One subprocess per role invocation, stateless

Each `router.Evaluate` / `worker.ProduceSolution` / `validator.Evaluate` call launches a fresh Python process, which does one cognitive step, returns its result envelope, and exits. No long-lived state across calls. The retry feedback channel works because Go threads the validator's `feedback` string in the second `worker.ProduceSolution` request (`orchestrator.go:295` already does this in-process).

Matches the existing `worker_server.py:7-13` "stateless by design" comment.

### Bidirectional NDJSON only for the worker

The worker needs callbacks to perform file edits. Router and validator are one-way.

- Router: Go pre-stuffs the request with task + retrieved context.
- Validator: Go pre-stuffs the request with task + the worker's captured diff.
- Worker: Go sends the request; the worker emits `file.write` tool-requests during its run and a final result `event` on completion. Go's adapter dispatches each `tool_request` inline and writes the `tool_response` back.

### Envelope shape

Reuse the existing three-kind envelope union (`event` / `tool_request` / `tool_response` at `internal/ndjson/protocol.go`). Add `file.write` to the tool-name constant table (`protocol.go:66-68`). The worker's final step result is an `event` envelope. The final result and tool-requests are distinguishable by `kind`; no new envelope kind is introduced.

### NDJSON adapters as new interface implementations

Write `ndjsonRouter`, `ndjsonWorker`, `ndjsonValidator` in a new `internal/remote` package that satisfy `orchestrator.Router` / `orchestrator.Worker` / `orchestrator.Validator`. The orchestrator code stays byte-for-byte unchanged. The existing in-process test mocks (`orchestrator_test.go:28-159`, `integration_test.go:34-95`) and the `cli*` placeholders (`main.go:25-138`) remain intact and selectable at the wiring site.

### Adapter concurrency: synchronous single-flight

`ProduceSolution` runs a decoder loop inline on the calling goroutine. On `tool_request`: handle inline (write the file via `os.WriteFile` with a path-prefix guard, build the `tool_response`, write to the encoder, loop). On the result `event`: decode it into the worker-result struct, return. On EOF mid-stream or process exit before the result event: return an error.

No pump goroutine, no channels. The worker's interaction is strictly sequential: each `file.write` request must be answered before the worker emits its next envelope.

Router and validator adapters are one-shot reads: a single decoder read for the result event, no loop.

### Subprocess lifecycle: graceful shutdown

Launch the subprocess with `exec.Command`. Watch `ctx.Done()` on a goroutine: on cancellation, send `SIGTERM`, then `SIGKILL` after a configurable grace period (default `5s`). The blocked decoder read returns either an EOF (process died gracefully) or a "read on closed pipe" error; the adapter translates either into `ctx.Err()` and returns.

Grace period is configurable via an adapter constructor parameter (`shutdownTimeout time.Duration`), defaulting to a package-level constant. Different roles can use different timeouts without touching the protocol.

### Request envelopes

```
router:     { task: {id, description, context_snapshot}, attempt: 1 }
worker:     { task: {id, description}, feedback: "", attempt: 1 }
validator:  { task: {id, description}, worktree_diff: "<diff string>", attempt: 1 }
```

- `attempt` is honest production metadata (the worker's retry count, 1-indexed). The mock repurposes it as the transcript array index. Real LLM backends can use it semantically to adjust retry strategy.
- `feedback` is the validator's prior feedback string, empty on the first worker attempt, non-empty on retry. The orchestrator already threads this in-process at `orchestrator.go:295`.
- `worktree_diff` is captured by the `ndjsonValidator` adapter itself via the `GitClient` it receives at construction (the same `GitClient` the orchestrator uses). The capture happens before the request is sent. Confirms `GetWorktreeDiff`'s `git add -N` mechanism (`worktree.go:104-118`) handles untracked files cleanly; no new git-side bug.

### Mock LLM: lives in Python, transcript as a JSON file

The mock LLM is in-process Python, returning scripted responses from a transcript file. Swapping the mock for a real LLM is a Python-internal change that never touches the Go-Python contract.

Transcript shape (`src/limen/mock/transcripts/spike.json`):

```json
{
  "transcript_id": "spike-happy-retry",
  "router": [
    {
      "decision": "proceed",
      "rationale": "Task is well-bounded; no expansion needed.",
      "complexity": "normal"
    }
  ],
  "worker": [
    {
      "tool_calls": [
        { "name": "file.write", "args": { "path": "solution.txt", "content": "initial buggy solution" } }
      ],
      "result": { "status": "complete", "summary": "Wrote solution.txt with an off-by-one bug." }
    },
    {
      "tool_calls": [
        { "name": "file.write", "args": { "path": "solution.txt", "content": "fixed solution" } }
      ],
      "result": { "status": "complete", "summary": "Wrote corrected solution.txt addressing validator feedback." }
    }
  ],
  "validator": [
    {
      "passes": false,
      "feedback": "Solution contains an off-by-one bug in the loop bound. Please fix and resubmit.",
      "criteria": [
        { "name": "compiles", "passes": true, "detail": "OK" },
        { "name": "correctness", "passes": false, "detail": "Off-by-one at line 3." }
      ]
    },
    {
      "passes": true,
      "feedback": "Solution is correct.",
      "criteria": [
        { "name": "compiles", "passes": true, "detail": "OK" },
        { "name": "correctness", "passes": true, "detail": "All checks pass." }
      ]
    }
  ]
}
```

Each role's list is consumed sequentially; the Nth invocation of a role returns the Nth entry. Transcript exhaustion fails loud via an error envelope (not silent repeat, not nonzero exit).

### CLI flags

- `--mock` (boolean, defaults to `true` until the real backend exists): selects the Python command (`python -m limen.mock.<role>` vs the future `python -m limen.<role>`).
- `--mock-transcript path/to/spike.json`: path to the transcript file (defaults to `src/limen/mock/transcripts/spike.json`).

`internal/remote` is backend-agnostic; the mock-vs-real distinction is purely a Python command-line difference selected at the `cmd/limen/main.go` wiring site.

### Python package layout

`src/limen/mock/` contains:

- `runtime.py` - NDJSON stdin/stdout loop, transcript loader, `request_tool(name, args)` callback that emits a `tool_request` and blocks on the matching `tool_response`.
- `router.py` / `worker.py` / `validator.py` - per-role cognitive logic (which transcript key to read, what envelope to emit). Pure functions from `(transcript, request-envelope) -> result-envelope`, testable without subprocesses.
- `__main__.py` per role (three thin entrypoints, `python -m limen.mock.router` etc.), each calling `runtime.run_role("<role>", cognitive_fn)`.
- `transcripts/spike.json` - the spike transcript (shown above).

The worker's bidirectional `file.write` loop lives inside `runtime.run_role`'s loop; the cognitive function emits tool-requests via `runtime.request_tool("file.write", {path, content})`, which blocks until Go responds.

## Preparatory Commit (before spike work)

Single commit on `main` fixing the two bugs the spike will trip over:

### BUG #3: Non-transactional canonical writes

`RecordFinalOutput` and `RecordContextSnapshot` (`internal/state/sqlite.go:321-358`) execute as standalone `UPDATE` statements, not transactionally with the surrounding state transition. A crash between `TransitionState(APPROVED)` (`orchestrator.go:381`) and `RecordFinalOutput` (`:393`) leaves the canonical state inconsistent (APPROVED with no final output). Fix: wrap both writes in a transaction with the preceding transition.

### BUG #1: Shallow `recordToolCall`

`recordToolCall` (`orchestrator.go:133-137`) stores only opaque labels (`"router.Evaluate"`, `"worker.ProduceSolution"`, `"validator.Evaluate"`); no args, no responses, no retrieved-context snapshot. The spike adds real tool calls (`file.write` from the worker, carrying `{path, content}` args); if not captured, the SQLite `tool_calls` table is a lie committed during the spike. Fix: extend the function to accept args + response alongside the label and update its callers. Update existing tests to assert the richer shape.

### Preparatory commit scope

- Extend `recordToolCall` signature; update callers at `orchestrator.go:294`, `:309`, and any spike-introduced call sites.
- Wrap `RecordFinalOutput` / `RecordContextSnapshot` in transactions with the surrounding transition.
- Update tests in `internal/state/state_test.go` and `internal/orchestrator/orchestrator_test.go` to assert the richer tool-call shape and the transactional invariant.

## Cleanup Commit (before spike work)

Delete the rejected pull-model surface and its broken tests:

- `src/limen/mcp_server/worker_server.py`
- `src/limen/mcp_server/validator_server.py`
- `src/limen/router/policy.py` (and `src/limen/router/`)
- `src/limen/mcp_server/limen_mcp.py` (orphaned `FastMCP` prototype; its "Gemma"/"DeepSeek" mapping does not match L1/L2/L3 in `.agents/docs/role_boundaries.md`)
- `tests/test_limen_mcp.py`
- `tests/test_worker_server.py`, `tests/test_validator_server.py`
- `tests/router/test_policy.py` (asserts against `NotImplementedError` stubs; broken)
- `tests/mcp_server/test_worker_server.py`, `tests/mcp_server/test_validator_server.py` (shell out to missing subcommands; broken)
- `src/limen/egg-info/` (stale)
- `config/mpc_config.json` (typo; references the orphan file)

Also addresses BUG #6 (`pyproject.toml:7` declares `dependencies = []` while `limen_mcp.py` imports `mcp` and `requests`) for free by removing the importing file.

## Test Pyramid

### Layer 1: `internal/remote/remote_test.go`

Adapter unit tests with a fake subprocess (canned NDJSON over `io.Reader`/`io.Writer` pairs, or a tiny Go helper binary driven by `os/exec` test helpers). Covers:

- Envelope pump: result event, tool request dispatch, EOF mid-stream, process exit before result.
- `file.write` path-prefix guard: reject `../` escapes (the single most security-relevant assertion; gets its own test).
- Envelope-to-struct translation per role.
- Transcript-exhaustion error surfacing.
- Graceful shutdown: `ctx.Done()` triggers `SIGTERM`, then `SIGKILL` after the grace period.
- Adapter constructor parameter: `shutdownTimeout` honored.

### Layer 2: `tests/mock/`

Python `runtime.py` unit tests (no subprocess). Drive via fake `sys.stdin`/`sys.stdout` pairs:

- NDJSON loop reads result event and returns.
- `request_tool` emits a `tool_request`, blocks on the matching `tool_response`, returns the result.
- Transcript loader parses JSON, sequences per role, fails loud on exhaustion with an error envelope.
- Per-role cognitive functions return the correct envelope shape.

### Layer 3: `internal/integration/`

One end-to-end integration test launching the real `limen run-task --task-id X --mock --mock-transcript <path>` against a real git repo in `t.TempDir()`. Asserts:

- Task reaches `COMMITTED`.
- The worktree diff contains `solution.txt`.
- The canonical branch advanced.
- The event bus saw the expected event sequence (routing proceed, worker tool call, validator fail, worker retry, validator pass, commit).

This test is the spike's contract-regression catcher; if the Go-Python contract drifts, this is where it fails.

## Deferred Bugs (not on the spike's path)

- BUG #4: `parseConflictMarkers` fragility (`worktree.go:222-247`). The spike has no conflict branch.
- BUG #2: `RecordContextSnapshot` overwrite-on-expand. The spike has no expand loop; the retriever runs once at `orchestrator.go:216-229` and the overwrite bug only manifests across multi-expand iterations.
- ISSUE #8: Dead `ErrNotImplemented` (`orchestrator.go:20`). Orthogonal.
- ISSUE #9: `cliGit.IsValid` swallows `git fsck` error (`main.go:165-169`). Orthogonal; the `cli*` placeholders are throwaway.
- ISSUE #10: Hardcoded `"main"` canonical branch (`main.go:286, :363`). Orthogonal; becomes configurable when a real repo uses a different default branch.

## Open Questions

- TODO: should the spike transcript file live at `src/limen/mock/transcripts/spike.json` or a sibling `transcripts/` at the repo root? Current proposal: `src/limen/mock/transcripts/` keeps it co-located with the mock code; swappable at runtime by `--mock-transcript`.
- TODO: should `--mock` default flip to `false` once the real backend exists, or remain `true` until explicitly removed? Current proposal: defaults to `true` until the real backend exists; flipped in the same commit that introduces the real Python entrypoints.

## Acceptance Criteria

- `go test ./...` and `pytest` both pass.
- `limen run-task --task-id spike-demo --mock --mock-transcript src/limen/mock/transcripts/spike.json` runs end-to-end against a real git repo and the task reaches `COMMITTED`.
- The TUI displays the full spike event sequence (router proceed, worker file edit, validator fail, worker retry, validator pass, commit) when the same command is run interactively.
- The end-to-end integration test (`internal/integration/`) fails loudly if any field in the Go-Python contract drifts.
- BUG #1 and BUG #3 are fixed and their tests assert the richer / transactional shapes.
- All deleted Python stubs and broken tests are gone; `pytest` no longer runs any test that asserts against `NotImplementedError` stubs or shells out to missing subcommands.

## References

- `.agents/docs/interactive_tui.md:84-93` - tool-call inversion (the unproven contract this spike validates).
- `.agents/docs/role_boundaries.md` - L1/L2/L3 capability isolation (rationale for one-subprocess-per-role and one entrypoint per role).
- `.agents/docs/git_worktree_contract.md` - No Git Noise, worktree access control (rationale for the `file.write` tool callback over direct Python filesystem access).
- `.agents/docs/determinism_boundary.md:29-32` - tool-call capture contract (rationale for BUG #1 fix).
- `docs/reviews/latest_review.md:76-80` - non-blocking observations that became BUG #1 and BUG #3.
- `internal/ndjson/protocol.go:1-222` - the wire protocol the spike wires into production for the first time.