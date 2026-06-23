#!/bin/bash
# Single-shot manual run: passes all issues to the agent and lets it pick.
# For the verified autonomous loop, use afk.sh instead.

issues=$(cat issues/*.md 2>/dev/null || echo "No issues found")
commits=$(git log -n 5 --format="%H%n%ad%n%B---" --date=short 2>/dev/null || echo "No commits found")
prompt=$(cat ralph/prompt.md)

opencode --agent python-go-coder --dangerously-skip-permissions \
    "Previous commits: $commits

Issues: $issues $prompt"