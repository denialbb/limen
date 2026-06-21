import pytest
from limen.mcp_server.worker_server import WorkerServer

def test_worker_server_statelessness():
    """Ensure the worker server does not maintain any state."""
    server = WorkerServer()
    # It shouldn't have dictionaries or properties tracking tasks
    assert not hasattr(server, "tasks")
    assert not hasattr(server, "state")
    assert len(server.__dict__) == 0, "WorkerServer instance should be stateless"

def test_get_context_returns_correct_schema():
    server = WorkerServer()
    result = server.get_context("task_123")
    assert isinstance(result, dict)
    assert "query_id" in result
    assert isinstance(result.get("chunks"), list)
    assert isinstance(result.get("sources"), list)
    assert isinstance(result.get("confidence"), float)
    assert isinstance(result.get("coverage_hint"), float)

def test_request_more_context():
    server = WorkerServer()
    server.request_more_context("task_123", "need additional references")

def test_submit_work():
    server = WorkerServer()
    server.submit_work("task_123", "completed task")
