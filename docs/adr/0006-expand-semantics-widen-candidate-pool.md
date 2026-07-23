# EXPAND semantics: widen the candidate pool; expand-state passed through the Retrieve seam

## Status

accepted

## Context

The orchestrator's EXPAND loop already exists
(`internal/orchestrator/orchestrator.go:192-265`): `DecisionExpand` →
`expandCount++` → re-`Retrieve`. But two coupled questions were open:
*what does the retriever do differently on EXPAND* (the widen behavior), and
*how does `Retrieve` learn which iteration it's on* (the plumbing —
`orchestrator.Retriever.Retrieve(ctx, task, em)` has no iteration param today,
confirmed at `orchestrator.go:204`).

Q7's cascade (ADR 0005) lights EXPAND when
`0 < coverage_hint < coverage_floor` and `expandCount < maxExpandIterations`.
This decision fixes what EXPAND *does* when lit.

## Decision

**Plumbing: extend the `Retrieve` seam with an `ExpandState` param.**

```
Retrieve(ctx, task, expandState, em)  // was: Retrieve(ctx, task, em)
```

`ExpandState` struct: `{Iteration int}` — passed in by the orchestrator
from `expandCount`. On the first pass `Iteration == 0`.

**Widen: EXPAND widens the BM25 candidate pool; the manifest cut stays fixed.**

- First pass: BM25 keeps top-`N` candidates (N ≥ 10, default N = 100) to feed
  the structural stage and the final top-10 cut.
- On EXPAND iteration `i`: keep `N · (1 + α^i)` candidates (α ≈ 0.5),
  half-again more fed to the structural stage each iteration.
- Manifest top-k stays 10 (ADR 0004). EXPAND changes *which* chunks surface
  in the top-10, drawn from a wider pool — not *how many*.

**Structural stage parameters are constant across iterations.** Structural
boost decay on EXPAND is a future tuning option, captured but unused this arc.

## Considered Options

Plumbing:

- **B. Carry expand-state in the manifest `query_id`.** ADR 0004 already
  encodes `#<expand-iteration>` in `query_id`; the retriever could re-read
  `task.context_snapshot` on entry to learn its last iteration. Rejected:
  makes the manifest *self-referential input* — `Retrieve` must parse its own
  previous output to parametrize itself. Breaks the purity of Retrieve as a
  function of its inputs and couples the producer to parsing the very shape
  it produces. The orchestrator already holds `expandCount`, the previous
  manifest, and the previous `query_id`; passing them in is one struct, not
  an indirect Task-read.
- **C. New `expand_count` column on Task.** Rejected: reopens a parallel
  state channel when the manifest is already the chosen Retriever→Router
  channel. Adds a schema migration for a Router-loop concern.
- **D. Stateless Retrieve; widen on every call.** Rejected: erases the
  first-pass vs expand-pass distinction — every Retrieve from the first is a
  widened retrieve. Wrong semantics.

Widen:

- **Remove query-only stopwords on EXPAND.** Rejected: stopword removal
  already happens on pass 1 (locked analyzer). Removing *more* per
  iteration is different Stage configuration per call, not a widen, and
  doesn't help coverage_hint (the surviving term set is unchanged; coverage
  is recall over them, not precision).
- **Increase manifest k on EXPAND (k=10 → 20).** Rejected: violates ADR 0004
  and inflates the worker-prompt budget — exactly what ADR 0004 rejected.
  EXPAND's purpose is candidate widening, not manifest inflation.
- **Rewrite the query via LLM.** Rejected: no LLM in the loop this arc; query
  rewriting is neural-stage / downstream-arc territory.

## Consequences

- `Retriever.Retrieve` seam changes: tests, mocks, and `cliRetriever`
  (`cmd/limen/main.go:~52`) must take the new `ExpandState` param. The
  orchestrator passes `expandCount` straight through as `Iteration`; it does
  not parse the previous manifest to populate `ExpandState`.
- EXPAND can surface *different* chunks in the top-10 than the first pass —
  which is the point: it raises coverage_hint (more query terms matched) and
  shifts confidence (different `topChunkBM25`). Q7's cascade re-evaluates
  the new manifest; the loop converges or hits `maxExpandIterations`.
- An EXPANDed retrieval's wider pool might surface lower-BM25 candidates that
  the structural stage boosts into the top-10 — a desired effect: the
  structural stage's signal is what EXPAND exists to let through.
- α and N are Stage-impl configuration, sharing the Q7 meta-rule (cutpoints
  as config, not constants); α's default (0.5) lives alongside
  `coverage_floor` / `confidence_floor`.