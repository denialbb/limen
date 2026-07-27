#!/usr/bin/env python3
"""Minimal MCP stdio client for the breadcrumb spike.

Zero-dependency newline-delimited JSON-RPC 2.0 client that spawns an MCP stdio
server, performs the ``initialize`` handshake, and drives ``tools/list`` /
``tools/call``. Used by the fake-CLI harness (slice 1) to prove the plumbing
end-to-end without agy, and reusable as a sanity check that our own server
speaks the protocol agy expects.
"""
import json
import subprocess


class MCPStdioClient:
    def __init__(self, argv, env=None, cwd=None):
        self._proc = subprocess.Popen(
            argv,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            env=env,
            cwd=cwd,
            bufsize=1,
        )
        self._id = 0

    def _next_id(self):
        self._id += 1
        return self._id

    def _send(self, obj):
        self._proc.stdin.write(json.dumps(obj) + "\n")
        self._proc.stdin.flush()

    def _request(self, method, params=None):
        msg_id = self._next_id()
        self._send({"jsonrpc": "2.0", "id": msg_id, "method": method,
                    "params": params or {}})
        # Read until we get the matching id (server may interleave nothing else
        # here, but be robust to notifications).
        while True:
            line = self._proc.stdout.readline()
            if not line:
                raise RuntimeError(f"server closed stdout awaiting {method}")
            resp = json.loads(line)
            if resp.get("id") == msg_id:
                if "error" in resp:
                    raise RuntimeError(f"{method} error: {resp['error']}")
                return resp.get("result")

    def _notify(self, method, params=None):
        self._send({"jsonrpc": "2.0", "method": method, "params": params or {}})

    def initialize(self, protocol_version="2025-06-18"):
        result = self._request("initialize", {
            "protocolVersion": protocol_version,
            "capabilities": {},
            "clientInfo": {"name": "spike-fake-cli", "version": "0.1.0"},
        })
        self._notify("notifications/initialized")
        return result

    def list_tools(self):
        return self._request("tools/list").get("tools", [])

    def call_tool(self, name, arguments):
        return self._request("tools/call", {"name": name, "arguments": arguments})

    def close(self):
        try:
            self._proc.stdin.close()
        except Exception:
            pass
        try:
            self._proc.wait(timeout=5)
        except Exception:
            self._proc.kill()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
