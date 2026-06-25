"""NDJSON stdin/stdout loop, transcript loader, and tool-request callback.

The Runtime owns the bidirectional NDJSON transport and the tool-call
loop.  Cognitive functions are pure ``(entry, request) -> result``
callables so they can be unit-tested with plain dicts — no Runtime,
no subprocess needed.
"""

from __future__ import annotations

import json
import os
import sys
import time

# Default transcript path used by entrypoints when no argument is given.
# Relative to the repo root (the Go side always launches from there).
_DEFAULT_TRANSCRIPT = os.path.join(
    os.path.dirname(__file__), "transcripts", "spike.json"
)


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

        1. Read the incoming request envelope from Go.
        2. Load the Nth transcript entry for *role* based on the ``attempt``
           field from the request (attempt is 1-based; index = attempt - 1).
        3. For ``"worker"``: replay every ``tool_calls[]`` entry via
           ``request_tool``, blocking on each response, *before* calling
           the cognitive function.  For ``"router"`` / ``"validator"``:
           no tool calls (one-shot reads).
        4. Call *cognitive_fn*(entry, request) — a pure 2-argument function
           that returns the result payload.
        5. Write the result as an ``event`` envelope.

        Transcript exhaustion writes an error ``event`` and exits the
        process cleanly (exit code 0) so the Go adapter can surface the
        error via the envelope rather than via a nonzero exit.
        """
        entries = self._transcript.get(role, [])

        # --- read request first to get attempt number -----------------------
        request_env = self._read_envelope()
        if request_env is None:
            # NODE: design principle — errors surface via envelopes, not
            # exit codes. Go sees an error event and acts accordingly.
            self._write_envelope(
                {
                    "kind": "event",
                    "event": {
                        "type": "error",
                        "task_id": "",
                        "payload": {
                            "error": (
                                f"Unexpected EOF before request envelope "
                                f"for role {role!r}"
                            )
                        },
                        "timestamp": _now_ms(),
                    },
                }
            )
            sys.exit(0)

        # NODE: Use the attempt field from the Go request as the transcript
        # index.  Each orchestrator call spawns a fresh subprocess, so we
        # cannot rely on in-process monotonic counters.  The attempt is
        # 1-based; the search path goes:
        #   request["event"]["payload"]["attempt"]   (wrapped NDJSON envelope from Go)
        #   request["attempt"]                       (flat dict used in unit tests)
        attempt = 1  # fallback
        try:
            payload = request_env["event"]["payload"]
            if isinstance(payload, dict) and "attempt" in payload:
                attempt = int(payload["attempt"])
        except (KeyError, TypeError, ValueError):
            try:
                attempt = int(request_env["attempt"])
            except (KeyError, TypeError, ValueError):
                pass
        idx = attempt - 1

        # --- transcript exhaustion ------------------------------------------
        if idx < 0 or idx >= len(entries):
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

        # --- tool calls (worker only) ---------------------------------------
        # NODE: the runtime owns the bidirectional tool-call loop so
        # cognitive functions stay pure (entry, request) -> result.
        delay = float(os.environ.get("LIMEN_MOCK_DELAY", "0"))

        if delay > 0:
            time.sleep(delay)

        if role == "worker":
            for tc in entry.get("tool_calls", []):
                if delay > 0:
                    time.sleep(delay)
                self.request_tool(tc["name"], tc["args"])

        if delay > 0:
            time.sleep(delay)

        # --- cognitive work -------------------------------------------------
        result = cognitive_fn(entry, request_env)

        if delay > 0:
            time.sleep(delay)

        # --- write result event ---------------------------------------------
        task_id = _extract_task_id(request_env)

        if role == "validator":
            criteria_list = result.get("criteria", [])
            criteria_names = [c.get("name") for c in criteria_list if isinstance(c, dict) and "name" in c]

            # Emit validator.examining
            self._write_envelope({
                "kind": "event",
                "event": {
                    "type": "validator.examining",
                    "task_id": task_id,
                    "payload": {
                        "criteria": criteria_names,
                    },
                    "timestamp": _now_ms(),
                },
            })
            if delay > 0:
                time.sleep(delay)

            # Emit each validator.criterion_result
            for c in criteria_list:
                if isinstance(c, dict):
                    self._write_envelope({
                        "kind": "event",
                        "event": {
                            "type": "validator.criterion_result",
                            "task_id": task_id,
                            "payload": {
                                "criterion": c.get("name"),
                                "passed": c.get("passes"),
                                "detail": c.get("detail"),
                            },
                            "timestamp": _now_ms(),
                        },
                    })
                    if delay > 0:
                        time.sleep(delay)

            # Emit final validator.verdict
            self._write_envelope({
                "kind": "event",
                "event": {
                    "type": "validator.verdict",
                    "task_id": task_id,
                    "payload": {
                        "passes": result.get("passes"),
                        "feedback": result.get("feedback"),
                    },
                    "timestamp": _now_ms(),
                },
            })
        else:
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
