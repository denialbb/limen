"""Unit tests for the mock runtime (no subprocess).

Drives the Runtime via fake ``sys.stdin`` / ``sys.stdout`` pairs using
``io.StringIO`` so every test is fast, deterministic, and isolated.
"""

from __future__ import annotations

import io
import json

import pytest

from limen.mock.runtime import Runtime, load_transcript

# ---------------------------------------------------------------------------
# Shared helpers
# ---------------------------------------------------------------------------

# Minimal transcript with one entry per role for most tests.
_TRANSCRIPT_ONE_EACH = {
    "transcript_id": "test",
    "router": [{"decision": "proceed", "rationale": "ok", "complexity": "low"}],
    "worker": [
        {
            "tool_calls": [{"name": "file.write", "args": {"path": "f.txt", "content": "x"}}],
            "result": {"status": "complete", "summary": "done"},
        }
    ],
    "validator": [
        {
            "passes": True,
            "feedback": "all good",
            "criteria": [{"name": "c1", "passes": True, "detail": ""}],
        }
    ],
}


def _make_transcript(role: str, entries: list[dict]) -> dict:
    """Build a transcript dict with entries only for *role*."""
    return {"transcript_id": "test", role: entries}


def _make_runtime(
    transcript: dict, stdin_content: str = ""
) -> tuple[Runtime, io.StringIO, io.StringIO]:
    """Create a Runtime with in-memory stdin/stdout."""
    sin = io.StringIO(stdin_content)
    sout = io.StringIO()
    rt = Runtime(transcript, stdin=sin, stdout=sout)
    return rt, sin, sout


def _request_envelope(task_id: str = "task-1", attempt: int = 1) -> dict:
    """Build a Go-style request envelope as the PRD spec describes."""
    return {"task": {"id": task_id, "description": "fix bug"}, "attempt": attempt}


def _line(env: dict) -> str:
    """Serialize one NDJSON envelope line."""
    return json.dumps(env) + "\n"


def _drain(sout: io.StringIO) -> list[dict]:
    """Return all envelopes written to *sout*.

    ``io.StringIO`` shares read/write head, so after writes the position
    is at end and ``readline()`` returns empty.  ``getvalue()`` sidesteps
    the issue entirely.
    """
    lines = sout.getvalue().splitlines()
    return [json.loads(line) for line in lines if line.strip()]


# ---------------------------------------------------------------------------
# Transcript loader
# ---------------------------------------------------------------------------

class TestTranscriptLoader:
    def test_loads_expected_shape(self):
        """Transcript loader parses JSON with router/worker/validator keys."""
        t = load_transcript("src/limen/mock/transcripts/spike.json")
        assert t["transcript_id"] == "spike-happy-retry"
        assert len(t["router"]) == 1
        assert len(t["worker"]) == 2
        assert len(t["validator"]) == 2

    def test_loads_missing_file_raises(self):
        with pytest.raises(FileNotFoundError):
            load_transcript("/nonexistent/transcript.json")


# ---------------------------------------------------------------------------
# NDJSON loop: reads request, returns result event
# ---------------------------------------------------------------------------

