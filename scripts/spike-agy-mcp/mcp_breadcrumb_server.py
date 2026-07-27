#!/usr/bin/env python3
"""Throwaway MCP stdio server for the agy breadcrumb spike.

Exposes ONE tool, ``limen_breadcrumb(message)``, which appends a single
``<iso8601-ts>\\t<message>`` line to the log file named by the
``LIMEN_BREADCRUMB_LOG`` environment variable.

Design constraints (issues/spike-agy-mcp-empirical.md, design_principles.md):

- **Stateless adapter / No Hidden State in MCP.** The server keeps NO task
  state, NO cache, NO routing, NO in-memory accumulation. Every ``tools/call``
  opens the log, appends one line, flushes, closes. Two calls are fully
  independent; a fresh process appending to the same log behaves identically to
  the same process making two calls.
- **Zero dependencies.** Pure stdlib newline-delimited JSON-RPC 2.0 over stdio,
  the MCP stdio transport framing (one JSON message per line, no embedded
  newlines). This avoids adding an SDK dependency for a throwaway spike.

This is NOT wired into cmd/limen, internal/remote, or the orchestrator.
"""
import datetime
import json
import os
import sys

TOOL_NAME = "limen_breadcrumb"
LOG_ENV = "LIMEN_BREADCRUMB_LOG"
# Advertised only for negotiation; we echo the client's requested version when
# present so we interoperate with whatever protocol revision agy speaks.
DEFAULT_PROTOCOL_VERSION = "2025-06-18"


def record_breadcrumb(log_path, message, now=None):
    """Append one ``<iso8601-ts>\\t<message>`` line to ``log_path``.

    Pure, stateless side effect: open-append-close. Returns the line written
    (without the trailing newline) so callers/tests can assert on it. ``now`` is
    injectable for deterministic tests; defaults to UTC wall-clock.
    """
    ts = (now or datetime.datetime.now(datetime.timezone.utc)).isoformat()
    line = f"{ts}\t{message}"
    with open(log_path, "a", encoding="utf-8") as fh:
        fh.write(line + "\n")
        fh.flush()
    return line


def _tool_definition():
    return {
        "name": TOOL_NAME,
        "description": (
            "Report a short progress breadcrumb / status update about what you "
            "are currently doing. Call this whenever you start a distinct step "
            "so progress can be observed."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "message": {
                    "type": "string",
                    "description": "A short human-readable status message.",
                }
            },
            "required": ["message"],
        },
    }


def handle_request(msg, log_path):
    """Map one decoded JSON-RPC request to a response dict, or None for a
    notification (no id -> no reply). Pure w.r.t. the message; the only side
    effect is the breadcrumb append inside tools/call."""
    method = msg.get("method")
    msg_id = msg.get("id")

    # Notifications (no id) get no response.
    if msg_id is None:
        return None

    if method == "initialize":
        client_ver = (msg.get("params") or {}).get("protocolVersion")
        return _result(msg_id, {
            "protocolVersion": client_ver or DEFAULT_PROTOCOL_VERSION,
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "limen-breadcrumb-spike", "version": "0.1.0"},
        })

    if method == "ping":
        return _result(msg_id, {})

    if method == "tools/list":
        return _result(msg_id, {"tools": [_tool_definition()]})

    if method == "tools/call":
        params = msg.get("params") or {}
        if params.get("name") != TOOL_NAME:
            return _error(msg_id, -32602, f"unknown tool {params.get('name')!r}")
        message = (params.get("arguments") or {}).get("message")
        if not isinstance(message, str):
            return _error(msg_id, -32602, "missing string argument 'message'")
        record_breadcrumb(log_path, message)
        return _result(msg_id, {
            "content": [{"type": "text", "text": "recorded"}],
            "isError": False,
        })

    return _error(msg_id, -32601, f"method not found: {method}")


def _result(msg_id, result):
    return {"jsonrpc": "2.0", "id": msg_id, "result": result}


def _error(msg_id, code, message):
    return {"jsonrpc": "2.0", "id": msg_id, "error": {"code": code, "message": message}}


def serve(stdin, stdout, log_path):
    """Run the newline-delimited JSON-RPC loop until stdin EOF."""
    for raw in stdin:
        raw = raw.strip()
        if not raw:
            continue
        try:
            msg = json.loads(raw)
        except json.JSONDecodeError:
            continue
        resp = handle_request(msg, log_path)
        if resp is not None:
            stdout.write(json.dumps(resp) + "\n")
            stdout.flush()


def main():
    log_path = os.environ.get(LOG_ENV)
    if not log_path:
        sys.stderr.write(f"{LOG_ENV} is required\n")
        return 2
    serve(sys.stdin, sys.stdout, log_path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
