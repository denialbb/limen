#!/usr/bin/env python3
"""HOME-scoped agy config builder for the breadcrumb spike (slice 2).

agy discovers MCP servers ONLY from HOME-derived globals
(``~/.gemini/config/mcp_config.json``) or from enabled plugins — there is no
repo-local or ``--mcp-config`` path (verified against the installed agy binary's
embedded docs). So the *only* safe way to load a limen-owned MCP server for a
single spike run, WITHOUT writing to the user's real ``~/.gemini``, is to run
agy with ``HOME`` pointed at a throwaway directory that this module populates.

This module:

- Writes ``<root>/.gemini/config/mcp_config.json`` (agy's schema:
  ``{"mcpServers": {id: {command, args, env}}}``) under a caller-owned tmp root.
- Returns a *new* env dict with ``HOME`` set to that root (it NEVER mutates
  ``os.environ`` and NEVER touches the real ``~/.gemini``).
- Provides ``snapshot_tree`` for the no-pollution test to assert the real
  ``~/.gemini`` is byte-for-byte untouched — metadata only (path/size/mtime), so
  it never reads the user's OAuth credential contents.

Hard constraint (issues/spike-agy-mcp-empirical.md): NEVER write to the real
``~/.gemini/config``. Everything here writes strictly under the caller's root.
"""
import json
import os
import sys

SERVER_ID = "limen-breadcrumb-spike"
BREADCRUMB_LOG_ENV = "LIMEN_BREADCRUMB_LOG"
DIAG_ENV = "LIMEN_SPIKE_DIAG"


def mcp_config(server_path, log_path, python=None, diag_path=None):
    """Return agy's mcp_config.json content as a dict for our stdio server.

    The per-server ``env`` block is how the spawned server learns where to write
    its breadcrumb log and (optionally) its lifecycle diagnostics — agy injects
    these into the server subprocess it launches."""
    env = {BREADCRUMB_LOG_ENV: os.path.abspath(log_path)}
    if diag_path:
        env[DIAG_ENV] = os.path.abspath(diag_path)
    return {
        "mcpServers": {
            SERVER_ID: {
                "command": python or sys.executable,
                "args": [os.path.abspath(server_path)],
                "env": env,
            }
        }
    }


def build_scoped_home(root, server_path, log_path, base_env=None, python=None,
                      diag_path=None):
    """Populate ``<root>/.gemini/config/mcp_config.json`` and return the env to
    run agy with. Does not mutate ``os.environ`` or the real ``~/.gemini``.

    Returns ``(env, config_path)``.
    """
    root = os.path.abspath(root)
    config_dir = os.path.join(root, ".gemini", "config")
    os.makedirs(config_dir, exist_ok=True)
    config_path = os.path.join(config_dir, "mcp_config.json")
    with open(config_path, "w", encoding="utf-8") as fh:
        json.dump(mcp_config(server_path, log_path, python=python,
                             diag_path=diag_path), fh, indent=2)

    env = dict(base_env if base_env is not None else os.environ)
    env["HOME"] = root
    # Some tools also honor XDG_CONFIG_HOME; keep it consistent with the scoped
    # HOME so nothing leaks back to the real config dir.
    env["XDG_CONFIG_HOME"] = os.path.join(root, ".config")
    env[BREADCRUMB_LOG_ENV] = os.path.abspath(log_path)
    if diag_path:
        env[DIAG_ENV] = os.path.abspath(diag_path)
    return env, config_path


def snapshot_tree(path):
    """Metadata-only snapshot of a directory tree: {relpath: (size, mtime_ns)}.

    Never opens file contents, so it never reads OAuth credentials. Returns {}
    if the path does not exist (e.g. CI with no ~/.gemini)."""
    snap = {}
    if not os.path.exists(path):
        return snap
    for dirpath, dirnames, filenames in os.walk(path):
        for name in filenames:
            fp = os.path.join(dirpath, name)
            try:
                st = os.lstat(fp)
            except OSError:
                continue
            snap[os.path.relpath(fp, path)] = (st.st_size, st.st_mtime_ns)
    return snap