class TestNDJSONLoop:
    def test_router_returns_decision_event(self):
        """Router run_role reads request, calls cognitive fn, writes event."""
        req = _line(_request_envelope("task-1"))
        rt, sin, sout = _make_runtime(_TRANSCRIPT_ONE_EACH, stdin_content=req)

        rt.run_role("router", lambda entry, _req: entry)

        envs = _drain(sout)
        assert len(envs) == 1
        env = envs[0]
        assert env["kind"] == "event"
        ev = env["event"]
        assert ev["type"] == "router.decision"
        assert ev["task_id"] == "task-1"
        assert ev["payload"]["decision"] == "proceed"
        assert ev["payload"]["rationale"] == "ok"

    def test_validator_returns_verdict_event(self):
        """Validator run_role returns a validator.verdict event."""
        req = _line(_request_envelope("task-2"))
        rt, sin, sout = _make_runtime(_TRANSCRIPT_ONE_EACH, stdin_content=req)

        rt.run_role("validator", lambda entry, _req: entry)

        envs = _drain(sout)
        assert len(envs) == 3
        
        # 1. examining
        assert envs[0]["kind"] == "event"
        assert envs[0]["event"]["type"] == "validator.examining"
        assert envs[0]["event"]["payload"]["criteria"] == ["c1"]

        # 2. criterion_result
        assert envs[1]["kind"] == "event"
        assert envs[1]["event"]["type"] == "validator.criterion_result"
        assert envs[1]["event"]["payload"]["criterion"] == "c1"

        # 3. verdict
        assert envs[2]["kind"] == "event"
        assert envs[2]["event"]["type"] == "validator.verdict"
        assert envs[2]["event"]["payload"]["passes"] is True

    def test_worker_returns_finished_event(self):
        """Worker run_role replays tool calls, then returns finished event."""
        req = (
            _line(_request_envelope("task-3"))
            + _line(
                {
                    "kind": "tool_response",
                    "tool_response": {"id": "mock-1", "ok": True, "result": None},
                }
            )
        )
        rt, sin, sout = _make_runtime(_TRANSCRIPT_ONE_EACH, stdin_content=req)

        # Cognitive fn is pure: just returns the result payload.
        rt.run_role("worker", lambda entry, _req: entry.get("result", {}))

        envs = _drain(sout)
        assert len(envs) == 2

        # First envelope: tool_request (issued by runtime, not cognitive fn)
        tool_env = envs[0]
        assert tool_env["kind"] == "tool_request"
        tr = tool_env["tool_request"]
        assert tr["tool"] == "file.write"
        assert tr["args"] == {"path": "f.txt", "content": "x"}

        # Second envelope: result event
        result_env = envs[1]
        assert result_env["kind"] == "event"
        ev = result_env["event"]
        assert ev["type"] == "worker.finished"
        assert ev["task_id"] == "task-3"
        assert ev["payload"] == {"status": "complete", "summary": "done"}


# ---------------------------------------------------------------------------
# request_tool round-trip (exercised through run_role on worker)
# ---------------------------------------------------------------------------

class TestRequestTool:
    def test_emits_request_and_returns_result(self):
        """Runtime issues tool_request per entry[‘tool_calls’], blocks, returns result."""
        stdin = (
            _line(_request_envelope("task-4"))
            + _line(
                {
                    "kind": "tool_response",
                    "tool_response": {
                        "id": "mock-1",
                        "ok": True,
                        "result": {"written": True},
                    },
                }
            )
        )
        sout = io.StringIO()
        rt = Runtime(_TRANSCRIPT_ONE_EACH, stdin=io.StringIO(stdin), stdout=sout)

        rt.run_role("worker", lambda entry, _req: entry.get("result", {}))

        envs = _drain(sout)
        assert len(envs) == 2

        # First write: tool_request (from transcript entry)
        assert envs[0]["kind"] == "tool_request"
        assert envs[0]["tool_request"]["tool"] == "file.write"
        assert envs[0]["tool_request"]["args"] == {"path": "f.txt", "content": "x"}

        # Last write: result event
        assert envs[1]["kind"] == "event"
        assert envs[1]["event"]["payload"]["status"] == "complete"

    def test_tool_failure_raises(self):
        """When tool_response.ok is false, the runtime raises RuntimeError."""
        stdin = (
            _line(_request_envelope("task-5"))
            + _line(
                {
                    "kind": "tool_response",
                    "tool_response": {
                        "id": "mock-1",
                        "ok": False,
                        "error": "permission denied",
                    },
                }
            )
        )
        rt, sin, sout = _make_runtime(_TRANSCRIPT_ONE_EACH, stdin_content=stdin)

        with pytest.raises(
            RuntimeError, match=r"file.write.*failed.*permission denied"
        ):
            rt.run_role("worker", lambda entry, _req: entry.get("result", {}))


# ---------------------------------------------------------------------------
# Transcript sequencing (per role, monotonic)
# ---------------------------------------------------------------------------

