"""Unit tests for the per-role cognitive functions (pure, no subprocess)."""

from __future__ import annotations

from limen.mock._router import router_fn
from limen.mock._worker import worker_fn
from limen.mock._validator import validator_fn


# ---------------------------------------------------------------------------
# Router
# ---------------------------------------------------------------------------

class TestRouterFunction:
    def test_returns_decision_payload(self):
        """router_fn returns the transcript entry as-is."""
        entry = {"decision": "proceed", "rationale": "safe", "complexity": "low"}
        request = {"task": {"id": "t1", "description": "d"}, "attempt": 1}
        result = router_fn(None, entry, request)
        assert result == entry

    def test_ignores_runtime_parameter(self):
        """router_fn is pure; runtime argument can be anything."""
        entry = {"decision": "expand", "rationale": "complex", "complexity": "high"}
        request = {"task": {"id": "t2"}, "attempt": 2}
        result = router_fn("ignored", entry, request)
        assert result["decision"] == "expand"


# ---------------------------------------------------------------------------
# Validator
# ---------------------------------------------------------------------------

class TestValidatorFunction:
    def test_returns_verdict_payload(self):
        """validator_fn returns the transcript entry as-is."""
        entry = {
            "passes": False,
            "feedback": "off-by-one",
            "criteria": [
                {"name": "compiles", "passes": True, "detail": "OK"},
                {"name": "correctness", "passes": False, "detail": "bad"},
            ],
        }
        request = {"task": {"id": "t3"}, "worktree_diff": "diff", "attempt": 1}
        result = validator_fn(None, entry, request)
        assert result["passes"] is False
        assert len(result["criteria"]) == 2

    def test_ignores_runtime_parameter(self):
        """validator_fn is pure; runtime argument can be anything."""
        entry = {"passes": True, "feedback": "all good", "criteria": []}
        result = validator_fn("ignored", entry, {})
        assert result["passes"] is True


# ---------------------------------------------------------------------------
# Worker
# ---------------------------------------------------------------------------

class _FakeRuntime:
    """Captures tool calls instead of doing real I/O."""

    def __init__(self):
        self.calls: list[tuple[str, dict]] = []
        self._results: list[object] = []

    def request_tool(self, name: str, args: dict) -> object:
        self.calls.append((name, args))
        if not self._results:
            raise RuntimeError("no pre-staged results")
        return self._results.pop(0)

    def stage_result(self, value: object) -> None:
        self._results.append(value)


class TestWorkerFunction:
    def test_replays_tool_calls_from_entry(self):
        """worker_fn calls runtime.request_tool for each tool_call in the entry."""
        fake = _FakeRuntime()
        fake.stage_result(None)
        entry = {
            "tool_calls": [{"name": "file.write", "args": {"path": "f.txt", "content": "x"}}],
            "result": {"status": "complete", "summary": "done"},
        }
        request = {"task": {"id": "t1"}, "feedback": "", "attempt": 1}
        result = worker_fn(fake, entry, request)

        assert len(fake.calls) == 1
        assert fake.calls[0] == ("file.write", {"path": "f.txt", "content": "x"})
        assert result == {"status": "complete", "summary": "done"}

    def test_no_tool_calls_still_returns_result(self):
        """Worker with empty tool_calls returns result without any calls."""
        fake = _FakeRuntime()
        entry = {
            "tool_calls": [],
            "result": {"status": "complete", "summary": "nothing to do"},
        }
        result = worker_fn(fake, entry, {"task": {"id": "t2"}, "attempt": 1})
        assert fake.calls == []
        assert result["summary"] == "nothing to do"

    def test_multiple_tool_calls_in_order(self):
        """Multiple tool_calls are replayed in the transcript order."""
        fake = _FakeRuntime()
        fake.stage_result({"ok": True})
        fake.stage_result({"ok": True})
        entry = {
            "tool_calls": [
                {"name": "file.write", "args": {"path": "a.txt", "content": "1"}},
                {"name": "file.write", "args": {"path": "b.txt", "content": "2"}},
            ],
            "result": {"status": "complete"},
        }
        result = worker_fn(fake, entry, {"task": {"id": "t3"}, "attempt": 1})

        assert len(fake.calls) == 2
        assert fake.calls[0] == ("file.write", {"path": "a.txt", "content": "1"})
        assert fake.calls[1] == ("file.write", {"path": "b.txt", "content": "2"})
        assert result["status"] == "complete"

    def test_returns_deep_copy_not_reference(self):
        """Cognitive functions return a dict copy, not the mutable entry object."""
        fake = _FakeRuntime()
        entry = {"tool_calls": [], "result": {"status": "complete"}}
        result = worker_fn(fake, entry, {"task": {"id": "t4"}, "attempt": 1})
        # Mutating entry after the fact should not affect result.
        entry["result"]["status"] = "tampered"
        assert result["status"] == "complete"
