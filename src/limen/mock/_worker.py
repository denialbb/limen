"""Worker cognitive logic – replays scripted tool calls from transcript."""

from __future__ import annotations


def worker_fn(runtime, entry: dict, request: dict) -> dict:
    """Execute scripted tool calls, then return the worker result.

    *runtime* provides ``request_tool(name, args)`` which blocks until
    Go responds.  The worker replays every ``tool_calls[]`` entry from
    the transcript in order, then returns ``result`` as the payload.

    *entry* is the Nth transcript entry for the "worker" role.
    *request* is the raw Go request envelope.
    """
    for tc in entry.get("tool_calls", []):
        runtime.request_tool(tc["name"], tc["args"])
    return dict(entry.get("result", {}))
