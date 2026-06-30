# Limen Determinism Boundary

To ensure replayability and correctness without bloating the canonical state, Limen draws a strict line between what must be explicitly reproducible and what is considered transient exhaust.

## 1. Must Be Reproducible (Stored in Go Canonical State)

These elements form the immutable record of a task and must be perfectly reproducible during a replay or audit:

- **Task state transitions**: The exact path taken through the state machine (e.g., `WORKER_RUNNING` → `AWAITING_VALIDATION` → `REVISION_REQUESTED`).
- **Final outputs**: The canonical patches, generated code, or documentation produced by the Worker.
- **Validation decisions**: The explicit `approve`, `reject`, or `request_revision` signals from the Validator, including the structured issues/feedback provided.
- **Retrieved context snapshot (light version)**: A manifest of exactly what context (files, snippets, graphs) was passed to the LLM. We must know *what* the LLM saw, but not necessarily the math used to select it.

## 2. Does NOT Need to Be Reproducible (Transient)

These elements are operational exhaust. They are used to make decisions in the moment but do not dictate the truth of the final outcome. Storing them would cause massive database bloat for no correctness benefit.

- **Reranker scores**: The floating-point confidence scores between query and document pairs.
- **Embedding vectors**: The raw tensor representations from the MiniLM semantic layer.
- **Internal model logits**: Token probabilities or generation exhaust from the Worker/Validator.
- **Fine-grained event streams**: Fine-grained streams stay ephemeral by principle (generation exhaust is transient).
- **Transient routing heuristics**: The raw entropy values or intermediate math the Router used before emitting the final `proceed/expand/escalate` signal.

---

## 3. Minimal Trace Contract (Light Observability)

To enforce this determinism boundary without storing full retrieval provenance, the canonical SQLite database strictly records only the following for observability:

1. `task_id`
2. `state transitions`
3. `tool calls` (This inherently captures the retrieved context snapshot as arguments passed to tools or responses returned by them)
4. `final outputs`
5. `validation decisions`
