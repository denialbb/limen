"""Router cognitive logic – pure function, testable without subprocess.

Signature: ``router_fn(entry: dict, request: dict) -> dict``

Also serves as entrypoint: ``python -m limen.mock.router <transcript_path>``
"""

from __future__ import annotations

import sys

from limen.mock.runtime import Runtime, load_transcript


def router_fn(entry: dict, request: dict) -> dict:
    """Return the router transcript entry as the result payload.

    *entry* is the Nth transcript entry for the "router" role.
    *request* is the raw Go request envelope (ignored by the mock).
    """
    return dict(entry)


def main() -> None:
    if len(sys.argv) < 2:
        sys.stderr.write("usage: python -m limen.mock.router <transcript_path>\n")
        sys.exit(2)
    transcript = load_transcript(sys.argv[1])
    rt = Runtime(transcript)
    rt.run_role("router", router_fn)


if __name__ == "__main__":
    main()
