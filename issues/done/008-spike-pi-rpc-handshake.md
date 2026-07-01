## Parent PRD

`docs/prd/real_agent_worker_validator.md`

## What to build

Implement a throwaway de-risking spike for the Pi RPC handshake. Spawn `pi --mode rpc` in a temporary directory, send a single hardcoded prompt via stdin (JSONL format), and confirm that Limen correctly reads the `agent_end` message and any stream events (`tool_execution_*`, `message_update`) from stdout.

## Acceptance criteria

- [ ] A test or minimal CLI command spawns `pi --mode rpc`.
- [ ] A JSONL prompt is sent to `pi`'s stdin.
- [ ] The process successfully reads the JSONL output from `pi`.
- [ ] `agent_end` message is correctly intercepted.

## Blocked by

None - can start immediately

## User stories addressed

- First Move 1 (Pi RPC handshake)

## Verify

`go test -run TestPiRPCHandshake ./...`
