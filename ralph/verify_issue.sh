#!/usr/bin/env bash

# Structure Issues liKe:
# # Add OAuth callback handler
# ## Verify
# python tests/test_oauth.py
# go test ./auth -run TestOAuthCallback

set -e

issue_file="$1"

verify_cmd=$(awk '
  $0 == "## Verify" {flag=1; next}
  flag && /^## / {exit}
  flag && NF {print}
' "$issue_file")

echo "Running: $verify_cmd"
bash -lc "$verify_cmd"
