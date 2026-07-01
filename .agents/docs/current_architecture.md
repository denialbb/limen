# Current Architecture

This document reflects the simplified architectural pivot for the Limen project, replacing previous iterations that relied on CGO, Redis, and complex state synchronization.

## Architecture Matrix

| Component       | Technology                   |
| --------------- | ---------------------------- |
| Canonical state | SQLite WAL                   |
| State owner     | Go Core                      |
| Access pattern  | CLI subprocess               |
| MCPs            | Thin Python clients          |
| Messaging       | None                         |
| Async jobs      | None                         |
| Upgrade path    | JSON-over-unix-socket daemon |

## Key Architectural Decisions

1. **Simplicity First**: We have dropped the Redis transport layer and CGO shared library bindings in favor of a strictly simpler architecture.
2. **State Management**: The Go Core is the exclusive owner of the state, utilizing SQLite in WAL (Write-Ahead Logging) mode to safely handle concurrency.
3. **Execution Model**: The Python MCP servers act as completely thin clients. Whenever an MCP tool is invoked, Python simply spawns the Go Core as a CLI subprocess, passing arguments via standard I/O.
4. **Synchronous Flow**: There is no messaging queue (Redis) or async job runner. Execution is synchronous through the CLI subprocesses.
5. **Future-Proofing**: The system is designed to easily upgrade from a CLI subprocess model to a long-running JSON-over-unix-socket daemon if/when performance or state permanence demands it.

## Process Topology

The process topology is designed around CLI-agnostic cognition where agents are spawned as subprocesses. For example, using the default Pi RPC mode:

```
P1: limen run-task  (orchestrator, parent)
     ├─ spawns ─► P2: pi --mode rpc  (worker; cwd = worktree)
     │                 └─ bash tool runs ─► P3: limen ready-for-review  (blocking callback)
     └─ spawns ─► PV: validator CLI  (cwd = throwaway worktree)
                       └─ bash tool runs ─► limen submit-verdict
```

P1, P3, PV share no memory — only SQLite + filesystem. All cross-process signaling is SQLite-mediated via a signaling table.

## Driver Seam

The `orchestrator.Worker` and `orchestrator.Validator` interfaces are synchronous-blocking from the orchestrator's view. There are two primary drivers:

- **Pi driver** (default): drives `pi --mode rpc`; the blocking `ready-for-review` callback owns the revision loop.
- **MCP / spawn-and-callback driver** (fallback): spawns a model-gated CLI, serves the callback (CLI-via-bash or MCP tool), blocks until the verdict lands.

## Safety & Sandboxing

**Known gap:** The system currently operates in a trusted posture. The agent has native filesystem access in the worktree (your machine, your repo, your task). Sandboxing is deferred; future implementation will encapsulate the whole Limen process in Docker.
