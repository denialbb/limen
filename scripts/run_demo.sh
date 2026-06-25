#!/usr/bin/env bash
set -e

echo "Building limen..."
go build -o limen ./cmd/limen

echo "Clearing old demo..."
rm -rf demo

echo "Initializing new demo..."
./scripts/init_demo.sh

cd demo

TASK_ID="spike-demo-$(date +%s)"
echo "Starting TUI demo with task: $TASK_ID"

LIMEN_MOCK_DELAY=1.5 PYTHONPATH=../src ../limen \
    --task-id "$TASK_ID" \
    --mock \
    --mock-transcript ../src/limen/mock/transcripts/spike.json
