"""Tests for the agy MCP breadcrumb spike harness.

CI-safe: everything here runs without agy and without network. The single live
``agy --print`` test is gated behind ``LIMEN_SPIKE_REAL_AGY=1`` (mirrors the
repo's ``LIMEN_E2E_REAL_AGENTS=1`` convention) and is skipped otherwise.

Run:  python3 -m pytest scripts/spike-agy-mcp/test_spike.py
"""
import os
import re
import subprocess
import sys

import pytest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from mcp_breadcrumb_server import record_breadcrumb, LOG_ENV, TOOL_NAME  # noqa: E402
from mcp_client import MCPStdioClient  # noqa: E402

SERVER = os.path.join(HERE, "mcp_breadcrumb_server.py")
FAKE_CLI = os.path.join(HERE, "fake_cli.py")
ISO_LINE = re.compile(r"^\d{4}-\d{2}-\d{2}T[\d:.+\-]+\t(.*)$")


def _server_env(log_path):
    env = dict(os.environ)
    env[LOG_ENV] = str(log_path)
    return env


def _read_lines(log_path):
    if not os.path.exists(log_path):
        return []
    with open(log_path, encoding="utf-8") as fh:
        return [ln.rstrip("\n") for ln in fh if ln.strip()]


# --- Slice 0: breadcrumb recorder -------------------------------------------

def test_record_breadcrumb_line_format(tmp_path):
    log = tmp_path / "bc.log"
    line = record_breadcrumb(str(log), "editing calc.py")
    m = ISO_LINE.match(line)
    assert m, f"line not in <iso>\\t<message> form: {line!r}"
    assert m.group(1) == "editing calc.py"
    assert _read_lines(str(log)) == [line]


def test_tool_call_over_mcp_records_line(tmp_path):
    """Drive the tool the way agy would: initialize, then tools/call."""
    log = tmp_path / "bc.log"
    with MCPStdioClient([sys.executable, SERVER], env=_server_env(log)) as c:
        c.initialize()
        tools = c.list_tools()
        assert [t["name"] for t in tools] == [TOOL_NAME]
        c.call_tool(TOOL_NAME, {"message": "ran the tests"})
    lines = _read_lines(str(log))
    assert len(lines) == 1
    assert ISO_LINE.match(lines[0]).group(1) == "ran the tests"


def test_stateless_two_calls_independent(tmp_path):
    """No Hidden State in MCP: two unrelated calls are independent and ordered,
    and a FRESH server process appending to the same log behaves identically to
    one process making both calls (proves no in-memory accumulation)."""
    log = tmp_path / "bc.log"
    # Call 1 in one process...
    with MCPStdioClient([sys.executable, SERVER], env=_server_env(log)) as c:
        c.initialize()
        c.call_tool(TOOL_NAME, {"message": "first"})
    # ...call 2 in a brand-new process against the same log.
    with MCPStdioClient([sys.executable, SERVER], env=_server_env(log)) as c:
        c.initialize()
        c.call_tool(TOOL_NAME, {"message": "second"})
    msgs = [ISO_LINE.match(ln).group(1) for ln in _read_lines(str(log))]
    assert msgs == ["first", "second"]


# --- Slice 1: fake-CLI harness (plumbing proof) -----------------------------

@pytest.mark.parametrize("n", [1, 3, 7])
def test_fake_cli_records_exactly_n_in_order(tmp_path, n):
    """A cooperative fake agent calling the tool N times mid-run must yield
    exactly N recorded breadcrumbs, in order. Proves server+client+recorder end
    to end so the real run's only unknown is model behavior."""
    log = tmp_path / "bc.log"
    proc = subprocess.run(
        [sys.executable, FAKE_CLI, SERVER, str(n), "step"],
        env=_server_env(log), capture_output=True, text=True, timeout=30,
    )
    assert proc.returncode == 0, proc.stderr
    msgs = [ISO_LINE.match(ln).group(1) for ln in _read_lines(str(log))]
    assert msgs == [f"step-{i}" for i in range(n)]
