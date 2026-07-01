## Parent PRD

`docs/prd/real_agent_worker_validator.md`

## What to build

Implement the IPC blocking callback round-trip between the worker and orchestrator. Create a SQLite signaling table (`task_signals` / `pending_callbacks`). Add a `limen ready-for-review --summary "..."` command that writes to this table and blocks, polling for a verdict. In the orchestrator (P1), intercept this signal, transition the task to `AWAITING_VALIDATION`, mock a verdict into the DB, and ensure the callback unblocks and returns the verdict to the caller.

## Acceptance criteria

- [ ] `task_signals` or `pending_callbacks` table created in SQLite.
- [ ] `limen ready-for-review` command implemented, writes ready signal and polls for verdict.
- [ ] Orchestrator reads the ready signal, mocks a verdict, and writes it back.
- [ ] The `limen ready-for-review` command returns the mocked verdict.

## Blocked by

None - can start immediately

## User stories addressed

- First Move 2 (Blocking callback round-trip)

## Verify

`go test -run TestBlockingCallbackRoundTrip ./...`
