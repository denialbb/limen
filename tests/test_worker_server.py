import pytest
import json
import subprocess
from unittest.mock import patch, MagicMock

from limen.mcp_server.worker_server import WorkerServer

@pytest.fixture
def server():
    return WorkerServer()

@patch("limen.mcp_server.worker_server.subprocess.run")
def test_get_context_success(mock_run, server):
    expected_output = {
        "query_id": "q123",
        "chunks": [],
        "sources": [],
        "confidence": 0.9,
        "coverage_hint": "good"
    }
    
    mock_result = MagicMock()
    mock_result.stdout = json.dumps(expected_output)
    mock_run.return_value = mock_result
    
    result = server.get_context("task_123")
    
    mock_run.assert_called_once_with(
        ["limen", "worker", "get-context", "task_123"],
        capture_output=True,
        text=True,
        check=True
    )
    assert result == expected_output

@patch("limen.mcp_server.worker_server.subprocess.run")
def test_get_context_failure(mock_run, server):
    mock_run.side_effect = subprocess.CalledProcessError(
        returncode=1,
        cmd=["limen", "worker", "get-context", "task_123"],
        stderr="Error fetching context"
    )
    
    with pytest.raises(RuntimeError, match="Command 'limen worker get-context task_123' failed with exit code 1: Error fetching context"):
        server.get_context("task_123")
        
@patch("limen.mcp_server.worker_server.subprocess.run")
def test_get_context_invalid_json(mock_run, server):
    mock_result = MagicMock()
    mock_result.stdout = "Invalid JSON"
    mock_run.return_value = mock_result
    
    with pytest.raises(RuntimeError, match="Failed to parse JSON output"):
        server.get_context("task_123")

@patch("limen.mcp_server.worker_server.subprocess.run")
def test_request_more_context_success(mock_run, server):
    mock_result = MagicMock()
    mock_run.return_value = mock_result
    
    server.request_more_context("task_123", "need more details")
    
    mock_run.assert_called_once_with(
        ["limen", "worker", "request-more-context", "task_123", "need more details"],
        capture_output=True,
        text=True,
        check=True
    )

@patch("limen.mcp_server.worker_server.subprocess.run")
def test_request_more_context_failure(mock_run, server):
    mock_run.side_effect = subprocess.CalledProcessError(
        returncode=1,
        cmd=["limen", "worker", "request-more-context", "task_123", "reason"],
        stderr="Task not found"
    )
    
    with pytest.raises(RuntimeError, match="Command 'limen worker request-more-context task_123 reason' failed with exit code 1: Task not found"):
        server.request_more_context("task_123", "reason")

@patch("limen.mcp_server.worker_server.subprocess.run")
def test_submit_work_success(mock_run, server):
    mock_result = MagicMock()
    mock_run.return_value = mock_result
    
    server.submit_work("task_123", "fixed bug")
    
    mock_run.assert_called_once_with(
        ["limen", "worker", "submit-work", "task_123", "fixed bug"],
        capture_output=True,
        text=True,
        check=True
    )

@patch("limen.mcp_server.worker_server.subprocess.run")
def test_submit_work_failure(mock_run, server):
    mock_run.side_effect = subprocess.CalledProcessError(
        returncode=2,
        cmd=["limen", "worker", "submit-work", "task_123", "summary"],
        stderr="Validation failed"
    )
    
    with pytest.raises(RuntimeError, match="Command 'limen worker submit-work task_123 summary' failed with exit code 2: Validation failed"):
        server.submit_work("task_123", "summary")
