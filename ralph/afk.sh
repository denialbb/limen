#!/bin/bash
set -eo pipefail

if [ -z "$1" ]; then
    echo "Usage: $0 <iterations>"
    exit 1
fi

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

for ((i = 1; i <= $1; i++)); do
    tmpfile=$(mktemp)
    trap "rm -f $tmpfile" EXIT

    commits=$(git log -n 5 --format="%H%n%ad%n%B---" --date=short 2>/dev/null || echo "No commits found")
    issues=$(cat issues/*.md 2>/dev/null || echo "No issues found")
    prompt=$(cat ralph/prompt.md)
    task_file=$(
        jq -r '
        .issues
        | map(select(.status=="todo"))
        | .[0].file
        ' state.json
    )

    docker sandbox run opencode \
        --agent build \
        --dangerously-skip-permissions \
        --format json \
        "Previous commits: $commits Issues: $issues $prompt" |
        grep --line-buffered '^{' |
        tee "$tmpfile" |
        jq --unbuffered -rj "$stream_text"

    result=$(jq -r "$final_result" "$tmpfile")

    if [[ -z "$task_file" ]]; then

        if ./verify_issue.sh "$task_file"; then
            echo "Ralph complete after $i iterations."
            exit 0
        fi

        echo "State empty but verification failed"
        exit 1
    fi

    if [[ "$result" == *"<promise>NO MORE TASKS</promise>"* ]]; then
        echo "Model claims completion (ignored)"
    fi
done
