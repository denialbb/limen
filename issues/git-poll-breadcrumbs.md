# Spike: git-poll breadcrumbs for eventless worker dialects (PRD #13)

Status: Ready for implementation
Plan author: pi (planner session), approved by denial
Implementer: claude instance (herdr pane w4:p1), TDD per repo convention
References: `docs/prd/real_agent_worker_validator.md` decision #13,
`docs/spikes/agy-mcp-integration.md` (empirical rejection of MCP for this need),
`internal/remote/agent.go`, `internal/remote/agy.go`, `internal/bus/bus.go`,
`internal/tui/tabs/worker.go`

## Why

The eventless dialects — `agy` (and any future one-shot plain-text CLI) —
surface only `WorkerStarted` → **(black box)** → `WorkerFinished` in the TUI
Worker tab and Timeline. `agy.go` flags breadcrumbs as "a future slice". The
agy-mcp spike (`docs/spikes/agy-mcp-integration.md`) **empirically proved** MCP
cannot deliver this observability: it is a model-gated pull channel (0/5
unprompted breadcrumbs; even prompted, cadence is 2.35–15.32 s, model-chosen).

The documented alternative — PRD #13: lightweight live breadcrumbs from
periodic `git status --porcelain` polling (~1.5 s, gitignore-aware,
delta-only, bounded cost) — is the agent-agnostic fallback the PRD already
chose. It is deterministic, dialect-agnostic, cheap, and closes the black-box
gap for **every** eventless dialect, not just agy. This spike builds it.

## Goal

While an eventless worker CLI runs inside its worktree, periodically poll
`git status --porcelain` and emit a `WorkerBreadcrumb` bus event carrying the
**delta** of changed files since the previous poll — so the TUI Worker tab
shows coarse live activity between `WorkerStarted` and `WorkerFinished`.

Event-rich dialects (pi/claude/opencode already emit `WorkerToolCall` /
`WorkerFileEdit` / `WorkerAgentMessage`) keep their native streams untouched;
git-poll is gated **on** only for dialects that declare themselves eventless,
matching PRD #13's "fallback" framing.

## Hard constraints

- **Breadcrumbs are ephemeral by principle**
  (`docs/.../determinism_boundary.md` §2: fine-grained streams stay
  ephemeral). They surface as bus events only — **never** written to the
  canonical SQLite tables. Do not touch the state/persistence layer.
- **Orchestrator package diff: zero bytes.** The poller lives in
  `internal/remote/` (the worker driver). Do not touch
  `internal/orchestrator/`.
- **Delta-only.** Emit only when the changed-file set actually changes between
  polls (de-dupe noise; a poll that sees the same porcelain output as the last
  emits nothing).
- **Gitignore-aware, bounded cost.** `git status --porcelain
  --untracked-files=normal` (respects gitignore; surfaces untracked non-ignored
  worker-created files). Single short-lived git subprocess per poll, ~1.5 s
  default cadence. The poller stops on worker exit and on context cancellation.
- **Agent-agnostic but gated on eventlessness.** Add an `emitBreadcrumbs`
  switch to the `dialect` struct; **true only for agy** (the eventless
  dialect). pi/claude/opencode stay false → no double-noise, no behavior change
  for them.
- **Injectable porcelain reader for determinism.** The poller must call a
  `func(ctx) (map[string]string, error)` reader seam, not hard-code `exec`.
  Tests supply a scripted fake; the real driver builds the reader from the
  worktree path. (Mirror the repo's proven seam pattern: the dialect
  `decode` is already a pure function; the poller splits the same way — a
  pure diff plus an injectable reader.)
- **Determinism boundary respected.** Breadcrumbs are observability exhaust,
  not a correctness signal; the validator never consumes them and they never
  influence a state transition.

## Design

### Event type (`internal/bus/bus.go`)

```go
// WorkerBreadcrumb surfaces a coarse, delta-only snapshot of the files the
// worker has changed since the previous breadcrumb (PRD #13). It is the
// agent-agnostic observability fallback for eventless dialects (agy): while a
// one-shot CLI runs with no native event stream, limen polls `git status
// --porcelain` in the worktree and emits the changed-file delta. Event-rich
// dialects (pi/claude/opencode) keep their native streams and do not emit
// breadcrumbs. Breadcrumbs are ephemeral bus events only — never persisted to
// the canonical SQLite (determinism boundary §2).
type WorkerBreadcrumb struct {
    TaskID string
    Files  []BreadcrumbFile
    Timestamp time.Time
}
func (*WorkerBreadcrumb) kind() string      { return "WorkerBreadcrumb" }
func (e *WorkerBreadcrumb) Time() time.Time { return e.Timestamp }

