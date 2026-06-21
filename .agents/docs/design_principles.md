# Limen Design Principles

## Core Identity

**Limen is NOT:**
- an agent framework
- a distributed inference platform
- an operating system
- a message broker

**Limen is fundamentally:**
A correctness-oriented workflow engine that uses LLMs as interchangeable cognitive workers.

## The Separation of Powers Axiom

> **Git defines feasibility, Go Core defines correctness, retrieval defines perception.**

If you maintain this separation strictly, the system stays decomposable. If any layer starts influencing another directly, you encounter the classic agent-system failure mode: 

*indistinguishable sources of truth → irreproducible behavior → impossible debugging.*

**The Control Flow Resolution:**
- If **Git rejects** → cannot proceed (unfeasible)
- Else if **Validator rejects** → rollback / retry (incorrect)
- Else if **Retrieval inconsistent** → recompute (blind)
- Else → commit (proven)

## Architectural Principles

- **Prove the Pain**: Limen should not contain infrastructure whose necessity cannot be demonstrated by profiling or operational pain.
- **Correctness First**: Correctness is the primary optimization target.
- **Reuse Infrastructure**: Limen orchestrates cognition, not infrastructure: reuse mature tooling whenever possible.
- **Single Source of Truth**: There must be exactly one canonical state owner.
- **Durable State**: State is durable by default: transient in-memory state is considered a liability.
- **Capability Isolation**: Capability isolation over prompt discipline: components should be physically unable to violate their responsibilities.
- **Epistemic Isolation**: Validator evaluates artifacts, not process history.
- **Progressive Retrieval**: Retrieval is progressive refinement.
- **Confidence > Similarity**: Confidence is more important than similarity: router decisions are based on retrieval stability.
- **Control Plane Routing**: Router is a control plane: router does not retrieve.
- **Early Compression**: Compression belongs to Layer 1.
- **Intent-based Tools**: LLM-facing tools express intent: LLMs should call high-level actions.
- **Python Constraints**: Python never touches canonical state directly.
- **Gated Synchronization**: Validation gates synchronization: workers may operate in isolated workspaces.
- **Phased Complexity**: Complexity should be introduced in phases.
- **Replayability**: Favor replayability.
- **Workflow First**: Limen is a workflow engine with attached cognition.
- **No Hidden State in MCP**: MCP servers must be strictly stateless adapters (calling Go Core and formatting responses). They must NOT maintain task state, cache canonical decisions, or hold workflow memory. This prevents accidental divergence between the validator, worker, and router views.
