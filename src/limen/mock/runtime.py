"""NDJSON stdin/stdout loop, transcript loader, and tool-request callback.

The Runtime owns the bidirectional NDJSON transport. Cognitive functions
receive a Runtime handle so the Worker can emit tool-requests inline during
its run; Router and Validator ignore it (they are one-shot reads).
"""

from __future__ import annotations

import json
import sys
import time


def load_transcript(path: str) -> dict:
    """Load a transcript JSON file from disk."""
    with open(path) as f:
        return json.load(f)


def _now_ms() -> int:
    return int(time.time() * 1000)


class Runtime:
    """Manages the NDJSON loop for a single role invocation.

    Construction accepts override *stdin* and *stdout* so unit tests can
    inject ``io.StringIO`` buffers without touching ``os.pipe`` or spawning
    subprocesses.
    """

    def __init__(
        self,
        transcript: dict,
        *,
        stdin: object = None,
        stdout: object = None,
    ):
        self._transcript = transcript
        self._stdin = stdin if stdin is not None else sys.stdin
        self._stdout = stdout if stdout is not None else sys.stdout
        # Per-role invocation counter (role name -> next index).
        self._indices: dict[str, int] = {}
        # Per-instance tool-request ID counter (used by _synthetic_id).
        self._req_counter: int = 0

    # ------------------------------------------------------------------
    # Transport helpers
    # ------------------------------------------------------------------

    def _read_envelope(self) -> dict | None:
        """Read and parse one NDJSON envelope from stdin.

        Returns *None* on EOF (closed pipe / process exit).
        """
        line = self._stdin.readline()
        if not line:
            return None
        return json.loads(line)

    def _write_envelope(self, env: dict) -> None:
        """Write one NDJSON envelope to stdout."""
        self._stdout.write(json.dumps(env) + "\n")
        self._stdout.flush()

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def request_tool(self, name: str, args: dict) -> object:
        """Emit a ``tool_request``, block until the matching ``tool_response``.

        Returns the ``result`` field of the response.  Raises
        :class:`RuntimeError` if the tool fails or EOF is reached before
        the response arrives.
        """
        req_id = _synthetic_id(self)
        self._write_envelope(
            {
                "kind": "tool_request",
                "tool_request": {"id": req_id, "tool": name, "args": args},
            }
        )
        while True:
            env = self._read_envelope()
            if env is None:
                raise RuntimeError(
                    f"Unexpected EOF while waiting for tool_response to {name!r}"
                )
            if env.get("kind") != "tool_response":
                # NODE: non-response envelopes (e.g. stray events) are ignored;
                # the Go adapter writes exactly one response per request.
                continue
            tr = env.get("tool_response", {})
            if tr.get("id") != req_id:
                continue
            if not tr.get("ok", False):
                raise RuntimeError(
                    f"Tool {name!r} failed: {tr.get('error', 'unknown error')}"
                )
            return tr.get("result")

    def run_role(self, role: str, cognitive_fn) -> None:
        """Execute one invocation of *role*.

        1. Load the Nth transcript entry for *role* (N is per-role monotonic).
        2. Read the incoming request envelope from Go.
        3. Call *cognitive_fn*(runtime, entry, request).
        4. Write the result as an ``event`` envelope.

        Transcript exhaustion writes an error ``event`` and exits the
        process cleanly (exit code 0) so the Go adapter can surface the
        error via the envelope rather than via a nonzero exit.
        """
        idx = self._indices.get(role, 0)
        entries = self._transcript.get(role, [])

        # --- transcript exhaustion ------------------------------------------
        if idx >= len(entries):
            self._write_envelope(
                {
                    "kind": "event",
                    "event": {
                        "type": "error",
                        "task_id": "",
                        "payload": {
                            "error": (
                                f"Transcript exhausted for role {role!r} "
                                f"at index {idx} (total entries: {len(entries)})"
                            )
                        },
                        "timestamp": _now_ms(),
                    },
                }
            )
            sys.exit(0)

        entry = entries[idx]
        self._indices[role] = idx + 1

        # --- read request ---------------------------------------------------
        request_env = self._read_envelope()
        if request_env is None:
            raise RuntimeError(
                f"Unexpected EOF before request envelope for role {role!r}"
            )

        # --- cognitive work -------------------------------------------------
        result = cognitive_fn(self, entry, request_env)

        # --- write result event ---------------------------------------------
        task_id = _extract_task_id(request_env)
        event_type = _event_type_for_role(role)

        self._write_envelope(
            {
                "kind": "event",
                "event": {
                    "type": event_type,
                    "task_id": task_id,
                    "payload": result,
                    "timestamp": _now_ms(),
                },
            }
        )


# ----------------------------------------------------------------------
# Internal helpers
# ----------------------------------------------------------------------

def _synthetic_id(rt: Runtime) -> str:
    """Return a unique-enough ID for a tool request (per-runtime monotonic)."""
    rt._req_counter += 1
    return f"mock-{rt._req_counter}"


def _extract_task_id(request_env: dict) -> str:
    """Best-effort extraction of task_id from the Go request envelope."""
    if not isinstance(request_env, dict):
        return ""
    # Request shape per PRD:  {"task": {"id": ..., ...}, "attempt": N}
    task = request_env.get("task") or {}
    return task.get("id", "")


def _event_type_for_role(role: str) -> str:
    return {
        "router": "router.decision",
        "worker": "worker.finished",
        "validator": "validator.verdict",
    }.get(role, f"{role}.finished")
