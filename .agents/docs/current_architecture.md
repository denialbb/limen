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
