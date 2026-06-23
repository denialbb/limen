"""Worker cognitive logic – pure function, testable without subprocess.

Signature: ``worker_fn(entry: dict, request: dict) -> dict``

The runtime handles the bidirectional tool-call loop (iterating
``entry["tool_calls"]``) before calling this function, so the
cognitive function stays pure and testable with plain dicts.

Also serves as entrypoint: ``python -m limen.mock.worker <transcript_path>``
"""

from __future__ import annotations

import sys

from limen.mock.runtime import (
    Runtime,
    _DEFAULT_TRANSCRIPT,
    load_transcript,
)


def worker_fn(entry: dict, request: dict) -> dict:
    """Return the worker result payload.

    *entry* is the Nth transcript entry for the "worker" role.
    *request* is the raw Go request envelope (ignored by the mock).
    """
    return dict(entry.get("result", {}))


def main() -> None:
    path = sys.argv[1] if len(sys.argv) > 1 else _DEFAULT_TRANSCRIPT
    transcript = load_transcript(path)
    rt = Runtime(transcript)
    rt.run_role("worker", worker_fn)


if __name__ == "__main__":
    main()
