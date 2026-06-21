# Limen Retrieval + Routing System — Design Handout

## Overview

This document consolidates the current design decisions for Limen’s retrieval and routing subsystem. The system is built to operate fully locally, without external embedding APIs, and to support correctness-first multi-agent execution with structured escalation.

The core objective is:

> Ensure the router only acts on stable, high-confidence context signals.

---

# Core Pipeline Architecture

Limen uses a **progressive retrieval refinement pipeline** followed by a **decision router**.

```text
Lexical (BM25)
    ↓
Semantic (MiniLM)
    ↓
Reranker (Cross-Encoder)
    ↓
Code Graph Expansion
    ↓
Router Decision Layer
```

Each stage reduces uncertainty and increases contextual precision.

---

# 1. Lexical Layer (BM25)

## Role

Fast recall layer for exact matches and structural tokens.

## Characteristics

- Keyword-based retrieval
- High precision for identifiers
- Strong for logs, function names, errors

## Implementation

- `rank_bm25` (Python)
- Optional: Elasticsearch / Tantivy for scaling

## Strengths

- Extremely fast
- Zero external dependencies
- Excellent for codebases and debugging traces

## Weaknesses

- No semantic understanding
- Misses paraphrased or conceptual matches

---

# 2. Semantic Layer (MiniLM)

## Role

Initial semantic filtering over lexical candidates.

## Model

- `all-MiniLM-L6-v2`

## Characteristics

- Lightweight embedding model
- Local execution (CPU or GPU)
- Filters BM25 candidates by meaning similarity

## Strengths

- Cheap semantic generalization
- Good baseline understanding of intent
- Fully offline

## Weaknesses

- Weak code reasoning capability
- Limited contextual precision

---

# 3. Reranker Layer (Cross-Encoder)

## Role

Primary relevance judgment layer.

## Recommended Models

- `BAAI/bge-reranker-base` (default)
- `BAAI/bge-reranker-large` (higher accuracy, slower)

## Characteristics

- Pairwise scoring: (query, document)
- Produces calibrated relevance scores
- Acts as “context judge”

## Strengths

- Strong semantic + structural understanding
- Excellent for code and debugging contexts
- Produces confidence distributions (critical for routing)

## Weaknesses

- Computationally heavier than embeddings
- Requires batching for efficiency

---

# 4. Code Graph Expansion Layer

## Role

Structural context expansion beyond text similarity.

## Inputs

- function references
- imports
- call graphs
- file dependencies
- AST relationships

## Characteristics

- Deterministic expansion
- Complements statistical retrieval layers
- Critical for debugging and architecture tasks

## Strengths

- Captures non-textual relationships
- Solves multi-file dependency blind spots
- Improves correctness in code-heavy tasks

## Weaknesses

- Requires codebase indexing
- More complex infrastructure

---

# 5. Router Decision Layer

## Role

Final control policy over retrieval sufficiency and task execution flow.

The router does NOT perform retrieval. It consumes signals.

---

## Input Signals

The router receives:

- Lexical confidence (BM25 score distribution)
- Semantic coherence (MiniLM clustering stability)
- Reranker confidence (top-k score spread + entropy)
- Code graph coverage (dependency completeness)

---

## Decision Modes

The router outputs exactly one of:

### 1. `proceed`

Use current context as-is.

Conditions:

- high reranker confidence
- stable score distribution
- sufficient graph coverage

---

### 2. `expand`

Perform additional retrieval.

Triggers:

- low semantic coherence
- ambiguous reranker distribution
- partial code graph coverage

Expansion actions:

- broaden BM25 query window
- rerun semantic filtering
- expand graph traversal radius

---

### 3. `escalate`

Invoke higher-cost reasoning (L3 validator or deeper context synthesis).

Triggers:

- conflicting retrieval clusters
- high entropy in reranker scores
- hard tasks (architecture/debugging/code_writing-hard)

---

# Full System Flow

```text
User Request
    ↓
BM25 Retrieval
    ↓
MiniLM Filtering
    ↓
Reranker Scoring
    ↓
Code Graph Expansion (if needed)
    ↓
Router Decision:
    ├── proceed
    ├── expand
    └── escalate
```

---

# Design Principles

## 1. No API Dependencies

All retrieval and scoring components run locally.

---

## 2. Progressive Refinement

Each stage increases semantic and structural precision.

---

## 3. Router as Control Plane

The router does not “understand content”.
It evaluates **retrieval stability and sufficiency**.

---

## 4. Confidence over similarity

Routing decisions are based on:

- score distributions
- entropy
- coherence metrics

not raw similarity values.

---

## 5. Structural awareness is mandatory

Code graph expansion is a first-class retrieval signal, not optional enhancement.

---

# Key Architectural Insight

This system is not a search pipeline.

It is:

> a confidence-gated context construction system for LLM reasoning

---

# Current Open Questions

- Exact thresholds for reranker entropy → expand vs escalate
- Optimal graph traversal depth for code retrieval
- Integration strategy with L2/L3 validation loops
- Batch sizing strategy for reranker performance
- Router training vs heuristic tuning approach

---

# Status

This design represents the current agreed architecture for Limen’s retrieval and routing system. Implementation should prioritize modularity between:

- retrieval layers (BM25 / semantic / reranker / graph)
- decision layer (router policy engine)
- execution layers (workers + validator)
