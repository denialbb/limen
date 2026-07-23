# Arch #2 — Extract validateCandidate() seam

**Strength:** Strong · **Package:** `internal/orchestrator`

## Problem
`OrchestratorImpl.RunTask` (`internal/orchestrator/orchestrator.go`) contains TWO full
validation implementations:
- callback path: ~lines 328-415
- synchronous path: ~lines 463-544

Both independently do the same sequence: transition to `AwaitingValidation`,
`GetWorktreeDiff`, provision throwaway worktree, `validator.Evaluate`, destroy worktree,
`CheckForConflicts` / `ExtractConflictRegions`, `RecordValidationDecision`, then the
pass/fail + retry-budget branch. ~80 lines duplicated with subtle divergences.

Note: on the callback path the `feedback` local reassigned near line 410 is DEAD (the
long-lived Pi process never re-enters `ProduceSolution`); confirm and remove the dead
assignment as part of this, or document why it must stay.

## Goal
Extract a single method, e.g.
`func (o *OrchestratorImpl) validateCandidate(ctx, task, wt) (ValidationOutcome, error)`
that owns the shared validation body. Call it from BOTH the callback and synchronous
paths, which become thin dispatchers.

## Acceptance
- One validation body; both paths call it. Duplicated block removed.
- Behavior preserved for both paths (pass, fail-with-retry, retry-budget-exhausted, conflict extraction).
- Dead `feedback` reassignment on the callback path removed (or justified in a comment).
- Add a unit test that drives `validateCandidate` with a mock `Store` + mock `Validator` for at least: pass, fail-with-revision, and a conflict case.
- `go build ./...`, `go vet ./...`, existing tests pass.

## Constraints
- No behavior change. Pure dedup + testability.
- Match surrounding style. No historical comments.
