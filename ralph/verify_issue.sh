#!/usr/bin/env bash
set -eo pipefail

issue_file="$1"

[ -n "$issue_file" ] || { echo "Usage: $0 <issue_file>" >&2; exit 1; }
[ -f "$issue_file" ] || { echo "Issue file not found: $issue_file" >&2; exit 1; }

# Extract lines after "## Verify" until the next "## " header.
# Strip triple-backtick fence lines so the command is plain shell.
verify_cmd=$(awk '
  $0 == "## Verify" {flag=1; next}
  flag && /^## / {exit}
  flag && NF && $0 !~ /^```/ {print}
' "$issue_file")

[ -n "$verify_cmd" ] || { echo "No ## Verify block found in $issue_file" >&2; exit 1; }

echo "Running verify for $issue_file:"
echo "$verify_cmd"
bash -lc "$verify_cmd"