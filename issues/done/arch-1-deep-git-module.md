# Arch #1 — Collapse cliGit into one deep git module

**Strength:** Strong · **Package:** `internal/git`, `internal/orchestrator`, `cmd/limen`

## Problem
`cliGit` (`cmd/limen/main.go:185-239`) is a shallow pass-through: 7 of its 8 methods
delegate one-to-one to `git.WorktreeManager`. `orchestrator.GitClient`
(`internal/orchestrator/orchestrator.go:68-85`) and `git.WorktreeManager`
(`internal/git/worktree.go:36-51`) are near-identical interfaces. The only real logic
in `cliGit` is `IsValid` (`git rev-parse` + `git status --porcelain` + `git fsck`).

## Goal
Delete the pass-through wrapper. Let `git.WorktreeManager` satisfy
`orchestrator.GitClient` directly, and move the repo-validity logic into the `git`
package as a method on `WorktreeManager` (e.g. `IsValidRepo()` / `IsValid()`).

## Acceptance
- `cliGit` struct and its delegating methods are gone from `cmd/limen/main.go`.
- The repo-validity check lives in `internal/git` (a `WorktreeManager` method), not in `cmd`.
- `orchestrator.RunTask` receives a `git.WorktreeManager` (or an interface it satisfies) directly — no wrapper.
- `orchestrator.GitClient` and `git.WorktreeManager` are reconciled: keep ONE interface. If `orchestrator` still needs an interface for testing, it should be the minimal set `WorktreeManager` already satisfies.
- Add/keep a unit test for the repo-validity logic in `internal/git`.
- `go build ./...` and `go vet ./...` pass. Existing tests still pass.

## Constraints
- Do NOT change behavior of any git operation. Pure structural deepening.
- Match surrounding code style. No historical/"used to" comments.
