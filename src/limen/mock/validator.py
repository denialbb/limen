"""Validator cognitive logic – pure function, testable without subprocess.

Also serves as entrypoint: ``python -m limen.mock.validator <transcript_path>``
"""

from __future__ import annotations

import sys

from limen.mock.runtime import Runtime, load_transcript


def validator_fn(runtime, entry: dict, request: dict) -> dict:
    """Return the validator transcript entry as the result payload.

    *runtime* is ignored (validator makes no tool calls).
    *entry* is the Nth transcript entry for the "validator" role.
    *request* is the raw Go request envelope.
    """
    return dict(entry)


def main() -> None:
    if len(sys.argv) < 2:
        sys.stderr.write("usage: python -m limen.mock.validator <transcript_path>\n")
        sys.exit(2)
    transcript = load_transcript(sys.argv[1])
    rt = Runtime(transcript)
    rt.run_role("validator", validator_fn)


if __name__ == "__main__":
    main()
