Type: AFK

## Parent PRD

`docs/prd/spike_ndjson_demo.md`

## What to build

Single preparatory commit on `main` fixing two latent bugs the spike will trip over, per PRD §"Preparatory Commit (before spike work)" and §"Preparatory commit scope".

**BUG #3 — Non-transactional canonical writes**: `RecordFinalOutput` and `RecordContextSnapshot` (`internal/state/sqlite.go:321-358`) execute as standalone `UPDATE` statements, not transactionally with the surrounding state transition. A crash between `TransitionState(APPROVED)` (`orchestrator.go:381`) and `RecordFinalOutput` (`:393`) leaves canonical state inconsistent (APPROVED with no final output). Wrap both writes in a transaction with the preceding transition.

**BUG #1 — Shallow `recordToolCall`**: `recordToolCall` (`orchestrator.go:133-137`) stores only opaque labels (`"router.Evaluate"`, `"worker.ProduceSolution"`, `"validator.Evaluate"`); no args, no responses, no retrieved-context snapshot. Extend the function to accept args + response alongside the label and update its callers at `orchestrator.go:294`, `:309`, and any spike-introduced call sites (none yet — this is prep). The spike will add real `file.write` tool calls carrying `{path, content}` args; without this fix the SQLite `tool_calls` table is a lie committed during the spike. See `.agents/docs/determinism_boundary.md:29-32`.

## Acceptance criteria

- [ ] `recordToolCall` signature extended to accept args + response alongside the label
- [ ] All existing callers of `recordToolCall` updated to the new shape
- [ ] `RecordFinalOutput` and `RecordContextSnapshot` wrapped in transactions with the surrounding transition (no standalone `UPDATE`)
- [ ] `internal/state/state_test.go` updated to assert the richer tool-call shape (args+response captured)
- [ ] `internal/state/state_test.go` updated to assert the transactional invariant (no APPROVED-without-final-output window observable)
- [ ] `internal/orchestrator/orchestrator_test.go` updated to assert the richer tool-call shape
- [ ] Existing in-process test mocks (`orchestrator_test.go:28-159`, `integration_test.go:34-95`) and `cli*` placeholders (`main.go:25-138`) remain intact and selectable at the wiring site
- [ ] `go test ./...` passes

## Blocked by

None - can start immediately.

## User stories addressed

- PRD acceptance criterion: "BUG #1 and BUG #3 are fixed and their tests assert the richer / transactional shapes."
- BUG #1, BUG #3

## Verify

```
go test ./...
```