# Limen Retrieval Contract

This document defines the output interface of the retrieval pipeline. It explicitly abstracts away *how* retrieval is performed (e.g., lexical vs. semantic vs. graph) and focuses purely on *what* must be returned to the orchestration layer.

## Key Invariant

**Retrieval returns a ranked, bounded context set with confidence metadata.**

It must **NOT** return:
- Embeddings or tensor vectors
- Internal index structures
- Reranker traces or intermediate scoring math (including per-chunk BM25 scores)

## Retrieval Output Contract

The retrieval subsystem must conform to the following JSON schema when delivering context:

```json
{
  "query_id": "task-<taskid>:#<expand-iteration>",
  "chunks": [
    {"path": "...", "line_start": int, "line_end": int, "text": "..."}
  ],
  "confidence": float,
  "coverage_hint": float
}
```

### Field Definitions

- **`query_id`**: Identifier linking the retrieval pass to a specific task and expand iteration. Format: `task-<taskid>:#<expand-iteration>`. Encodes both the task and the iteration within the `maxExpandIterations` loop so each manifest is traceable to the pass that produced it.
- **`chunks`**: Ordered set (descending final stage-aggregate score; ties broken by `(path, line_start)`) of the top-k retrieved chunks, k fixed across expand iterations. Per-chunk fields: `path`, `line_start`, `line_end`, `text`. **No per-chunk `score`, no `stage` trace, no `id`** — ranking is communicated only through array order.
- **`confidence`**: A normalized float in [0,1] representing the retrieval subsystem's certainty in the best match's relevance. Defined as a saturating map of `topChunkBM25 / Σ IDF(query terms)` (ADR 0002). Used by the Router to decide `PROCEED` vs `ESCALATE` (after coverage gates).
- **`coverage_hint`**: A normalized float in [0,1] representing query-term recall: the fraction of distinct surviving query terms whose whole-token form appears in some retrieved chunk (ADR 0003). Used by the Router to trigger `EXPAND`; a value of exactly 0 triggers immediate `ESCALATE` (escape hatch).

### Source derivation

The distinct set of source paths is derivable from `chunks[].path`; no separate `sources[]` field is emitted (ADR 0004).

## ADRs

- ADR 0002 — confidence definition (best-match magnitude, not entropy)
- ADR 0003 — coverage_hint definition (whole-token recall with survival exclusion)
- ADR 0004 — manifest shape (this contract; dropped `sources[]`)
- ADR 0005 — Router thresholds (cascade + zero-coverage escape hatch)
- ADR 0008 — acceptance tests pinning each contract field