# Fatal validator verdicts and retry-exhaustion transitions

Status: Approved for implementation by denial (2026-10-31)
Implementer: idle Herdr agy worker; planner/reviewer: pi

## Goal

Make validator outcomes and retry-exhaustion state transitions agree across the Go contract, CLI adapters, persisted history, callback path, acceptance scenarios, and state-machine docs.

## Locked behavior

- A passing verdict approves as today.
- A retryable rejection (`fatal=false`) with `RetryCount < MaxRetries` follows `AWAITING_VALIDATION → REVISION_REQUESTED → WORKER_RUNNING`; increment `RetryCount` once for the rerun.
- A retryable rejection at the limit follows `AWAITING_VALIDATION → REVISION_REQUESTED → FAILED_ESCALATED`; do not increment `RetryCount` because no rerun starts. `RetryCount` counts actual reruns (default 3 retries means up to 4 worker attempts).
- A fatal rejection (`fatal=true`, `passes=false`) follows `AWAITING_VALIDATION → FAILED_ESCALATED` immediately and does not consume a retry.
- Fatal means a non-retryable policy/invariant violation or an implementation judged fundamentally flawed/unsalvageable, matching `.agents/docs/tool_contracts.md`. Ordinary code/test failures remain retryable.
- `passes=true` with `fatal=true` is an invalid verdict/transport error.
- Apply retry-exhaustion semantics to both the callback-driven real-agent path and synchronous/mock path. Shell and mock validators return `fatal=false` unless a test explicitly scripts a fatal result.

## Contract and persistence

- Replace the validator's ambiguous `(passes, feedback)` result with a typed verdict/outcome carrying `passes`, `fatal`, and `feedback`; update every adapter, caller, and test double.
- Agent validator `LIMEN_VERDICT` sentinels must explicitly include boolean `fatal`; update the shared validator prompt and parser. Reject malformed/inconsistent verdicts rather than silently treating them as success.
- Carry `fatal` through the callback verdict JSON and `bus.ValidatorVerdict`; render it in the Validator view if that view renders verdict status.
- Persist `fatal` with each `validation_decisions` row and in the task's latest-decision JSON. Add an in-place SQLite migration with a `false` default for existing rows; preserve existing history. Update read/write records and tests.
- On a fatal callback verdict, first write the verdict so `ready-for-review` is released, then cancel the per-attempt worker context and wait for `ProduceSolution` to return before finalizing or destroying its worktree. Do not let the expected worker cancellation replace the fatal task outcome. Do not rely on prompt compliance to stop the worker.

## State machine and tests

- Update `.agents/docs/state_machine.md` to draw both fatal and retry-exhaustion paths and state the retry-count rule. Keep the direct `AWAITING_VALIDATION → FAILED_ESCALATED` edge only for the modeled fatal verdict; remove the retry-budget shortcut from that edge.
- Update `.agents/docs/tool_contracts.md` to match the implemented explicit fatal verdict contract.
- Update `features/task_lifecycle.feature` so retry-budget exhaustion explicitly walks through `REVISION_REQUESTED` before `FAILED_ESCALATED`; add a fatal-rejection scenario that escalates directly and proves no retry is consumed.
- Remove the `REVISION_REQUESTED → FAILED_ESCALATED` exemption from `internal/integration/state_agreement_test.go` once a scenario exercises it. Keep no stale exemption for the fatal edge.
- Add/adjust focused tests for both callback and synchronous retry exhaustion, fatal verdict parsing/validation, worker cancellation/join on fatal, bus/TUI representation as applicable, and SQLite migration/persistence. Tests must prove transition order and that no extra retry/worker run occurs on fatal or at exhaustion.
- Preserve validator transport errors as errors, not correctness verdicts. Do not change retry limits, retrieval, unrelated fatal/error handling, or the trusted/no-sandbox posture.

## Workflow and verification

- Work on branch `fix/fatal-verdict-retry-transition`; TDD one vertical slice at a time. Commit the implementation; do not run real provider-backed smoke tests from the worker pane.
- Reviewer independently inspects the diff and runs the relevant Go tests plus the repository QA gates (`bash scripts/qa-check.sh` or `bash scripts/run-gates.sh` as appropriate).
- After code review, the planner runs provider-backed tests only in disposable fixture repos: worker runs for agy, pi/OpenCode Go, OpenCode Go, and OpenCode Zen using configured free defaults with the shell validator; repeat the pi/OpenCode Go worker once with the AGY validator. Stop rather than fall back if a requested free model is unavailable or any selected model is not free. Success requires the fixture's correctness test to pass, task state `COMMITTED`, and clean fixture/worktree/process cleanup. Record actual CLI/model versions, outcomes, and durations; never write credentials to the report.