// BreadcrumbFile is one entry of a delta-only worktree-status snapshot. Status
// is the two-character `git status --porcelain` XY code (e.g. " M", "??").
type BreadcrumbFile struct {
    Path   string
    Status string
}
```

### Pure delta (`internal/remote/`, new file or in agent-breadcrumb file)

```go
// diffBreadcrumbSets returns the delta between the previous and current
// porcelain snapshots: entries that are newly present or whose XY status code
// changed. Entries that disappeared (file reverted/staged) are intentionally
// NOT emitted as added — the breadcrumb is a "what changed now" signal, not a
// ledger. Equal sets return nil (delta-only/noise suppression, PRD #13).
func diffBreadcrumbSets(prev, curr map[string]string) []BreadcrumbFile
```

Deterministic ordering: sort the result by Path so events are replayable in
test assertions.

### Poller (`internal/remote/`)

```go
// pollBreadcrumbs periodically calls `read` (default: `git status --porcelain`
// in `dir`), computes the delta vs the previous snapshot, and publishes a
// WorkerBreadcrumb for each non-empty delta. It stops when ctx is cancelled.
// `read` is injected for determinism; the real reader is `gitStatusReader(dir)`.
func pollBreadcrumbs(ctx context.Context, interval time.Duration,
    read func(ctx context.Context) (map[string]string, error),
    taskID string, em orchestrator.Emitter)
