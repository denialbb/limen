#!/bin/bash
# ralph/review.sh — reviewer/coder iteration phase.
#
# Reviews the work that ralph/afk.sh produced, then drives a coder to fix any
# findings until the reviewer passes or the iteration cap is hit.
#
# Usage:
#   ./ralph/review.sh [iterations]      # default 5 reviewer<->coder rounds
#
# State:
#   ralph/.reviewed-up-to   - SHA of the last commit the reviewer approved.
#                             First run reviews everything since origin/main
#                             (or the root commit if no upstream is set).
#   docs/reviews/ralph-review-<ts>.md - the latest review output.
#
# Each round:
#   1. Compute the unreviewed commit range.
#   2. Run the code-reviewer agent on the diff + the done issues' acceptance
#      criteria.
#   3. If the reviewer says it passes -> record the tip and exit 0.
#   4. Otherwise run the python-go-coder agent to address the findings,
#      re-verify, and loop.

set -eo pipefail

iterations=${1:-5}
mkdir -p docs/reviews

marker="ralph/.reviewed-up-to"

# Resolve the base commit to review from.
base_for() {
    if [ -f "$marker" ] && [ -n "$(cat "$marker")" ]; then
        cat "$marker"
        return
    fi
    git rev-parse origin/main 2>/dev/null \
        || git rev-list --max-parents=0 HEAD 2>/dev/null \
        || echo ""
}

tip_sha() { git rev-parse HEAD; }

# Collect acceptance criteria from every issue that has been moved to done,
# so the reviewer checks implementation against the issue contract.
done_criteria() {
    local out=""
    for f in issues/done/*.md 2>/dev/null; do
        [ -f "$f" ] || continue
        out+="

### $(basename "$f")

$(awk '
    $0 == "## Acceptance criteria" {flag=1; next}
    flag && /^## / {exit}
    flag {print}
' "$f")
"
    done
    [ -z "$out" ] && out="(no done issues found)"
    echo "$out"
}

# Extract the verify commands from every done issue (for the coder to re-run).
done_verify() {
    local out=""
    for f in issues/done/*.md 2>/dev/null; do
        [ -f "$f" ] || continue
        out+="

### $(basename "$f")

$(awk '
    $0 == "## Verify" {flag=1; next}
    flag && /^## / {exit}
    flag && NF && $0 !~ /^```/ {print}
' "$f")
"
    done
    [ -z "$out" ] && out="(no verify commands)"
    echo "$out"
}

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

reviewer_prompt() {
    local base="$1" tip="$2"
    local diff
    diff=$(git diff "${base:+${base}..}${tip}" 2>/dev/null || git diff "$tip")
    local criteria
    criteria=$(done_criteria)

    cat <<EOF
You are reviewing the work that an autonomous coding agent (ralph) produced
overnight. Be thorough and direct; no sugarcoating. Do not approve until the
code is correct and matches the issue contracts.

The commits under review are the range ${base:+${base}..}${tip} (HEAD).

Here is the cumulative diff of that range:

\`\`\`diff
${diff}
\`\`\`

Here are the acceptance criteria from the issues that ralph claims to have
completed. The implementation must satisfy each box. Flag any criterion that
is not clearly met by the diff.

${criteria}

Review the diff for:
1. Code correctness (edge cases, error handling, resource leaks, races).
2. Adherence to the project design docs under docs/ and .agents/docs/.
3. Whether each acceptance-criterion box is actually satisfied by the code.
4. Tests: do they assert the contract, not just that code runs without panic?

If the code passes review, end your response with exactly this line on its own:

    <review>PASS</review>

Otherwise list the issues as numbered findings, each with: what is wrong, why
it matters, and how to fix it. End your response with:

    <review>FAIL</review>
EOF
}

coder_prompt() {
    local review="$1"
    local verify
    verify=$(done_verify)
    cat <<EOF
A code reviewer found the following issues with recently committed work. Fix
every finding. Do not introduce new behavior beyond addressing the review.
After fixing, run the verify commands for the done issues and ensure they
pass, then commit with a message summarizing the fixes.

REVIEW FINDINGS:

${review}

VERIFY COMMANDS TO RUN BEFORE COMMITTING:

${verify}

When all findings are addressed and verifies pass, end your response with
exactly this line on its own:

    <fixes>DONE</fixes>

If a finding is wrong or cannot be addressed, explain why and end with:

    <fixes>BLOCKED</fixes>
EOF
}

for ((r = 1; r <= iterations; r++)); do
    base=$(base_for)
    tip=$(tip_sha)

    if [ -n "$base" ] && [ "$base" = "$tip" ]; then
        echo "Nothing to review (tip == base == ${base})."
        exit 0
    fi

    echo "=== Review round $r: ${base:-root}..${tip} ==="

    ts=$(date +%Y%m%d-%H%M%S)
    review_file="docs/reviews/ralph-review-${ts}.md"

    tmpfile=$(mktemp)
    trap "rm -f $tmpfile" EXIT

    prompt=$(reviewer_prompt "$base" "$tip")

    opencode --agent code-reviewer --dangerously-skip-permissions \
        --format json \
        "$prompt" |
        grep --line-buffered '^{' |
        tee "$tmpfile" |
        jq --unbuffered -rj "$stream_text"

    review=$(jq -r "$final_result" "$tmpfile" 2>/dev/null || echo "")
    printf '%s\n' "$review" > "$review_file"
    echo "Review written to $review_file"

    case "$review" in
        *"<review>PASS</review>"*)
            echo "$tip" > "$marker"
            echo "Review PASSED. Marker updated to ${tip}."
            exit 0
            ;;
        *"<review>FAIL</review>"*)
            echo "Review FAILED. Dispatching coder to address findings."
            ;;
        *)
            echo "Reviewer emitted no <review> tag; treating as FAIL."
            ;;
    esac

    coder_tmp=$(mktemp)
    trap "rm -f $tmpfile $coder_tmp" EXIT

    c_prompt=$(coder_prompt "$review")

    opencode --agent python-go-coder --dangerously-skip-permissions \
        --format json \
        "$c_prompt" |
        grep --line-buffered '^{' |
        tee "$coder_tmp" |
        jq --unbuffered -rj "$stream_text"

    coder_result=$(jq -r "$final_result" "$coder_tmp" 2>/dev/null || echo "")

    if ! ./ralph/verify.sh; then
        echo "verify.sh failed after coder round; stopping."
        exit 1
    fi

    case "$coder_result" in
        *"<fixes>DONE</fixes>"*)
            echo "Coder reports fixes done. Looping back to reviewer."
            ;;
        *"<fixes>BLOCKED</fixes>"*)
            echo "Coder is BLOCKED on a finding. Stopping for human review."
            exit 1
            ;;
        *)
            echo "Coder emitted no <fixes> tag. Looping back to reviewer."
            ;;
    esac
done

echo "Reached review iteration cap (${iterations}). Last review: ${review_file}"
exit 1