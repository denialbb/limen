"""Router cognitive logic – pure function, testable without subprocess."""

from __future__ import annotations


def router_fn(runtime, entry: dict, request: dict) -> dict:
    """Return the router transcript entry as the result payload.

    *runtime* is ignored (router makes no tool calls).
    *entry* is the Nth transcript entry for the "router" role.
    *request* is the raw Go request envelope.
    """
    # NODE: the transcript entry IS the result payload (decision + rationale).
    return dict(entry)
