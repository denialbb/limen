# Limen Git Worktree Contract

Git is merely a filesystem virtualization artifact managed by the Go Core. The Go Core is the ultimate authority of state; Git is just its isolation mechanism.

## 1. Isolation & State Management
- **Worktree Provisioning**: When a task begins execution, Go Core provisions an isolated environment via `git worktree add`.
- **No Git Noise**: Workers do not make Git commits. Intermediate checkpoints and revisions live exclusively in the Go SQLite database (as patches or metadata). Git logs must remain completely clean and free of "WIP" noise.
- **Path Abstraction**: The Python MCP server transparently maps tool paths to the specific worktree. However, the tool metadata provides the LLM with abstract context (e.g., "You are working on a branch derived from `main@a1b2c3d`"). This acknowledges "git reality" so the LLM anticipates parallel changes without needing to execute git commands itself.

## 2. Concurrency & Conflict Protocol
- **Optimistic Concurrency**: Multiple workers operate in parallel worktrees derived from the same base.
- **Structured Conflict Resolution**: We do not dump raw `<<<< HEAD` git conflict markers into a generic worker's workspace. If a rebase/merge conflict occurs when attempting to commit an approved task:
  1. The system detects the conflict.
  2. Go Core extracts the conflicting diff regions.
  3. A specific, structured "Conflict Repair" step is initiated (e.g., prompting a worker with "These two intentions conflict: Diff A vs Diff B. Propose a resolution patch.").
  4. The proposed resolution is re-applied and must pass the validation loop again.

## 3. Destruction
- Worktrees are ephemeral. Immediately upon successful squash-merge into the canonical branch (or upon terminal failure/escalation), the worktree directory is destroyed by the Go Core.
