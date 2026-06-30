## Parent PRD

`docs/prd/real_agent_worker_validator.md`

## What to build

Implement the validator orchestration logic. Upon receiving a `ready-for-review` signal from the worker, the orchestrator must capture the worker's diff, provision a throwaway worktree (reusing `provisionTempWorktree`), and apply the diff. Then, spawn an autonomous CLI validator in that throwaway worktree to run tests and submit its verdict via `limen submit-verdict`. Test debris must not pollute the committed diff.

## Acceptance criteria

- [ ] Throwaway worktree is created from the canonical base + worker's diff.
- [ ] Validator CLI is spawned inside the throwaway worktree.
- [ ] Validator evaluates the code and uses `submit-verdict`.
- [ ] Original worker's directory is completely unaffected by validator's test debris.

## Blocked by

- Blocked by `issues/010-spike-cli-validator-seam.md`

## User stories addressed

- Acceptance Criteria (Validator ran the test for real in a separate worktree)

## Verify

`go test -run TestValidatorThrowawayWorktree ./...`
