# Limen: Go Core / Python Glue Architecture Plan

This document details the architectural boundary and interaction model between the Go core implementation and the Python "glue" layer for the Limen project, resolving the open design decisions for the routing, execution, and validation subsystems.

## 1. System Boundary: Go vs. Python
- **Go Core Engine**: Handles all heavy orchestration. This includes task scheduling, enforcing routing decisions, managing state transitions across the multi-agent graph, controlling retry loops, fanning out/in workers, and ensuring deterministic execution.
- **Python Glue**: Acts as the interface layer. It defines the Model Context Protocol (MCP) server, builds adapters between the Go core and OpenCode, experiments with and defines routing policies, handles context compression, embeddings, and heuristic calculations.
- **Execution Layer**: **OpenCode** acts as the model interface layer, owning the actual LLM API calls and enabling easy model swapping.

## 2. Cross-Language Communication
- **CGO / Shared Library**: The Go core will be compiled as a C shared library (`.so`). The Python glue will interface directly with this library using `ctypes` or `cffi`. This provides high-performance, in-process communication while maintaining the language boundary.

## 3. Retrieval & Context Compression (Python Layer)
- **Embedding Strategy**: A hybrid approach combining BM25 (lexical) and local embeddings (e.g., `all-MiniLM-L6-v2`) to filter candidates before passing them to the decision router.
- **Code Graph Traversal Depth**: **Dynamic**. Retrieval starts at Depth-1 (direct neighbors: callers, callees, imported types). If the router detects high entropy or low coherence in the results, it triggers an `expand` action to traverse to Depth-2.
- **Reranker Batch Sizing**: Uses **dynamic batching** constrained by `MAX_BATCH_TOKENS` to balance throughput and memory. For small candidate sets (< 4), the reranker falls back to sequential processing to maximize observability and debugging clarity.

## 4. Decision Router Policy (Python Layer)
- **Implementation**: The router decision layer is implemented as a **Heuristic Rule Engine**. It uses strict, manually tuned thresholds (e.g., reranker entropy, score distributions) to deterministically output `proceed`, `expand`, or `escalate`. This fits perfectly within the Python domain while Go enforces the resulting graph transitions.

## 5. Validation Loops & Decomposition (Go Layer)
- **L3 Decomposition Granularity (Hard Tasks)**: **Per-File Decomposition**. When the L3 Validator breaks down a "hard" `code_writing` task, it assigns each L2 subagent to create or modify exactly one specific file. This guarantees clean boundaries and simplifies the final merge process.
- **Retry Limits & Fallbacks**:
  - **Normal Tier**: Maximum 2 retries.
  - **Hard Tier**: Maximum 5 retries.
  - **Exhaustion Behavior**: If the retry limit is hit without L3 validation passing, the Go engine halts the loop and escalates the task to the User for manual intervention, strictly enforcing correctness over automated guessing.
