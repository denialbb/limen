# Limen

Correctness-oriented workflow engine that orchestrates multi-agent software
engineering through a strict Go-owned state machine. This glossary covers the
retrieval subsystem — the "perception" pillar (*retrieval defines perception*).

## Retrieval

**Retrieval**:
Building the bounded, ranked context set a task needs, with confidence metadata,
from the codebase. The perception pillar.
_Avoid_: search, indexing (indexing is a sub-step of retrieval)

**Progressive Retrieval**:
The pipeline pattern where composable Stages successively refine a candidate set,
and the aggregate score distribution yields confidence/coverage. Not a search
pipeline — a confidence-gated context-construction system.
_Avoid_: RAG

**Stage**:
One composable step in the retrieval pipeline that scores or reorders candidate
chunks. BM25 is a Stage; the structural boost is a Stage; future neural rerankers
are Stages.
_Avoid_: layer, filter

**Chunk**:
A bounded unit of retrievable text from the codebase, produced by a `Chunker`.
This arc: a fixed line-window (~50 lines, ~10 overlap). Precise symbol-aligned
chunking (tree-sitter) is a later `Chunker` behind the CGO line.
_Avoid_: document, passage, snippet

**Context Manifest**:
The retrieval output — a bounded, ranked set of chunks with `sources`,
`confidence`, and `coverage_hint`, conforming to `retrieval_contract.md`. It is
the *sole* channel from Retriever to Router. Persisted verbatim in the Task's
`context_snapshot` column (that column name is storage, not domain vocabulary).
_Avoid_: context snapshot, context blob, retrieval result

**Confidence**:
Normalized signal of retrieval certainty. This arc: derived from the BM25 top-k
score-distribution entropy. Drives the Router's proceed-vs-escalate choice.
_Avoid_: score, similarity, relevance

**Coverage Hint**:
Normalized signal of context completeness. This arc: derived from query-term
recall. Drives the Router's expand choice.
_Avoid_: recall, completeness score
