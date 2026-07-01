## Parent PRD

`docs/prd/real_agent_worker_validator.md`

## What to build

Implement the CLI validator seam to prove the orchestrator admits a model-gated CLI. Build the `limen submit-verdict --passes <bool> --feedback <str>` command, which records the verdict in the `validation_decisions` and signaling table. Verify that an autonomous dummy CLI can run this command to unblock a waiting `ready-for-review` callback.

## Acceptance criteria

- [ ] `limen submit-verdict` command implemented and writes to canonical `validation_decisions` and the signaling table.
- [ ] A dummy validator CLI can run `submit-verdict` to unblock a worker's `ready-for-review` poll.

## Blocked by

- Blocked by `issues/009-spike-ipc-callback-round-trip.md`

## User stories addressed

- First Move 3 (`limen submit-verdict` from an autonomous CLI)

## Verify

`go test -run TestSubmitVerdict ./...`
