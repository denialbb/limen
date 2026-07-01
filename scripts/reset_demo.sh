#!/bin/bash

# Reset the demo repository and clean up old databases.
# Run from the project root: ./scripts/reset_demo.sh

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/.."

if [ -d "demo" ]; then
	rm -rf demo
	echo "Demo repository reset successfully"
else
	echo "No demo repository found to reset"
fi

# Delete SQLite databases and WAL/SHM side-files left in the project root.
for db in limen.db .demo*.db; do
	[ -f "$db" ] && rm "$db" && echo "Deleted: $db"
done
for wal in limen.db-wal limen.db-shm .demo*.db-wal .demo*.db-shm; do
	[ -f "$wal" ] && rm "$wal" && echo "Deleted: $wal"
done
