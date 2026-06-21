# Handoff: Multi-Agent System — Router + Worker + Validator Architecture

## Session Summary

Grilling session on designing a 3-layer multi-agent system with a Router model on top, fast worker models in the middle, and a strong validator model at the bottom.

## Design Decisions

### Primary Goal
- **Quality/Correctness** — Layer 3 (the strong model) is the gate. Layers 1+2 are pre-processing.

### Layer 1 — Router
- **Classifies on**: Both task type AND complexity (2D matrix)
- **Compresses**: Prompt compression + context compression (distills user prompt and pulls relevant codebase context)
- **Output**: Task type + complexity tier + compressed prompt + context

### Routing Matrix (Task × Complexity → Flow)

| Task | easy | normal | hard |
|---|---|---|---|
| code_writing | L2 | L2→L3 | L3 (decompose → subagents → validate) |
| debugging | L2 | L2→L3 | L3 |
| architecture | L2→L3 | L3 | L3 |
| document_writing | L2 | L2→L3 | L2→L3 |
| research | L2→L3 | L2→L3 | L3 |

- **L2 only**: Worker produces output, done.
- **L2→L3**: Worker produces, L3 validates with tiered retry protocol.
- **L3 only**: Strong model handles directly (with decomposition for code_writing/hard).

### Task Types (5)
code_writing, debugging, architecture, document_writing, research

### Complexity Tiers (3)
easy, normal, hard

### Layer 2 — Workers
- **One specialized model per task type** (5 distinct workers, not a single generalist)
- Each worker tuned independently for its task domain

### Layer 2 → Layer 3 Validation Protocol
- **Tiered by complexity**:
  - **easy**: No L3 validation (L2 only)
  - **normal**: L3 spot-check, capped retries (L2 revises, L3 re-checks ≤ N rounds)
  - **hard**: Full structured review (L3 produces issue list with severity + suggested fixes, L2 iterates until all resolved)

### Open-Source Landscape
- **No OOTB solution** matches this specific architecture.
- **LangGraph** (LangChain) is the best foundation: conditional routing nodes, subgraphs for workers, validation loops with re-entry.
- AutoGen, CrewAI, Dify offer partial matches but require custom L1 implementation.
- Semantic Kernel is a weak fit.

## Decisions Closed
- [x] Primary goal: Correctness
- [x] Router does: classify + compress (both prompt + context)
- [x] Classification schema: 2D (task type × complexity)
- [x] Task types: code_writing, debugging, architecture, document_writing, research
- [x] Complexity tiers: easy, normal, hard
- [x] Routing matrix: as defined above
- [x] L2 staffing: one specialized model per task type
- [x] L2→L3 protocol: tiered by complexity

## Decisions Still Open (Next Session)
- Which specific models for L2 workers? (e.g., Llama 3.1 8B vs. CodeLlama vs. Qwen2.5-Coder)
- Which strong model for L3 validator? (e.g., GPT-4o, Claude Opus, DeepSeek-R1, Llama 405B)
- Embedding model for the Router's compression step?
- Decomposition granularity for code_writing/hard (how does L3 decide subproblem boundaries?)
- Max retry rounds for L2→L3 normal/hard tiers?
- Infrastructure: self-hosted vs. API-based? Which inference provider?
- Metrics for validator correctness? (agreement with held-out human review?)
- Router accuracy benchmarks — what's the target before shipping?

## Suggested Skills for Next Agent
- **grilling** — Continue stress-testing the open decisions above (especially model selection and decomposition strategy).
- **handoff** — Load this document to pick up where this session left off.
- If building a prototype: any skill for the chosen framework (e.g., LangGraph prototyping).

## References
- [AutoGen](https://github.com/microsoft/autogen)
- [CrewAI](https://github.com/crewAIInc/crewAI)
- [LangGraph](https://github.com/langchain-ai/langgraph)
- [Dify](https://github.com/langgenius/dify)
- [Semantic Kernel](https://github.com/microsoft/semantic-kernel)

*No project-internal artifacts to reference (this is a greenfield design).*