```

The reader: `gitStatusReader(dir string) func(ctx) (map[string]string, error)`
runs `git -C <dir> status --porcelain --untracked-files=normal`, parses each
line into `{XY -> path}` (strip the leading two chars + space; rest is the
path; porcelain paths are repo-relative). Parse into the map. Surface non-fatal
git errors as a no-op empty snapshot (a transient git hiccup must not crash the
worker run) — log via the existing pattern if a logger is at hand, else swallow.

### Driver wiring (`internal/remote/agent.go`)

- Add `emitBreadcrumbs bool` to the `dialect` struct.
- In `agentWorker.ProduceSolution`, when `w.dialect.emitBreadcrumbs`:
  - `pollCtx, cancel := context.WithCancel(ctx)`
  - start `go pollBreadcrumbs(pollCtx, breadcrumbInterval,
      w.breadcrumbReader, task.ID, em)` after `WorkerStarted` is published
      (breadcrumbs are meaningful only while the worker is running).
  - `w.breadcrumbReader` defaults to `gitStatusReader(wt.Path)`; if a test
    injected one (unexported field, same package), use that instead.
  - call `cancel()` (and let the goroutine exit) after the scan loop ends,
    before `WorkerFinished`.
  - Failure of the poller must never fail `ProduceSolution` — it is
    observability exhaust, fire-and-forget.

### Dialect switch (`internal/remote/agy.go`)

- Set `emitBreadcrumbs: true` on `agyDialect()`.
- Update the `agyDialect` doc comment: the "future slice" is now built;
  reference PRD #13 and this issue.
- pi/claude/opencode dialects stay `emitBreadcrumbs: false` (the zero value);
  confirm their constructors are unchanged.

### TUI (`internal/tui/`)

- `model.go`: route `*bus.WorkerBreadcrumb` to the Worker tab (mirror the
  `WorkerToolCall` / `WorkerFileEdit` cases already there). Update the comment
  block at model.go ~line 409 to mention `WorkerBreadcrumb`.
- `internal/tui/tabs/worker.go`: add a `case *bus.WorkerBreadcrumb` rendering a
  compact line, e.g.:
  `fmt.Sprintf("Activity: %d file(s) changed — %s", len(files), joinedPaths)`
  truncating the path list to keep it one line (cap ~5 paths + "…").
  Breadcrumbs are NOT rendered in the Timeline tab (ephemeral by principle —
  the timeline mirrors the audited SQLite record, which breadcrumbs never
  enter).

## TDD slices (one commit each, red-green-refactor)

0. **bus event type + pure delta.** Add `WorkerBreadcrumb` + `BreadcrumbFile`
   to `internal/bus/bus.go` (with `kind()`/`Time()`) and the `Event` switch
   arms it needs. Add `diffBreadcrumbSets` (pure) to `internal/remote`.
   Unit tests: empty→empty returns nil; add one → [{path,status}]; status
   code change → emitted; equal sets → nil; multiple deltas sorted by Path.
   Red → green.

1. **poller with injected reader.** Add `pollBreadcrumbs` + `gitStatusReader`
   (default reader; real git subprocess — covered by a real-worktree test
   below, not unit-tested in isolation). Test `pollBreadcrumbs` with a
   scripted sequence reader (returns successive `map[string]string`
   snapshots), a recorder emitter, and a tiny interval; cancel the context
   after the sequence and assert the published `WorkerBreadcrumb` deltas match
   the scripted delta sequence exactly (and that it stops on cancel — the
   recorder sees no events after cancel). Red → green.

2. **wire into agentWorker (eventless dialect).** Add `dialect.emitBreadcrumbs`
   - `agentWorker.breadcrumbReader`; start/stop the poller in
   `ProduceSolution` as designed. Test: drive `agyDialect()` via
   `newAgentWorkerCmd` against a fake-CLI script that prints nothing and sleeps
   briefly (agy is eventless), with an **injected scripted reader**; assert the
   recorder saw `WorkerBreadcrumb` events (proving the poller ran and published
   during the run) and that `WorkerFinished` still follows. Negative contract:
   a pi/claude/opencode dialect run (default reader or fake) emits **zero**
   breadcrumbs (proving the gate holds). Red → green.

3. **real-git integration (snowflake, not CI-fast).** Add a real-worktree test
   gated by `LIMEN_E2E_REAL_AGENTS=1` (match the repo's gated-real-run
   pattern) that: provisions a temp worktree, a fake-CLI script that *touches a
   file* after ~400 ms; assert breadcrumb deltas reflect the new file. Keep
   this gated so unit/CI stays fast & deterministic. Red → green.

4. **TUI rendering + routing.** `model.go` routes `*bus.WorkerBreadcrumb` to
   the Worker tab; `tabs/worker.go` renders the compact activity line; assert
   in the existing `tui_test.go`/`component_test.go` style that a breadcrumb
   event renders a line containing "Activity:" and the changed path(s), and
   that it does NOT appear in the Timeline tab. Update the model.go comment
   block. Red → green.

5. **Docs sweep (one commit).** Update `agy.go` doc comment (slice landed);
   add a short "Breadcrumbs" entry to the `interactive_tui.md` event taxonomy
   table; add a one-paragraph PRD #13 "landed" note to
   `docs/adr/0009-*.md` addendum (or a new short addendum) recording that
   git-poll breadcrumbs shipped as the eventless-dialect observability
   fallback, gated on `dialect.emitBreadcrumbs`, ephemeral by principle. No
   README change required, but keep the existing autonomous-harness section
   accurate.

## Gotchas

- **`exec.CommandContext` on `git -C <dir>`**: porcelain paths can contain
  spaces (escaped by porcelain as octal `\NNN`). A robust parser handles the
  quoted-path form; for v1 a simple "strip first two columns + one space,
  take the rest" is fine for fixture repos with clean paths, but document the
  caveat. Tests must use path-safe fixtures.
- **Reader errors are non-fatal.** A git subprocess failure mid-poll must emit
  nothing and never surface as a `ProduceSolution` error.
- **Stop ordering.** Cancel the poller before publishing `WorkerFinished` so a
  race can't emit a stale breadcrumb after the worker is done. Use the
  `context.WithCancel(ctx)` from the parent; on parent cancel the poller dies
  too (correct under cancellation).
- **No extra goroutine leak on clean exit / cancel.** `pollBreadcrumbs` must
  return promptly on ctx done; the `gitStatusReader` must respect ctx (use
  `exec.CommandContext`).
- **Gated real-run, not CI-fast.** Slice 3's real-git test must be gated
  (`LIMEN_E2E_REAL_AGENTS=1`) — match the existing `internal/remote`
  `RealBinary` gating. Do not disable the gate.
- **Env-var-prefixed commands bypass claude's allowlist prefixes.** Run gated
  suites yourself (the planner), or hand claude `env
  LIMEN_E2E_REAL_AGENTS=1` in an allowlisted-exact form — but claude should
  not need it: slices 0–2,4 are unit/integration with injected fakes and run
  under plain `go test ./...`.
- **One writer per repo.** You (claude) write the harness + tests; the planner
  writes the spec and reviews. Never edit `internal/orchestrator/`. Stop and
  ask if a constraint here contradicts ADR 0009 or the PRD — the ADR/PRD are
  authoritative.

## Acceptance

- `go test ./...` green (unit + integration; the gated real-git slice is a
  no-op without `LIMEN_E2E_REAL_AGENTS=1`).
- `diffBreadcrumbSets` pure unit tests green; `pollBreadcrumbs` scripted
  sequence test green.
- agy run (eventless, via injected fake reader) publishes worker breadcrumbs
  mid-run; pi/claude/opencode runs publish **zero** breadcrumbs (gate holds).
- TUI Worker tab renders the breadcrumb activity line; Timeline does NOT.
- `internal/orchestrator/` diff: zero bytes. `internal/state|persistence`: zero
  bytes. Canonical SQLite: zero new columns/tables (breadcrumbs never persist).
- Docs: `agy.go` comment updated; `interactive_tui.md` taxonomy entry; ADR
  0009 addendum PRD-#13-landed note.

## References

- `docs/prd/real_agent_worker_validator.md` decision #13 (git-poll breadcrumbs).
- `docs/spikes/agy-mcp-integration.md` (empirical rejection of MCP; recommends
  git-poll as next).
- `docs/adr/0009-multi-driver-dialects.md` (dialect seam; revisit trigger).
- `internal/remote/agent.go` (generic `agentWorker` driver — wiring site).
- `internal/remote/agy.go` (eventless dialect — `emitBreadcrumbs: true`).
- `internal/bus/bus.go` (event types — add `WorkerBreadcrumb`).
- `internal/tui/tabs/worker.go` + `internal/tui/model.go` (render + route).
- `.agents/docs/determinism_boundary.md` §2 (ephemeral fine-grained streams).
