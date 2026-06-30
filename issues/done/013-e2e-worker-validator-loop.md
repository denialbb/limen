## Parent PRD

`docs/prd/real_agent_worker_validator.md`

## What to build

Wire up the complete end-to-end loop against a tiny fixture repository (e.g., implementing an `add(a,b)` function to make `test_add.py` pass). Ensure that if the validator rejects the solution (simulated or real failure), the `ready-for-review` callback unblocks with feedback, and the Pi worker revises its solution. Prove that a successful validation leads to a committed diff and task state `COMMITTED`.

## Acceptance criteria

- [ ] `limen run-task` completes the end-to-end worker-validator loop on a tiny fixture.
- [ ] Loop succeeds and reaches `COMMITTED` when tests pass.
- [ ] Loop correctly handles a rejection (returns feedback to worker, worker revises).
- [ ] The committed diff passes the fixture's test suite.

## Blocked by

- Blocked by `issues/011-integration-real-pi-worker.md`
- Blocked by `issues/012-integration-validator-throwaway-worktree.md`

## User stories addressed

- Acceptance Criteria (End-to-end loop against a fixture)
- Acceptance Criteria (Rejection-path test)

## Verify

`go test -run TestEndToEndWorkerValidatorLoop ./...`
