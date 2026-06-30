## Parent PRD

`docs/prd/real_agent_worker_validator.md`

## What to build

Wrap the Pi RPC handshake into a real `orchestrator.Worker` backend. Implement the `ProduceSolution` method for the Pi driver: spawn `pi --mode rpc`, pass the initial prompt, and rely on the blocking `ready-for-review` callback to handle the revision loop. Wire up observability (Pi events or git-poll fallback) to stream live breadcrumbs to the orchestrator.

## Acceptance criteria

- [ ] `orchestrator.Worker` interface implemented using the Pi RPC backend.
- [ ] Backend is selectable via `--worker-backend=pi`.
- [ ] Worker spawns `pi`, which calls `ready-for-review` when done.
- [ ] Live observability events are tracked during the worker's execution.

## Blocked by

- Blocked by `issues/008-spike-pi-rpc-handshake.md`
- Blocked by `issues/009-spike-ipc-callback-round-trip.md`

## User stories addressed

- Acceptance Criteria (Pi as worker)

## Verify

`go test -run TestPiWorkerBackend ./...`
