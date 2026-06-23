Type: AFK

## Parent PRD

`docs/prd/spike_ndjson_demo.md`

## What to build

Write `ndjsonRouter`, `ndjsonWorker`, `ndjsonValidator` in a new `internal/remote` package satisfying `orchestrator.Router` / `orchestrator.Worker` / `orchestrator.Validator`, per PRD §"NDJSON adapters as new interface implementations", §"Adapter concurrency: synchronous single-flight", and §"Subprocess lifecycle: graceful shutdown". The orchestrator code stays byte-for-byte unchanged.

Add `file.write` to the tool-name constant table (`internal/ndjson/protocol.go:66-68`). The worker's final step result is an `event` envelope; final result and tool-requests are distinguishable by `kind`; no new envelope kind is introduced.

**Worker adapter** (`ndjsonWorker`): `ProduceSolution` runs a decoder loop inline on the calling goroutine. On `tool_request`: handle inline (write the file via `os.WriteFile` with a path-prefix guard rejecting `../` escapes, build the `tool_response`, write to the encoder, loop). On the result `event`: decode into the worker-result struct, return. On EOF mid-stream or process exit before the result event: return an error.

**Router and validator adapters**: one-shot reads — a single decoder read for the result event, no loop.

**Subprocess lifecycle**: launch via `exec.Command`. Watch `ctx.Done()` on a goroutine: on cancellation, send `SIGTERM`, then `SIGKILL` after a configurable grace period (default `5s`). Blocked decoder read returns either EOF (graceful) or "read on closed pipe"; adapter translates either into `ctx.Err()`. Grace period configurable via an adapter constructor parameter (`shutdownTimeout time.Duration`); different roles can use different timeouts without touching the protocol.

**Request envelopes** per PRD §"Request envelopes":
```
router:     { task: {id, description, context_snapshot}, attempt: 1 }
worker:     { task: {id, description}, feedback: "", attempt: 1 }
validator:  { task: {id, description}, worktree_diff: "<diff string>", attempt: 1 }
```
`worktree_diff` is captured by the `ndjsonValidator` adapter itself via the `GitClient` it receives at construction (same `GitClient` the orchestrator uses), before the request is sent. Confirms `GetWorktreeDiff`'s `git add -N` mechanism (`worktree.go:104-118`) handles untracked files cleanly.

Layer 1 unit tests in `internal/remote/remote_test.go` per PRD §"Layer 1: `internal/remote/remote_test.go`" cover: envelope pump (result event, tool request dispatch, EOF mid-stream, process exit before result); `file.write` path-prefix guard (reject `../` escapes — its own test); envelope-to-struct translation per role; transcript-exhaustion error surfacing; graceful shutdown (`ctx.Done()` triggers `SIGTERM`, then `SIGKILL` after grace period); adapter constructor parameter `shutdownTimeout` honored. Use a tiny Go helper binary driven by `os/exec` test helpers or canned NDJSON over `io.Reader`/`io.Writer` pairs.

## Acceptance criteria

- [ ] `internal/remote/` package created with `ndjsonRouter`, `ndjsonWorker`, `ndjsonValidator` satisfying the orchestrator interfaces
- [ ] `file.write` added to tool-name constants in `internal/ndjson/protocol.go:66-68`
- [ ] `ProduceSolution` is synchronous single-flight: no pump goroutine, no channels; each `file.write` request answered before the worker emits its next envelope
- [ ] `file.write` path-prefix guard rejects `../` escapes (dedicated test)
- [ ] EOF mid-stream and process-exit-before-result-event both return errors (not silent success)
- [ ] Router and validator adapters are one-shot reads (single decoder read for the result event, no loop)
- [ ] `ndjsonValidator` captures `worktree_diff` from its `GitClient` before sending the request
- [ ] Subprocess lifecycle: `ctx.Done()` triggers `SIGTERM`, then `SIGKILL` after the grace period; adapter returns `ctx.Err()`
- [ ] `shutdownTimeout` is an adapter constructor parameter, defaults to a package-level constant (5s); different roles can use different timeouts
- [ ] Orchestrator code (`internal/orchestrator/orchestrator.go`) stays byte-for-byte unchanged
- [ ] Existing in-process test mocks and `cli*` placeholders remain intact and selectable at the wiring site
- [ ] `internal/remote/remote_test.go` covers all PRD §"Layer 1" cases above
- [ ] `go test ./...` passes

## Blocked by

None - can start immediately.

## User stories addressed

- PRD §"NDJSON adapters as new interface implementations"
- PRD §"Adapter concurrency: synchronous single-flight"
- PRD §"Subprocess lifecycle: graceful shutdown"
- PRD §"Bidirectional NDJSON only for the worker"
- PRD §"Envelope shape"
- PRD §"Request envelopes"
- PRD §"Layer 1: `internal/remote/remote_test.go`"

## Verify

```
go test ./internal/remote/...
go test ./...
```