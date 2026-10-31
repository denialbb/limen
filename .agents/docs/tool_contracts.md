# Limen Tool Contracts

## Key Invariant
**Tools represent intent, not system mechanics.** 

LLMs should invoke high-level actions. The underlying infrastructure (Go Core, Git Worktrees, SQLite WAL) handles the execution details.

---

## 1. Worker Tools (MCP)

These tools are exposed to the L2 Worker (e.g., via OpenCode) to interact with its task.

- **`get_context(task_id)`**: Requests the initially compiled context payload (provided by the Router/Retrieval layer).
- **`request_more_context(task_id, reason)`**: Signals that the provided context is insufficient to complete the task, triggering further retrieval expansion.
- **`submit_work(task_id, summary)`**: Signals intent that the worker has finished editing the isolated `git worktree` and is ready for validation. (The system mechanically handles diffing the worktree).

---

## 2. Validator Tools & Wire Verdicts

These contracts are exposed to the L3 Validator (e.g., via Antigravity CLI, Claude, OpenCode, or Shell) to enforce correctness. At the wire protocol level, agent validators report their outcome via the `LIMEN_VERDICT` stdout sentinel containing a JSON payload with `passes` (bool), `fatal` (bool), and `feedback` (string).

- **`approve(task_id)` (`passes=true, fatal=false`)**: Signals absolute correctness. Triggers worktree commit and merge.
- **`request_revision(task_id, issues)` (`passes=false, fatal=false`)**: Signals retryable flaws (ordinary test/build failures). Returns feedback to the Worker and initiates a retry loop bounded by `MaxRetries`.
- **`reject(task_id, reason)` (`passes=false, fatal=true`)**: Fatal rejection. Indicates non-retryable policy or invariant violations, or an implementation judged fundamentally flawed/unsalvageable. Escalates directly to `FAILED_ESCALATED` and bypasses remaining retries without consuming a retry count.
- **Invalid Verdict**: `passes=true` combined with `fatal=true` is contradictory and rejected as a transport/system error. Missing `passes` or `fatal` fields in the sentinel JSON are likewise rejected as malformed.
- **`expand_context(task_id, reason)`**: Signals that the Validator lacks sufficient codebase context to definitively prove or disprove correctness.

---

## 3. Router Interfaces

*Note: In the current phase, the Router is implemented as a Pure Heuristics Rule Engine, so these represent internal API contracts. However, the interfaces are designed to allow for an LLM component in the future. If uncertainty exceeds a defined threshold, an LLM may be invoked to evaluate the signals and dynamically decide whether to `proceed`, `expand`, or `escalate`.*

- **`classify(task)`**: Determines the task type (e.g., `code_writing`, `debugging`).
- **`estimate_complexity(task)`**: Assigns a tier (`easy`, `normal`, `hard`) to dictate the routing matrix.
- **`decide_routing(metadata)`**: Evaluates entropy and coherence to output a routing decision (`proceed`, `expand`, `escalate`).
