"""Validator cognitive logic – pure function, testable without subprocess."""

from __future__ import annotations


def validator_fn(runtime, entry: dict, request: dict) -> dict:
    """Return the validator transcript entry as the result payload.

    *runtime* is ignored (validator makes no tool calls).
    *entry* is the Nth transcript entry for the "validator" role.
    *request* is the raw Go request envelope.
    """
    return dict(entry)
