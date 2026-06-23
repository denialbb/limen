"""Unit tests for the per-role cognitive functions (pure 2-arg, no subprocess)."""

from __future__ import annotations

from limen.mock.router import router_fn
from limen.mock.worker import worker_fn
from limen.mock.validator import validator_fn


# ---------------------------------------------------------------------------
# Router
# ---------------------------------------------------------------------------

class TestRouterFunction:
    def test_returns_decision_payload(self):
        """router_fn(entry, request) returns the transcript entry as-is."""
        entry = {"decision": "proceed", "rationale": "safe", "complexity": "low"}
        request = {"task": {"id": "t1", "description": "d"}, "attempt": 1}
        result = router_fn(entry, request)
        assert result == entry
        # Ensure it's a copy, not the same mutable object.
        entry["decision"] = "expand"
        assert result["decision"] == "proceed"

    def test_ignores_request(self):
        """router_fn is pure and deterministic given entry."""
        entry = {"decision": "expand", "rationale": "complex", "complexity": "high"}
        result = router_fn(entry, {})
        assert result["decision"] == "expand"


# ---------------------------------------------------------------------------
# Validator
# ---------------------------------------------------------------------------

class TestValidatorFunction:
    def test_returns_verdict_payload(self):
        """validator_fn(entry, request) returns the transcript entry as-is."""
        entry = {
            "passes": False,
            "feedback": "off-by-one",
            "criteria": [
                {"name": "compiles", "passes": True, "detail": "OK"},
                {"name": "correctness", "passes": False, "detail": "bad"},
            ],
        }
        request = {"task": {"id": "t3"}, "worktree_diff": "diff", "attempt": 1}
        result = validator_fn(entry, request)
        assert result["passes"] is False
        assert len(result["criteria"]) == 2

    def test_return_is_an_independent_copy(self):
        """validator_fn returns a new dict, not the entry reference."""
        entry = {"passes": True, "feedback": "all good", "criteria": []}
        result = validator_fn(entry, {})
        entry["passes"] = False
        assert result["passes"] is True


# ---------------------------------------------------------------------------
# Worker
# ---------------------------------------------------------------------------

class TestWorkerFunction:
    def test_returns_result_payload(self):
        """worker_fn(entry, request) returns entry['result'] as a copy."""
        entry = {
            "tool_calls": [{"name": "file.write", "args": {"path": "f.txt", "content": "x"}}],
            "result": {"status": "complete", "summary": "done"},
        }
        request = {"task": {"id": "t1"}, "feedback": "", "attempt": 1}
        result = worker_fn(entry, request)
        assert result == {"status": "complete", "summary": "done"}

    def test_return_is_independent_copy(self):
        """Result is a copy, not a reference to entry['result']."""
        entry = {
            "tool_calls": [],
            "result": {"status": "complete", "summary": "nothing to do"},
        }
        result = worker_fn(entry, {"task": {"id": "t2"}, "attempt": 1})
        entry["result"]["status"] = "tampered"
        assert result["status"] == "complete"

    def test_handles_missing_result_key(self):
        """Worker without a result key returns empty dict."""
        entry = {"tool_calls": []}
        result = worker_fn(entry, {"task": {"id": "t3"}, "attempt": 1})
        assert result == {}

    def test_ignores_request(self):
        """worker_fn is pure and ignores the request parameter."""
        entry = {"result": {"status": "complete"}}
        result = worker_fn(entry, {})
        assert result == {"status": "complete"}
