#!/usr/bin/env bash
set -e

REPO_PATH="${1:-/tmp/test-repo}"

echo "Resetting test repo at $REPO_PATH"

# Remove old repo
rm -rf "$REPO_PATH"
mkdir -p "$REPO_PATH"
cd "$REPO_PATH"

# Init git
git init
git config user.email "test@example.com"
git config user.name "Test User"

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

# Initial commit
git add -A
git commit -m "Initial commit with buggy add function"

# Clean limen artifacts
rm -f limen.db limen.db-shm limen.db-wal
rm -rf .limen

echo "✓ Test repo ready at $REPO_PATH"
echo "Run: ./limen --task-id test-fix-add --prompt '...' --repo-path $REPO_PATH --mock=false --worker-backend pi --validator-cmd 'go test ./...'"
