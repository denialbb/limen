# Limen Role Boundaries

This document defines the strict capability and epistemic boundaries for the three primary cognitive roles in the Limen architecture. These boundaries must be enforced by capability isolation, not merely prompt discipline.

## 1. Router

**Allowed:**
- Classify task type.
- Estimate task complexity.
- Request expansion or escalation signals (e.g., instructing the system to broaden retrieval depth or abort due to unresolvable entropy).

**Not Allowed:**
- Execute code.
- Modify the repository.
- Validate worker outputs.
- Store or manipulate canonical state.

## 2. Worker

**Allowed:**
- Generate code, text, or documentation.
- Propose patches and edits.
- Request additional context through MCP tools.

**Not Allowed:**
- Approve or reject its own work.
- Decide final correctness.
- Modify canonical global state directly (all operations must be strictly confined to isolated `git worktree` branches).

## 3. Validator

**Allowed:**
- Evaluate worker outputs against correctness criteria.
- Request revisions (providing feedback to the worker).
- Issue formal Approve/Reject signals.
- Request more context to verify assertions.

**Not Allowed:**
- Produce "final truth edits" directly (the Validator cannot fix the code itself, as doing so would leave its own work unvalidated).
- Bypass the worker iteration loop.
- Execute changes against the codebase.
