# Go-native progressive-retrieval pipeline; BM25 + structural stage first, neural stages deferred

## Status

accepted

## Context

The retrieval subsystem must produce the `retrieval_contract.md` output
(`chunks`, `sources`, `confidence`, `coverage_hint`) so the Router has real
signals to gate on. The prior design
(`.agents/docs/[STALE]_handoff_retrieval-router.md`) specified a five-layer,
Python-hosted pipeline: BM25 → MiniLM embeddings → bge cross-encoder reranker →
code-graph expansion → router.

The only channel from Retriever to Router is the persisted `context_snapshot`
string on the Task: `Retrieve` returns it, the orchestrator records it, refreshes
the task, and `router.Evaluate(ctx, task, em)` reads it back. So `confidence` and
`coverage_hint` must be encoded into the manifest and parsed back out by the Router.

## Decision

Implement retrieval as a **Go-native pipeline of composable ranking Stages**
behind the existing `orchestrator.Retriever` seam. This arc populates the pipeline
with **lexical (BM25) plus one cheap deterministic structural stage** (identifier /
symbol-definition boost) only.

- `confidence` — see **ADR 0002**. [Original draft of this ADR specified
  score-distribution entropy; superseded by the corrected definition of
  confidence as query-normalized best-match magnitude.]
- `coverage_hint` is derived from **query-term recall** (fraction of distinct task
  terms matched by any retrieved chunk).

Neural stages (embeddings, cross-encoder) and true code-graph expansion are
**deferred to a later arc**, added as new `Stage` implementations with no
orchestrator change.

## Considered Options

- **Neural layers now, via CGO** (onnxruntime / llama.cpp bindings). Rejected:
  `current_architecture.md` records that CGO was deliberately dropped for
  simplicity. Reopening it is a decision of its own, not a detail of this arc.
- **Neural layers now, via a Python sidecar.** Rejected: re-expands the Python
  execution layer that the previous arc deliberately demoted to "adapter of last
  resort," and welds a ~200-LOC pure-Go ranker to a model-serving subprocess with
  a different lifecycle — the maximal-coupling shape, not a minimal clean one.

## Consequences

- Paraphrased/conceptual matches that share no tokens with the target code are not
  retrieved this arc. The pipeline reports that as a correctly-**low**
  `coverage_hint` rather than silently degrading — the Router doing its job.
- The CGO-vs-Python-sidecar question for neural inference is deferred to the
  neural-stage arc and will get its own ADR at that boundary.
