#!/bin/bash
set -eo pipefail

SESSION_FILE=".ralph_session"

if [[ -f "$SESSION_FILE" ]]; then
    SESSION_ID=$(cat "$SESSION_FILE")
else
    SESSION_ID=$(opencode sessions create 2>/dev/null || opencode chat --new-session 2>/dev/null)
    echo "$SESSION_ID" >"$SESSION_FILE"
fi

echo "Using session: $SESSION_ID"

iterations=${1:-20}
mkdir -p issues/done

stream_text='
select(.type == "text")
| .part.text // empty
| gsub("\n"; "\r\n")
| . + "\r\n\n"
'

final_result='
[select(.type == "text") | .part.text]
| last // ""
'

# An issue is AFK if its first non-empty line matches "Type: AFK".
is_afk() {
    local f="$1"
    awk '
        NF { print; exit }
    ' "$f" | grep -q '^Type: AFK'
}

# An issue is done if its file has been moved into issues/done/.
is_done() {
    local f="$1"
    [ -f "issues/done/$(basename "$f")" ]
}

# An issue is blocked if any file referenced under "## Blocked by" is not done.
# Lines look like:  - Blocked by `issues/NNN-title.md`
# or:                None - can start immediately
is_unblocked() {
    local f="$1"
    local deps
    deps=$(awk '
        $0 == "## Blocked by" {flag=1; next}
        flag && /^## / {exit}
        flag && NF {
            # extract issues/NNN-*.md token between backticks
            if (match($0, /`issues\/[^`]+`/)) {
                print substr($0, RSTART+1, RLENGTH-2)
            } else if (match($0, /issues\/[A-Za-z0-9._-]+\.md/)) {
                print substr($0, RSTART, RLENGTH)
            }
        }
    ' "$f")
    [ -z "$deps" ] && return 0
    for d in $deps; do
        if [ -f "$d" ] && ! is_done "$d"; then
            return 1
        fi
    done
    return 0
}

# Pick the next AFK, not-done, unblocked issue (lowest-numbered first).
pick_next() {
    for f in $(ls issues/*.md 2>/dev/null | sort); do
        [ -f "$f" ] || continue
        is_afk "$f" || continue
        is_done "$f" && continue
        is_unblocked "$f" || continue
        echo "$f"
        return 0
    done
    return 1
}

# Count remaining AFK work.
count_remaining() {
    local n=0
    for f in issues/*.md; do
        [ -f "$f" ] || continue
        is_afk "$f" || continue
        is_done "$f" && continue
        n=$((n + 1))
    done
    echo "$n"
}

write_state() {
    local status="$1" task="$2"
    python3 - "$status" "$task" <<'PY'
import json, os, sys
status, task = sys.argv[1], sys.argv[2]
path = "state.json"
try:
    with open(path) as fh:
        state = json.load(fh)
except (FileNotFoundError, json.JSONDecodeError):
    state = {"tasks": {}}
tasks = state.setdefault("tasks", {})
for f in sorted(os.path.dirname(p) or "." for p in []):
    pass
# build fresh task map from issues/ + issues/done/
fresh = {}
for d in ("issues", "issues/done"):
    if not os.path.isdir(d):
        continue
    for name in sorted(os.listdir(d)):
        if not name.endswith(".md"):
            continue
        full = os.path.join(d, name)
        with open(full) as fh:
            first = ""
            for line in fh:
                if line.strip():
                    first = line.strip()
                    break
        kind = "HITL" if first.startswith("Type: HITL") else "AFK"
        done = d == "issues/done"
        fresh[name] = {"type": kind, "done": done}
tasks = fresh
state["tasks"] = tasks
state["last"] = {"status": status, "task": task}
with open(path, "w") as fh:
    json.dump(state, fh, indent=2, sort_keys=True)
PY
}

prompt_body=$(cat ralph/prompt.md)

for ((i = 1; i <= iterations; i++)); do
    remaining=$(count_remaining)
    if [ "$remaining" -eq 0 ]; then
        echo "All AFK tasks complete after $((i - 1)) iterations."
        write_state "complete" ""
        exit 0
    fi

    task_file=$(pick_next || true)
    if [ -z "$task_file" ]; then
        echo "$remaining AFK task(s) remain but all are blocked. Deadlock."
        write_state "blocked" ""
        exit 1
    fi

    echo "=== Iteration $i: $task_file ($remaining remaining) ==="

    commits=$(git log -n 5 --format="%H%n%ad%n%B---" --date=short 2>/dev/null || echo "No commits found")
    issue_text=$(cat "$task_file")

    tmpfile=$(mktemp)
    trap "rm -f $tmpfile" EXIT

    prompt=$(
        cat <<EOF
Previous commits:
$commits

You are working on this single issue file: $task_file

ISSUE CONTENTS:
$issue_text

$prompt_body
EOF
    )

    opencode run \
        --session "$SESSION_ID" \
        --agent python-go-coder \
        --dangerously-skip-permissions \
        --format json \
        "$prompt" |
        grep --line-buffered '^{' |
        tee "$tmpfile" |
        jq --unbuffered -rj "$stream_text"

    result=$(jq -r "$final_result" "$tmpfile" 2>/dev/null || echo "")

    if ./verify_issue.sh "$task_file"; then
        echo "Verify passed for $task_file; moving to issues/done/"
        mv "$task_file" "issues/done/$(basename "$task_file")"
        write_state "passed" "$task_file"
    else
        echo "Verify FAILED for $task_file; leaving open for next iteration."
        write_state "failed" "$task_file"
    fi

    if [[ "$result" == *"<promise>NO MORE TASKS</promise>"* ]]; then
        echo "Model claims completion (ignored; verifier is source of truth)."
    fi
done

remaining=$(count_remaining)
echo "Reached iteration cap; $remaining AFK task(s) still open."
write_state "cap_reached" ""
exit 1
