# Limen Retrieval Contract

This document defines the output interface of the retrieval pipeline. It explicitly abstracts away *how* retrieval is performed (e.g., lexical vs. semantic vs. graph) and focuses purely on *what* must be returned to the orchestration layer.

## Key Invariant

**Retrieval returns a ranked, bounded context set with confidence metadata.**

It must **NOT** return:
- Embeddings or tensor vectors
- Internal index structures
- Reranker traces or intermediate scoring math

## Retrieval Output Contract

The retrieval subsystem must conform to the following JSON schema when delivering context:

```json
{
  "query_id": "...",
  "chunks": [...],
  "sources": [...],
  "confidence": float,
  "coverage_hint": float
}
```

### Field Definitions

- **`query_id`**: A unique identifier linking the retrieval pass to a specific task routing or expansion request.
- **`chunks`**: The actual text blocks, code snippets, or AST nodes retrieved from the codebase.
- **`sources`**: A list of file paths or identifiers indicating where the `chunks` originated.
- **`confidence`**: A normalized float representing the retrieval subsystem's certainty in the relevance of the results (derived internally from metrics like reranker entropy). Used by the Router to decide whether to `proceed` or `escalate`.
- **`coverage_hint`**: A float indicating the estimated completeness of the retrieved context (e.g., percentage of the call graph successfully traversed). Used by the Router to trigger `expand` operations.
