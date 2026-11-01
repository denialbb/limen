#!/usr/bin/env bash
# Regression test for scripts/tui-wait.sh pipefail/SIGPIPE race.
#
# A capture script that outputs a match on line 1 followed by >100 KiB of data
# must not trigger SIGPIPE (exit 141) and cause tui-wait.sh to miss the terminal
# match under pipefail.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

TMPDIR="$(mktemp -d "${TMPDIR:-/tmp}/test-tui-wait.XXXXXX")"
trap 'rm -rf "$TMPDIR"' EXIT

FAKE_CAPTURE="$TMPDIR/fake-capture.sh"
cat << 'EOF' > "$FAKE_CAPTURE"
#!/usr/bin/env bash
set -e
echo "COMMITTED"
# Emit >100 KiB to overflow default pipe buffers (64 KiB) and reliably trigger SIGPIPE if read end closes early
head -c 120000 /dev/zero | tr '\0' 'x'
echo
EOF
chmod +x "$FAKE_CAPTURE"

echo "== running test-tui-wait regression =="
# Should succeed on first poll (<1s); timeout set to 3s.
if TUI_CAPTURE_CMD="$FAKE_CAPTURE" TUI_WAIT_INTERVAL=1 ./scripts/tui-wait.sh dummy-session --timeout 3; then
    echo "PASS: test-tui-wait completed without SIGPIPE timeout"
    exit 0
else
    rc=$?
    echo "FAIL: test-tui-wait failed with exit code $rc (expected 0)" >&2
    exit "$rc"
fi