class TestTranscriptSequencing:
    def test_second_invocation_gets_second_entry(self):
        """The 2nd call to run_role for the same role uses the 2nd transcript entry."""
        entries = [
            {"decision": "proceed", "rationale": "fail1", "complexity": "low"},
            {"decision": "proceed", "rationale": "pass", "complexity": "low"},
        ]
        t = _make_transcript("router", entries)

        # NODE: each subprocess is stateless; the attempt field from the Go
        # request serves as the 1-based transcript index.
        # Invocation 1 — attempt=1 -> entry 0 ("fail1")
        req = _line(_request_envelope("task-1", attempt=1))
        rt, sin, sout = _make_runtime(t, stdin_content=req)
        rt.run_role("router", lambda e, _req: e)
        envs1 = _drain(sout)
        assert envs1[0]["event"]["payload"]["rationale"] == "fail1"

        # Invocation 2 — attempt=2 -> entry 1 ("pass")
        rt._stdin = io.StringIO(_line(_request_envelope("task-1", attempt=2)))
        sout2 = io.StringIO()
        rt._stdout = sout2
        rt.run_role("router", lambda e, _req: e)
        envs2 = _drain(sout2)
        assert envs2[0]["event"]["payload"]["rationale"] == "pass"

    def test_roles_are_independent(self):
        """Router and validator indices are tracked independently."""
        t = {
            "router": [{"decision": "proceed", "rationale": "r", "complexity": "l"}],
            "validator": [{"passes": True, "feedback": "v", "criteria": []}],
            "transcript_id": "t",
        }
        req = _line(_request_envelope("task-1"))
        rt, sin, sout = _make_runtime(t, stdin_content=req)
        rt.run_role("router", lambda e, _req: e)
        envs1 = _drain(sout)
        assert envs1[0]["event"]["payload"]["decision"] == "proceed"

        rt._stdin = io.StringIO(_line(_request_envelope("task-1")))
        sout2 = io.StringIO()
        rt._stdout = sout2
        rt.run_role("validator", lambda e, _req: e)
        envs2 = _drain(sout2)
        assert envs2[-1]["event"]["payload"]["feedback"] == "v"


# ---------------------------------------------------------------------------
# Transcript exhaustion
# ---------------------------------------------------------------------------

class TestTranscriptExhaustion:
    def test_exhaustion_writes_error_envelope_and_exits(self):
        """When a role has no more entries, an error envelope is emitted."""
        t = _make_transcript(
            "router", [{"decision": "proceed", "rationale": "ok", "complexity": "l"}]
        )

        # Consume the only entry (attempt=1 -> index 0).
        req = _line(_request_envelope("task-1", attempt=1))
        rt, sin, sout = _make_runtime(t, stdin_content=req)
        rt.run_role("router", lambda e, _req: e)
        envs1 = _drain(sout)
        assert envs1[0]["event"]["type"] == "router.decision"

        # Now exhaustion: attempt=2 -> index 1 but only 1 entry exists.
        rt._stdin = io.StringIO(_line(_request_envelope("task-1", attempt=2)))
        sout2 = io.StringIO()
        rt._stdout = sout2
        with pytest.raises(SystemExit) as exc_info:
            rt.run_role("router", lambda e, _req: e)
        assert exc_info.value.code == 0

        env_err = _drain(sout2)[0]
        assert env_err["kind"] == "event"
        assert env_err["event"]["type"] == "error"
        assert "exhausted" in env_err["event"]["payload"]["error"]
        assert "router" in env_err["event"]["payload"]["error"]

    def test_exhaustion_does_not_silently_repeat(self):
        """Exhaustion emits error envelope, not the last entry repeated."""
        t = _make_transcript(
            "router", [{"decision": "proceed", "rationale": "only", "complexity": "low"}]
        )

        # First call — attempt=1 -> index 0, succeeds.
        req1 = _line(_request_envelope("task-1", attempt=1))
        rt, sin, sout = _make_runtime(t, stdin_content=req1)
        rt.run_role("router", lambda e, _req: e)
        envs1 = _drain(sout)
        assert envs1[0]["event"]["payload"]["rationale"] == "only"

        # Second call — attempt=2 -> index 1, exhausted.
        rt._stdin = io.StringIO(_line(_request_envelope("task-2", attempt=2)))
        sout2 = io.StringIO()
        rt._stdout = sout2
        with pytest.raises(SystemExit):
            rt.run_role("router", lambda e, _req: e)
        env_err = _drain(sout2)[0]
        assert env_err["event"]["type"] == "error"
        assert "exhausted" in env_err["event"]["payload"]["error"]

    def test_exhaustion_for_role_with_zero_entries(self):
        """Role with empty entry list fails on first invocation."""
        t = _make_transcript("worker", [])
        req = _line(_request_envelope("task-1"))
        rt, sin, sout = _make_runtime(t, stdin_content=req)
        with pytest.raises(SystemExit):
            rt.run_role("worker", lambda e, _req: e)
        env_err = _drain(sout)[0]
        assert env_err["event"]["type"] == "error"
        assert "0" in env_err["event"]["payload"]["error"]  # index 0
