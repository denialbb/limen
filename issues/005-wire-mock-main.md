Type: AFK

## Parent PRD

`docs/prd/spike_ndjson_demo.md`

## What to build

Wire the mock backend into `cmd/limen/main.go` at the wiring site, per PRD §"CLI flags" and §"NDJSON adapters as new interface implementations" (last paragraph). `internal/remote` is backend-agnostic; the mock-vs-real distinction is purely a Python command-line difference selected at the `cmd/limen/main.go` wiring site.

Add two CLI flags:
- `--mock` (boolean, defaults to `true` until the real backend exists): selects the Python command (`python -m limen.mock.<role>` vs the future `python -m limen.<role>`).
- `--mock-transcript path/to/spike.json`: path to the transcript file (defaults to `src/limen/mock/transcripts/spike.json`).

When `--mock` is true, the wiring site constructs `internal/remote` adapters (`ndjsonRouter`, `ndjsonWorker`, `ndjsonValidator`) with the Python command `python -m limen.mock.<role>` and the transcript path passed through. The orchestrator code stays byte-for-byte unchanged. The existing `cli*` placeholders remain intact and selectable (e.g. when `--mock` is absent or via a future flag).

## Acceptance criteria

- [ ] `--mock` boolean flag added, defaults to `true`
- [ ] `--mock-transcript` string flag added, defaults to `src/limen/mock/transcripts/spike.json`
- [ ] When `--mock` is true, `cmd/limen/main.go` constructs `ndjsonRouter`/`ndjsonWorker`/`ndjsonValidator` from `internal/remote` with `python -m limen.mock.<role>` and the transcript path
- [ ] Orchestrator code (`internal/orchestrator/orchestrator.go`) stays byte-for-byte unchanged
- [ ] Existing `cli*` placeholders and in-process test mocks remain intact and selectable at the wiring site
- [ ] `go build ./...` succeeds
- [ ] `limen --help` shows the new flags
- [ ] `go test ./...` passes

## Blocked by

- Blocked by `issues/003-python-mock-runtime.md` (need the mock entrypoints to launch)
- Blocked by `issues/004-ndjson-adapters-package.md` (need the adapter constructors to wire)

## User stories addressed

- PRD §"CLI flags"
- PRD §"NDJSON adapters as new interface implementations" (wiring point)

## Verify

```
go build ./...
./limen --help
go test ./...
```