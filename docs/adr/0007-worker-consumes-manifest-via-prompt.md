# Worker consumes the manifest via the spawn prompt; cliWorker stays manifest-blind

## Status

accepted

## Context

Before this decision the snapshot-to-worker channel was half-built:
`internal/remote/remote.go:228` sends `ContextSnapshot` as a field in the
ndjson worker request JSON, but `internal/remote/pi.go:112-120` builds
`promptText` from `task.ID`, `task.Prompt`, and `feedback` only — the
manifest does not reach the pi worker prompt. The pi backend is the
production default (`cmd/limen/main.go:247`, `worker-backend=pi`), so the
production worker today proceeds on `task.Prompt` alone, blind to
retrieval.

The Router's only output (`PROCEED`) leads to a worker that, without this
decision, never observes the manifest. Perception that changes nothing is
untestable.

## Decision

**Bake the manifest into the worker spawn prompt this arc.**

- Render the manifest's top-k chunks into the prompt as a `## Context`
  section, appended after `task.Prompt` in `pi.go`'s `promptText` and in the
  ndjson worker's request. Per-chunk format:
  ```
  <path>:<line_start>-<line_end>
  ```<lang>
  <text>
  ```
  ```
  detected language from the file extension.
- **Do not expose `confidence`, `coverage_hint`, or `query_id` to the
  worker.** These are Router-internal; surfacing them invites the worker to
  game them.
- **`cliWorker` stays manifest-blind.** The manifest is still persisted on
  `task.ContextSnapshot` (existing column) and reachable for future workers,
  but the stub does not render or parse it — no premature code in code
  slated for replacement.

## Considered Options

- **Defer worker consumption to a later arc; ship the Retriever→Router loop
  alone.** Rejected: leaves the manifest with no observable consumer. Q10's
  acceptance test would be able to assert "the Router made the right
  decision" but not "retrieval changed worker behaviour" — a
  self-referential subsystem whose only output is binary (PROCEED / ESCALATE).
  Perception must be observable at the point of work.
- **Wire the manifest through uniformly, including into `cliWorker`.**
  Rejected: `cliWorker` (`cmd/limen/main.go:69-105`) writes a fixed string
  to a file and has no prompt to inject into. Wiring it would mean parsing /
  reacting to the manifest in a stub slated for replacement — dead code.
- **Expose `confidence` / `coverage_hint` to the worker.** Rejected:
  Router-internal signals. Surfacing them invites the worker to game them
  ("the Router is at 0.51 confidence, hedge your answer") rather than do the
  work the chunks describe.

## Consequences

- `pi.go`'s `promptText` gains a `## Context` section rendered from
  `task.ContextSnapshot`; the snapshot must be parsed from its JSON
  (`chunks[].path/line_start/line_end/text`) and rendered to fenced code
  blocks. Same render path feeds the ndjson worker request.
- The ndjson `context_snapshot` JSON field (`remote.go:56,228`) and the
  pi prompt's `## Context` section diverge in format: ndjson sends raw JSON,
  the pi prompt sends rendered prose. Reconciling them — both prose, or both
  JSON — is a Stage-impl detail; this ADR fixes only that the manifest
  *reaches both* wired workers.
- Worker prompt size grows by the manifest's chunk text (~500 lines at k=10,
  ADR 0004). Already inside the pi provider's typical context budget; no
  truncation logic this arc (a future arc owns worker-context-budget).
- `cliWorker`'s asymmetry ceases to be a bug (the manifest doesn't reach
  it) and becomes an explicit boundary (the stub ignores it, by design).