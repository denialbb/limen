"""Worker cognitive logic – pure function, testable without subprocess.

Signature: ``worker_fn(entry: dict, request: dict) -> dict``

The runtime handles the bidirectional tool-call loop (iterating
``entry["tool_calls"]``) before calling this function, so the
cognitive function stays pure and testable with plain dicts.

Also serves as entrypoint: ``python -m limen.mock.worker <transcript_path>``
"""

from __future__ import annotations

import sys

from limen.mock.runtime import Runtime, load_transcript


def worker_fn(entry: dict, request: dict) -> dict:
    """Return the worker result payload.

    *entry* is the Nth transcript entry for the "worker" role.
    *request* is the raw Go request envelope (ignored by the mock).
    """
    return dict(entry.get("result", {}))


def main() -> None:
    if len(sys.argv) < 2:
        sys.stderr.write("usage: python -m limen.mock.worker <transcript_path>\n")
        sys.exit(2)
    transcript = load_transcript(sys.argv[1])
    rt = Runtime(transcript)
    rt.run_role("worker", worker_fn)


if __name__ == "__main__":
    main()
