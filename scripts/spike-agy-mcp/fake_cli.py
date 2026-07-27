#!/usr/bin/env python3
"""Fake coding-agent CLI for the breadcrumb spike (slice 1).

Stands in for ``agy --print`` with a *cooperative* agent: it connects to the
limen breadcrumb MCP server, then during its "run" calls ``limen_breadcrumb``
exactly N times in order before exiting. Its only purpose is to prove the
plumbing (server + client + recorder) end-to-end WITHOUT the real model, so that
in slice 3 the only remaining unknown is genuine model behavior.

This deliberately makes the tool call unavoidable (unlike a real model, which
chooses) — it bounds nothing about model behavior; it only validates transport.

Usage:  fake_cli.py <server_path> <n> [message_prefix]
Env:    LIMEN_BREADCRUMB_LOG  (passed through to the spawned server)
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from mcp_client import MCPStdioClient  # noqa: E402
from mcp_breadcrumb_server import TOOL_NAME  # noqa: E402


def main(argv):
    if len(argv) < 3:
        sys.stderr.write("usage: fake_cli.py <server_path> <n> [prefix]\n")
        return 2
    server_path = argv[1]
    n = int(argv[2])
    prefix = argv[3] if len(argv) > 3 else "step"

    env = dict(os.environ)  # carries LIMEN_BREADCRUMB_LOG to the server
    with MCPStdioClient([sys.executable, server_path], env=env) as c:
        c.initialize()
        # Confirm the tool is discoverable, exactly as a real agent would.
        names = [t["name"] for t in c.list_tools()]
        if TOOL_NAME not in names:
            sys.stderr.write(f"tool {TOOL_NAME} not offered; saw {names}\n")
            return 1
        for i in range(n):
            c.call_tool(TOOL_NAME, {"message": f"{prefix}-{i}"})
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
