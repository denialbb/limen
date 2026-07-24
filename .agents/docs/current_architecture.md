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

P1, P3, PV share no memory — only SQLite + filesystem. The worker's ready/verdict handshake (P3) is SQLite-mediated via a signaling table. The validator path shown (`submit-verdict`) is the autonomous topology; the default `agentValidator` instead reports a `LIMEN_VERDICT` stdout sentinel that P1 parses and records, so P1 owns verdict recording (ADR 0009).

## Driver Seam

The `orchestrator.Worker` and `orchestrator.Validator` interfaces are synchronous-blocking from the orchestrator's view. Cognition is CLI-agnostic: any headless coding-agent CLI is pluggable per-role at the wiring site (`--worker-backend`, `--validator-backend`), with the orchestrator untouched (ADR 0009).

**Worker** — a single generic `agentWorker` driver (`internal/remote/agent.go`) parameterized by a `dialect` (argv builder, optional stdin-prompt encoder, pure line decoder, constraint block). Two families share it:

- **RPC family** (pi, claude stream-json): prompt written to stdin, process stays alive, rich per-line events, explicit end event (`agent_end` / `result`) closes stdin.
- **One-shot family** (opencode, agy): prompt in argv, stdin closed, stdout scanned to EOF.

In-process revision is free on all CLIs: the blocking `limen ready-for-review` bash call holds the agent loop open across verdict rounds. Each dialect owns only its constraint block; `renderWorkerPrompt` owns the shared contract (task, ADR-0007 context manifest, ready-for-review). Supported: `pi`, `claude`, `opencode`, `agy` (plus the `cli`/`mock` placeholders).

**Validator** — `agentValidator` spawns a validator CLI in the throwaway worktree with a Level-3 prompt (inspect diff, run tests) and parses the last `LIMEN_VERDICT: {"passes":...,"feedback":...}` stdout sentinel. The stdout sentinel (not `submit-verdict`) keeps verdict recording with the orchestrator, avoiding the callback race in the synchronous `Evaluate` flow. Supported: `shell` (cliValidator), `agy`, `claude`, `opencode` (plus `mock`).

**Decision: CLI subprocess + `limen` callbacks, no MCP server** (ADR 0009). The `limen` binary is already the tool surface for every bash-capable agent; MCP would cover only the callback channel while prompt delivery, lifecycle, and observability stay per-CLI. Revisit trigger: an agent with no shell execution, or a pull-model retrieval contract.

## Safety & Sandboxing

**Known gap:** The system currently operates in a trusted posture. The agent has native filesystem access in the worktree (your machine, your repo, your task). Sandboxing is deferred; future implementation will encapsulate the whole Limen process in Docker.
