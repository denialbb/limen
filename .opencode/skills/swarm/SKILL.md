---
name: swarm
description: >-
  Use when the user wants to subdivide a body of work into parallel tasks,
  dispatch multiple coding subagents concurrently, route each result through
  a code reviewer, and iterate until all reviews pass. Trigger phrases include
  "swarm", "subdivide and dispatch", "parallel agents", or any request to
  send multiple subagents and review their work.
---

# Swarm Workflow

This skill coordinates a parallel subagent workflow: subdividing work,
dispatching coding agents, forwarding their output to a reviewer, saving
reviews, validating them, and re-tasking agents until all blockers are
resolved.

## Roles

- **Orchestrator (you)**: subdivides the work (pair this with the /prd-to-issues
  skill to create tracer bullets vertical slices), dispatches agents, receives
  commit hashes, forwards to the reviewer, saves reviews, validates them
  against your own judgment, and decides whether to re-task or proceed.
- **Coding subagent (`python-go-coder`)**: implements a single,
  well-scoped task. Creates or modifies files, writes tests, runs the build
  and test suite, commits, and reports back the commit hash and a summary.
- **Reviewer subagent (`code-reviewer`)**: receives a commit hash (or diff),
  reviews it against the project's design documents and coding standards,
  and returns a structured verdict with blockers and non-blocking notes.

## Workflow Steps

### 1. Subdivide

Break the work into the smallest possible vertical slices use the /prd-to-issues skill if possible. Prefer tasks that touch disjoint file sets so agents can run concurrently without git conflicts. If tasks have dependencies, group them into waves: tasks within a wave run in parallel; a wave only starts after the previous wave's reviews pass.

### 2. Dispatch

Launch one `python-go-coder` Task per task in the current wave. Each agent prompt MUST include:

- The specific files to create or modify.
- The design document path(s) that govern the work.
- The exact interfaces, types, or signatures to implement.
- The requirement to run `go build` and `go test ./...` before committing.
- The requirement to commit with a clear, bullet-pointed message.
- The instruction to report back the commit hash and a summary of changes.

Use `git checkout -b <branch>` or worktrees per agent if tasks might touch overlapping
files; otherwise all agents may commit on the current branch.

### 3. Review

For each completed task, launch a `code-reviewer` Task with:

- The commit hash (or range) to review.
- The design document path(s) to check adherence against.
- The specific task scope so the reviewer knows what is in and out of scope.

### 4. Save Reviews

Save each reviewer output to `docs/reviews/` with a descriptive filename
(e.g., `docs/reviews/<task-name>_review.md`). Include the commit hash,
the reviewer's verdict, blockers, and non-blocking notes. This creates a
durable audit trail for both you and the reviewer in future iterations.

### 5. Validate

Read each review and form your own judgment. For each blocker the reviewer
raises:

- If you agree: re-task the coding agent with specific fix instructions
  referencing the blocker.
- If you disagree: note your reasoning, override the blocker, and document
  why in the saved review file or in your response to the user.

Do not blindly trust the reviewer. Do not blindly trust the coder. You are
the final gate.

### 6. Iterate

Re-dispatch coding agents for any agreed-upon blockers. Re-review only the
fixes (not the entire task, unless the fixes are invasive). A task is
"done" when:

- The reviewer's verdict is PASS, OR
- You have explicitly overridden every remaining blocker with documented
  reasoning.

### 7. Proceed

Once all tasks in a wave pass, move to the next wave. Once all waves are
complete, summarize the full body of work to the user with commit hashes
and final review statuses.

## Concurrency Rules

- Launch multiple coding agents in a single message whenever their tasks
  are independent.
- Launch multiple reviewer agents in a single message whenever their
  reviews are independent.
- Never launch a coding agent for a task that depends on an unreviewed or
  failed task from a prior wave.
- Keep exactly one in-progress todo per wave to track the wave's status.

## Failure Handling

- If a coding agent reports a build or test failure, re-task it with the
  error output before forwarding to review.
- If a coding agent cannot complete the task (e.g., ambiguity), resolve
  the ambiguity yourself and re-task with clearer instructions.
- If the reviewer and you disagree on a fundamental design point, surface
  the disagreement to the user rather than silently overriding.
