Type: AFK

## Parent PRD

`docs/prd/spike_ndjson_demo.md`

## What to build

The Layer 3 end-to-end integration test, per PRD §"Layer 3: `internal/integration/`" and §"Spike Path". This test is the spike's contract-regression catcher: if the Go-Python contract drifts, this is where it fails.

One end-to-end test in `internal/integration/` launches the real `limen run-task --task-id X --mock --mock-transcript <path>` against a real git repo in `t.TempDir()`. The spike exercises exactly one branch set through `RunTask` (PRD §"Spike Path"):

1. Router decides `proceed` (no expand).
2. Retriever returns empty.
3. Worker produces one `file.write` edit (`solution.txt` with initial buggy content), emits a result event.
4. Validator fails once with off-by-one feedback.
5. Worker retries with the validator's `feedback`, produces a corrected `file.write` edit, emits a result event.
6. Validator passes.
7. No conflict; `CommitWorktree` advances `main`; task finalizes `COMMITTED`.

The test asserts:
- Task reaches `COMMITTED`.
- The worktree diff contains `solution.txt`.
- The canonical branch advanced.
- The event bus saw the expected event sequence (routing proceed, worker tool call, validator fail, worker retry, validator pass, commit).

## Acceptance criteria

- [ ] `internal/integration/` contains an end-to-end test launching real `limen run-task --task-id X --mock --mock-transcript <path>`
- [ ] Test uses `t.TempDir()` with a real git repo
- [ ] Test asserts task reaches `COMMITTED`
- [ ] Test asserts the worktree diff contains `solution.txt`
- [ ] Test asserts the canonical branch advanced
- [ ] Test asserts the event-bus sequence: routing proceed, worker file edit, validator fail, worker retry, validator pass, commit
- [ ] Test fails loudly if any field in the Go-Python contract drifts
- [ ] `go test ./...` passes
- [ ] `pytest` passes

## Blocked by

- Blocked by `issues/002-fix-spike-path-bugs.md` (bug fixes must land before the spike runs end-to-end)
- Blocked by `issues/005-wire-mock-main.md` (need the `--mock`/`--mock-transcript` wiring to launch the binary)

## User stories addressed

- PRD §"Layer 3: `internal/integration/`"
- PRD §"Spike Path"
- PRD acceptance criterion: "`limen run-task --task-id spike-demo --mock --mock-transcript ...` runs end-to-end against a real git repo and the task reaches `COMMITTED`."
- PRD acceptance criterion: "The end-to-end integration test (`internal/integration/`) fails loudly if any field in the Go-Python contract drifts."
- PRD acceptance criterion: "`go test ./...` and `pytest` both pass."

## Verify

```
go test ./internal/integration/...
go test ./...
pytest
```