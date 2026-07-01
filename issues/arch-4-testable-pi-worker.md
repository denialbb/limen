# Arch #4 — Make piWorker testable; name Pi's dialect adapter

**Strength:** Worth exploring · **Package:** `internal/remote`

## Problem
`piWorker.ProduceSolution` (`internal/remote/pi.go`) is the DEFAULT worker but has
effectively no test: `pi_test.go` skips unless a real `pi` binary is present and asserts
nothing. `ProduceSolution` hard-codes `exec.CommandContext(ctx, "pi", ...)` (~pi.go:33) —
no injection point — while `remote.NewWorker` (`internal/remote/remote.go`) takes
`cmdArgs []string` and IS testable with a fake script.

The Pi event decoder (`pi.go:~115-193`: `agent_end` / `tool_execution_start` / `turn_end`
traversal over `map[string]interface{}`) is untested.

## Goal
1. Inject the command into `piWorker` (mirror `remote.NewWorker`'s `cmdArgs []string`
   pattern) so tests can point it at a fake script that emits Pi's NDJSON dialect.
2. Extract Pi's event decoding into a named function/adapter (e.g. `decodePiEvent`) that
   takes a raw line/map and returns the bus event(s) — a pure, directly-testable seam.
3. Add a real unit test: feed recorded Pi-dialect lines (agent_end, tool_execution_start,
   turn_end, a thinking/text turn) and assert the emitted bus events.

## Acceptance
- `piWorker` no longer hard-codes `"pi"`; the command is injectable (default remains `pi`).
- Pi event decoding is a testable function separate from process I/O.
- New test exercises the decoder with fixture lines and asserts emitted events (no real `pi` binary required).
- `go build ./...`, `go vet ./...`, existing tests pass.

## Constraints
- Default runtime behavior unchanged (still spawns `pi` with the same args/prompt).
- Match surrounding style. No historical comments.
