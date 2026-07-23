# Retrieval manifest shape: ordered top-k chunks, no intermediate scoring

## Status

accepted

## Context

The manifest is the verbatim content of the Task's `context_snapshot` column
(`internal/state/sqlite.go:74,431,508`) and the sole channel from Retriever to
Router (`internal/orchestrator/orchestrator.go:~204`: `Retrieve` returns it,
`TransitionAndRecordContextSnapshot` writes it, refreshed Task is read by
`router.Evaluate`). The contract
(`.agents/docs/retrieval_contract.md`) forbids returning "reranker traces or
intermediate scoring math"; a per-chunk BM25 score is exactly that.

This decision fixes the *written schema*. Cutpoints on `confidence` ×
`coverage_hint` are the router-thresholds decision; EXPAND's widening behavior
is the EXPAND-semantics decision.

## Decision

```json
{
  "query_id": "task-<taskid>:#<expand-iteration>",
  "chunks":   [ {"path": "...", "line_start": int, "line_end": int, "text": "..."}, ... ],
  "confidence":    0.0..1.0,
  "coverage_hint": 0.0..1.0
}
```

- **Per-chunk fields:** `path`, `line_start`, `line_end`, `text`. **No
  `score`, no `stage` trace, no `id`.** Ranking is communicated only through
  array order.
- **Ordering:** descending final stage-aggregate score; ties broken by
  `(path, line_start)` for determinism.
- **top-k = 10**, fixed across all EXPAND iterations. EXPAND widens the
  *candidate search*, not the manifest cut — k is not the expand knob and is
  set once. 10 × ~50-line window ≈ ~500 lines of worker-prompt budget.
- **`sources[]` is dropped from the contract.** The Router derives the
  distinct set of chunk paths from `chunks[].path`; a redundant top-level
  `sources[]` costs bytes and risks drift (a path listed with no chunk
  referencing it).
- **Dedup:** exact duplicates only (same `path` + same `[line_start,
  line_end]`). The chunker's ~10-line overlap deliberately produces
  partial-overlap chunks with distinct content — keep them.
  Similarity-based merging is neural-stage territory, deferred.
- **`query_id`:** `task-<taskid>:#<expand-iteration>`. Encodes both the task
  and the expand iteration in the `maxExpandIterations = 5` loop, so each
  manifest is traceable to the pass that produced it. Embedding the iteration
  counter here (rather than a separate field) keeps the contract schema
  unchanged to four fields.

## Considered Options

- **Per-chunk `score` field.** Rejected: the contract forbids intermediate
  scoring math, and the Router consumes only `confidence`/`coverage_hint`
  (which aggregate the ranking); a per-chunk score has no consumer.
- **Redundant top-level `sources[]`.** Rejected: derivable from
  `chunks[].path`; redundant top-level structures risk drift and cost bytes
  on a column already persisted per Task.
- **Variable top-k across EXPAND iterations.** Rejected: that makes k the
  expand knob, which is Q8's concern — and EXPAND's purpose is *candidate
  widening*, not manifest inflation. A fixed k keeps the worker-prompt budget
  constant across iterations, isolating the effect of EXPAND to *which*
  chunks surface, not *how many*.

## Consequences

- Contract schema narrows from five fields to four (`query_id`, `chunks`,
  `confidence`, `coverage_hint`). `sources` removed from
  `.agents/docs/retrieval_contract.md` as cleanup.
- The TUI's `ContextBuilt` event (`internal/bus/bus.go:217-222`) today carries
  `ManifestRef string` / `SnapshotSize int`. `ManifestRef` is now redundant with
  `query_id` and `SnapshotSize` is derivable from `len(manifestJSON)`. Both are
  *event*-side conveniences and are not part of the contract; keep or drop at
  Stage-impl time — not a contract change.
- `bus.RouterExamining.Entropy` (`internal/bus/bus.go:231`) is now dead code;
  the Router no longer derives entropy. Cleanup, tracked, not blocking.