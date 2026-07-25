#!/usr/bin/env bash
# Scaffold a throwaway fixture repo with a buggy "add" function and a failing
# test that pins the correct behavior. Used by the TUI e2e runs.
#
# Usage: reset-test-repo.sh [repo-path] [lang]
#   repo-path   default: /tmp/test-repo
#   lang        go (default) | python
#
# go     -> main.go + main_test.go, validated with `go test ./...`
# python -> calc.py + test_calc.py, validated with `python -m pytest -x`
set -e

REPO_PATH="${1:-/tmp/test-repo}"
LANG_KIND="${2:-go}"

echo "Resetting $LANG_KIND test repo at $REPO_PATH"

# Remove old repo
rm -rf "$REPO_PATH"
mkdir -p "$REPO_PATH"
cd "$REPO_PATH"

# Init git
git init
git config user.email "test@example.com"
git config user.name "Test User"

case "$LANG_KIND" in
go)
    # Create buggy main.go
    cat > main.go << 'EOF'
package main

import "fmt"

func main() {
	result := add(2, 3)
	fmt.Println(result)
}

// BUG: This function subtracts instead of adds
func add(a, b int) int {
	return a - b
}
EOF

    # Create test file
    cat > main_test.go << 'EOF'
package main

import "testing"

func TestAdd(t *testing.T) {
	result := add(2, 3)
	if result != 5 {
		t.Errorf("Expected 5, got %d", result)
	}
}
EOF

    # Go module
    go mod init example.com/test-repo

    COMMIT_MSG="Initial commit with buggy add function"
    RUN_HINT="./limen --task-id test-fix-add --prompt '...' --repo-path $REPO_PATH --mock=false --worker-backend pi --validator-cmd 'go test ./...'"
    ;;
python)
    # Buggy implementation: subtracts instead of adding.
    cat > calc.py << 'EOF'
def add(a, b):
    # BUG: subtracts instead of adding
    return a - b
EOF

    # pytest test that pins the correct behavior.
    cat > test_calc.py << 'EOF'
from calc import add


def test_add():
    assert add(2, 3) == 5
EOF

    COMMIT_MSG="Initial commit with buggy add function (python)"
    RUN_HINT="./limen --task-id test-fix-add-py --prompt '...' --repo-path $REPO_PATH --mock=false --worker-backend pi --validator-cmd 'python -m pytest -x'"
    ;;
*)
    echo "reset-test-repo: unknown lang '$LANG_KIND' (want: go | python)" >&2
    exit 2
    ;;
esac

# Initial commit
git add -A
git commit -m "$COMMIT_MSG"

# Clean limen artifacts
rm -f limen.db limen.db-shm limen.db-wal
rm -rf .limen

echo "✓ $LANG_KIND test repo ready at $REPO_PATH"
echo "Run: $RUN_HINT"
