"""Worker cognitive logic – replays scripted tool calls from transcript.

Also serves as entrypoint: ``python -m limen.mock.worker <transcript_path>``
"""

from __future__ import annotations

import sys

from limen.mock.runtime import Runtime, load_transcript


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


def main() -> None:
    if len(sys.argv) < 2:
        sys.stderr.write("usage: python -m limen.mock.worker <transcript_path>\n")
        sys.exit(2)
    transcript = load_transcript(sys.argv[1])
    rt = Runtime(transcript)
    rt.run_role("worker", worker_fn)


if __name__ == "__main__":
    main()
