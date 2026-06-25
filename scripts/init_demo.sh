#!/usr/bin/env bash
set -e

echo "Initializing demo environment..."

mkdir -p demo
cd demo

if [ ! -d ".git" ]; then
    git init
    git config user.email "demo@example.com"
    git config user.name "Demo User"
    
    echo ".mock-db.sqlite" > .gitignore
    echo "mock_state.json" >> .gitignore
    echo "limen.db*" >> .gitignore
    
    git add .gitignore
    git commit -m "Initial commit with gitignore"
    echo "Git repo initialized."
fi

# Initialize mock database
touch .mock-db.sqlite
echo "{}" > mock_state.json

echo "Demo environment ready."
