#!/bin/bash

# Reset the demo repository and clean up old databases

# Navigate to the project root
cd /home/denial/Projects/limen

# Reset the demo repository
if [ -d ".demo-repo" ]; then
	rm -rf .demo-repo
	echo "Demo repository reset successfully"
else
	echo "No demo repository found to reset"
fi

# Delete old databases
for db in .demo*.db; do
	if [ -f "$db" ]; then
		rm "$db"
		echo "Deleted database: $db"
	fi
done

# Delete SQLite WAL files
for wal in .demo*.db-wal; do
	if [ -f "$wal" ]; then
		rm "$wal"
		echo "Deleted WAL file: $wal"
	fi
done

# Delete SQLite SHM files
for shm in .demo*.db-shm; do
	if [ -f "$shm" ]; then
		rm "$shm"
		echo "Deleted SHM file: $shm"
	fi
done
