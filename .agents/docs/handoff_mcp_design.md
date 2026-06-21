# Limen: MCP Design & Execution Architecture

This document outlines the design decisions for how Limen interfaces agnostically with its execution environments (Antigravity and OpenCode) via the Model Context Protocol (MCP), resolving the infrastructure, state management, and file system isolation requirements.

## 1. Dual MCP Server Architecture
- **Two Distinct Servers**: The Python glue layer exposes two separate MCP entry points:
  - `limen-mcp-validator`: Used exclusively by Antigravity (L3 Validator).
  - `limen-mcp-worker`: Used exclusively by OpenCode (L2 Workers).
- This enforces strict isolation between the validation and execution environments, allowing tailored toolsets for each role.

## 2. State Synchronization & Transport
- **Canonical State (Go Core)**: The Go engine owns all canonical state (tasks, revisions, contexts, validator reports, retrieval metadata) in a local database (e.g., SQLite/BoltDB).
- **Transport (Redis)**: Redis streams are used for inter-process communication, handling pending jobs, notifications, and retry requests.
- **Minimal Payloads**: MCP tool payloads and stream messages are kept strictly minimal, passing only references like `task_id` and `revision`.

## 3. Workflow Invocation
- **Push via API/CLI**: The Go core initiates the workflows programmatically. It triggers the target client (Antigravity CLI or OpenCode API) and injects the initial system prompt, passing the relevant `task_id` directly to the LLM.

## 4. MCP Tool Structure
- **Action-Oriented Tools**: LLMs are provided with declarative, action-oriented tools rather than low-level event pollers.
  - Workers use tools like `get_task_context(task_id)` and `submit_work(task_id, content)`.
  - Validators use tools like `submit_validation_report(task_id, report)`.
- The LLM determines when to use these tools based on the instructions and `task_id` provided in the initial injected prompt.

## 5. Filesystem Isolation & Concurrency
- **Git Worktrees**: To support concurrent workers and validators without conflicts, Limen utilizes `git worktree`.
  - When a task begins, Go executes `git worktree add .limen/<task_id> HEAD`.
  - The worker modifies the code within this isolated branch.
  - Upon L3 validation approval, the worktree is patched/merged into the main branch and the worktree is deleted.
- **Transparent Path Rewriting**: To keep the LLM prompts simple, the models use standard repository paths (e.g., `src/main.py`). The Python MCP Server intercepts `read_file` and `write_file` calls and transparently rewrites the paths to the active `.limen/<task_id>/src/main.py` based on the session context.
