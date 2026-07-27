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
import scope  # noqa: E402

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


# --- Slice 2: HOME-scoped config + no-pollution -----------------------------

def test_build_scoped_home_writes_only_under_root(tmp_path):
    root = tmp_path / "scoped-home"
    log = tmp_path / "bc.log"
    env, config_path = scope.build_scoped_home(str(root), SERVER, str(log))

    # Config landed under the scoped HOME with agy's schema.
    assert config_path == str(root / ".gemini" / "config" / "mcp_config.json")
    assert os.path.exists(config_path)
    cfg = __import__("json").load(open(config_path))
    entry = cfg["mcpServers"][scope.SERVER_ID]
    assert entry["args"] == [SERVER]
    assert entry["env"][scope.BREADCRUMB_LOG_ENV] == str(log)

    # The returned env re-homes agy into the tmp root.
    assert env["HOME"] == str(root)
    assert env["HOME"] != os.path.expanduser("~")


def test_real_gemini_is_never_touched(tmp_path):
    """Non-negotiable: building a scoped home must not create, delete, or modify
    anything under the user's real ~/.gemini (metadata snapshot, before/after)."""
    real_gemini = os.path.expanduser("~/.gemini")
    before = scope.snapshot_tree(real_gemini)
    real_existed = os.path.exists(real_gemini)

    root = tmp_path / "scoped-home"
    log = tmp_path / "bc.log"
    scope.build_scoped_home(str(root), SERVER, str(log))

    after = scope.snapshot_tree(real_gemini)
    assert after == before, "real ~/.gemini was mutated by the spike harness"
    # And building must not have conjured a real ~/.gemini where none existed.
    assert os.path.exists(real_gemini) == real_existed

    # build_scoped_home must not mutate the live process environment either.
    assert os.environ.get("HOME") != str(root)


def test_diag_captures_server_lifecycle(tmp_path):
    """The lifecycle diagnostics sink must record start -> initialize ->
    tools/list -> tools/call, so the real run can distinguish 'server loaded but
    model never called it' from 'server never loaded'."""
    log = tmp_path / "bc.log"
    diag = tmp_path / "diag.log"
    env = _server_env(log)
    env[scope.DIAG_ENV] = str(diag)
    with MCPStdioClient([sys.executable, SERVER], env=env) as c:
        c.initialize()
        c.list_tools()
        c.call_tool(TOOL_NAME, {"message": "hi"})
    events = [ln.split("\t")[1] for ln in _read_lines(str(diag))]
    assert events == ["start", "initialize", "tools/list", "tools/call"]


# --- Slice 3: gated real run -------------------------------------------------

RUNNER = os.path.join(HERE, "run_trials.py")


def test_run_trials_is_gated_off_by_default(tmp_path):
    """Without LIMEN_SPIKE_REAL_AGY=1 the runner must be a no-op and must NOT
    invoke agy (keeps CI/token-free). It should return fast with a gated notice."""
    env = dict(os.environ)
    env.pop("LIMEN_SPIKE_REAL_AGY", None)
    proc = subprocess.run(
        [sys.executable, RUNNER, "--trials", "1"],
        env=env, capture_output=True, text=True, timeout=20,
    )
    assert proc.returncode == 0
    assert "[gated]" in proc.stdout


@pytest.mark.skipif(os.environ.get("LIMEN_SPIKE_REAL_AGY") != "1",
                    reason="real agy run; set LIMEN_SPIKE_REAL_AGY=1")
def test_real_agy_single_trial_loads_server(tmp_path):
    """Sanity: one gated aligned trial must at least LOAD the server (proves the
    real-agy plumbing); call count is what the full matrix measures."""
    import run_trials
    r = run_trials.run_trial("aligned", 0)
    assert r["server_loaded"], r
    assert r["tools_listed"], r


def test_build_scoped_home_does_not_mutate_os_environ(tmp_path):
    sentinel_before = dict(os.environ)
    root = tmp_path / "scoped-home"
    scope.build_scoped_home(str(root), SERVER, str(tmp_path / "bc.log"))
    assert dict(os.environ) == sentinel_before
