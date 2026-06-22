#!/bin/bash
set -e

echo "Building limen..."
go build -o bin/limen ./cmd/limen

SANDBOX_DIR="sandbox_repo"

echo "Setting up sandbox repository in ./$SANDBOX_DIR..."
rm -rf "$SANDBOX_DIR"
mkdir -p "$SANDBOX_DIR"
cd "$SANDBOX_DIR"

# Initialize a git repository with an initial commit
git init -b main
echo "Initial content" > main.txt
echo "limen.db*" > .gitignore
echo ".limen/" >> .gitignore
git add main.txt .gitignore
git commit -m "Initial commit"

echo "------------------------------------------------"
echo "Running limen on the sandbox repository..."
echo "------------------------------------------------"

# Run the limen orchestrator targeting this sandbox
../bin/limen run-task --task-id "sandbox-task-1" --repo-path "." --db-path "limen.db"

echo "------------------------------------------------"
echo "Sandbox execution complete!"
echo "You can inspect the dummy commit limen generated:"
echo "git -C $SANDBOX_DIR log -n 2"
