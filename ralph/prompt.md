# ISSUES

Local issue files from `issues/` are provided at start of context. Parse them to
understand the open issues. Each issue file begins with a `Type: AFK` or
`Type: HITL` line — only AFK issues are yours to work on.

You will work on exactly ONE issue per run: the issue file named in your prompt.
Do not touch any other issue file. Do not work on HITL issues.

You've also been passed a file containing the last few commits. Review these to
understand what work has been done.

# TASK SELECTION

The script picks the next AFK issue whose blockers are all complete and passes
it to you by filename. Work only on that issue. Prioritize, within the issue,
in this order:

1. Critical bugfixes
2. Development infrastructure
3. Tracer bullets for new features (small end-to-end slices through all layers)
4. Polish and quick wins
5. Refactors

# EXPLORATION

Explore the repo to understand the existing code before editing.

# IMPLEMENTATION

Use /tdd to complete the task.

# FEEDBACK LOOPS

This is a Go + Python repo. Before committing, run:

- `go test ./...` to run the Go tests
- `pytest` to run the Python tests

Run the issue's `## Verify` commands too — they are the issue's acceptance
contract and the outer loop will re-run them to decide completion.

# COMMIT

Make a git commit. The commit message must:

1. Include key decisions made
2. Include files changed
3. Blockers or notes for next iteration

# THE ISSUE

If the task is complete (all `## Verify` commands pass and all acceptance
criteria boxes can be checked), stop. Do not move the issue file yourself; the
outer script verifies and moves it on success.

# FINAL RULES

ONLY WORK ON THE NAMED ISSUE. DO NOT commit until `go test ./...` and `pytest`
both pass.