import json
import pytest
from unittest.mock import patch, MagicMock
import subprocess

from limen.mcp_server.validator_server import ValidatorServer

@pytest.fixture
def validator():
    return ValidatorServer()

@patch('subprocess.run')
def test_approve_success(mock_run, validator):
    validator.approve("task123")
    mock_run.assert_called_once_with(
        ["limen", "validator", "approve", "task123"],
        check=True,
        capture_output=True,
        text=True
    )

@patch('subprocess.run')
def test_approve_failure(mock_run, validator):
    mock_run.side_effect = subprocess.CalledProcessError(
        returncode=1,
        cmd=["limen", "validator", "approve", "task123"],
        stderr="Task not found"
    )
    with pytest.raises(RuntimeError) as excinfo:
        validator.approve("task123")
    assert "Task not found" in str(excinfo.value)
    assert "exit status 1" in str(excinfo.value)

@patch('subprocess.run')
def test_request_revision_success(mock_run, validator):
    issues = ["issue1", "issue2"]
    validator.request_revision("task123", issues)
    mock_run.assert_called_once_with(
        ["limen", "validator", "request-revision", "task123", json.dumps(issues)],
        check=True,
        capture_output=True,
        text=True
    )

@patch('subprocess.run')
def test_request_revision_failure(mock_run, validator):
    issues = ["issue1"]
    mock_run.side_effect = subprocess.CalledProcessError(
        returncode=2,
        cmd=["limen", "validator", "request-revision", "task123", json.dumps(issues)],
        stderr="Invalid format"
    )
    with pytest.raises(RuntimeError) as excinfo:
        validator.request_revision("task123", issues)
    assert "Invalid format" in str(excinfo.value)
    assert "exit status 2" in str(excinfo.value)

@patch('subprocess.run')
def test_reject_success(mock_run, validator):
    validator.reject("task123", "Too complex")
    mock_run.assert_called_once_with(
        ["limen", "validator", "reject", "task123", "Too complex"],
        check=True,
        capture_output=True,
        text=True
    )

@patch('subprocess.run')
def test_reject_failure(mock_run, validator):
    mock_run.side_effect = subprocess.CalledProcessError(
        returncode=1,
        cmd=["limen", "validator", "reject", "task123", "reason"],
        stderr="Error"
    )
    with pytest.raises(RuntimeError) as excinfo:
        validator.reject("task123", "reason")
    assert "Error" in str(excinfo.value)

@patch('subprocess.run')
def test_expand_context_success(mock_run, validator):
    validator.expand_context("task123", "Need more files")
    mock_run.assert_called_once_with(
        ["limen", "validator", "expand-context", "task123", "Need more files"],
        check=True,
        capture_output=True,
        text=True
    )

@patch('subprocess.run')
def test_expand_context_failure(mock_run, validator):
    mock_run.side_effect = subprocess.CalledProcessError(
        returncode=3,
        cmd=["limen", "validator", "expand-context", "task123", "reason"],
        stderr="Not supported"
    )
    with pytest.raises(RuntimeError) as excinfo:
        validator.expand_context("task123", "reason")
    assert "Not supported" in str(excinfo.value)
    assert "exit status 3" in str(excinfo.value)
