"""Entrypoint: ``python -m limen.mock.validator <transcript_path>``"""

import sys

from limen.mock.runtime import Runtime, load_transcript
from limen.mock._validator import validator_fn


def main() -> None:
    if len(sys.argv) < 2:
        sys.stderr.write("usage: python -m limen.mock.validator <transcript_path>\n")
        sys.exit(2)
    transcript = load_transcript(sys.argv[1])
    rt = Runtime(transcript)
    rt.run_role("validator", validator_fn)


if __name__ == "__main__":
    main()
