# Limen

**Limen is fundamentally a correctness-oriented workflow engine that uses LLMs as interchangeable cognitive workers.**

Limen sits between user requests and model backends, orchestrating complex, multi-agent software engineering workflows. It is not an agent framework, a distributed inference platform, or a message broker. It is a strictly controlled state machine designed to enforce code correctness before applying changes.

---

## Separation of Powers

The architecture is governed by a singular axiom:

> **Git defines feasibility, Go Core defines correctness, retrieval defines perception.**

If this separation is maintained strictly, the system stays decomposable. If any layer starts influencing another directly, the system falls into: _indistinguishable sources of truth → irreproducible behavior → impossible debugging._

---

## Core Architecture

```mermaid
flowchart TD
    subgraph PythonThinClients [Python Execution Layer]
        Router[Router Policy Engine<br/>L1]
        Worker[MCP Worker<br/>L2]
        Validator[MCP Validator<br/>L3]
    end

    subgraph GoCore [Go Orchestration Engine]
        CLI[Subprocess CLI API]
        SM[State Machine]
        DB[(SQLite WAL)]
        Git[Git Worktree Manager]
    end

    LLMs((LLMs)) <-->|Model Context Protocol| PythonThinClients

    Router -->|JSON / StdIO| CLI
    Worker -->|JSON / StdIO| CLI
    Validator -->|JSON / StdIO| CLI

    CLI --> SM
    SM <--> DB
    SM --> Git
    Git -->|Isolates| Filesystem[(Physical Filesystem)]
```

The system has pivoted from a pure-Python execution loop to a highly resilient hybrid architecture:

### 1. Go Core (State Owner)

The orchestration engine is written in Go. It is the exclusive owner of the system's state, using an **SQLite WAL (Write-Ahead Logging)** database to ensure state durability and replayability.

- **Git Worktree Virtualization**: The Go Core provisions isolated `git worktree` environments for every task, allowing LLM workers to operate concurrently without creating dirty git histories.
- **Orchestration Loop**: The Core sequentially gates tasks through a strict state machine (`CREATED` → `ROUTING_EVALUATION` → `WORKER_RUNNING` → `AWAITING_VALIDATION` → `APPROVED` → `COMMITTED`).

### 2. Thin Clients (Execution Layer)

The Python layer has no stateful responsibilities. It consists solely of **stateless Model Context Protocol (MCP)** adapters and the heuristic routing engine.

- **Router (L1)**: Evaluates context entropy and complexity to decide whether to proceed, expand context, or escalate to a human.
- **Workers (L2)**: Connect to LLMs to generate code inside isolated worktrees.
- **Validators (L3)**: Evaluate the worker's artifacts against the original request.

When an LLM invokes an MCP tool, the Python adapter simply formats the request and spawns the Go Core as a CLI subprocess to manipulate the canonical state.

---

## The Main Loop

The Go Core ensures absolute correctness by executing this procedural pipeline for every task:

1. **Is Git state valid?**

   └─ `no` → initiate semantic conflict resolution

2. **Build retrieval context** (ephemeral manifest)
3. **Worker produces candidate solution** (inside isolated worktree)
4. **Validator evaluates correctness**
5. **If validator fails** → trigger retry loop
6. **If Git conflict on merge** → semantic resolution step
7. **If both Git and Validator agree** → squash merge and commit via Go Core

---

## Development Status

Limen is currently in its initial **Implementation Phase**.
We have completed a comprehensive **Test-Driven Development (TDD)** pass:

- [x] Formalized all capability constraints, invariants, and boundaries in `.agents/docs/`
- [x] Stubbed the Go SQLite WAL state machine interfaces
- [x] Stubbed the Go Git Worktree virtualization interfaces
- [x] Stubbed the Python MCP stateless clients and Router policy
- [x] Established comprehensive failing test suites enforcing the separation of powers

The next immediate phase is implementing the concrete execution logic within the Go Core to satisfy the test suites.
