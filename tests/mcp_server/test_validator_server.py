import pytest
from limen.mcp_server.validator_server import ValidatorServer

def test_validator_server_statelessness():
    """Ensure the validator server does not maintain any state."""
    server = ValidatorServer()
    assert not hasattr(server, "tasks")
    assert not hasattr(server, "state")
    assert len(server.__dict__) == 0, "ValidatorServer instance should be stateless"

def test_approve():
    server = ValidatorServer()
    server.approve("task_123")

def test_request_revision():
    server = ValidatorServer()
    server.request_revision("task_123", ["Missing return type", "Fails edge case X"])

def test_reject():
    server = ValidatorServer()
    server.reject("task_123", "Fundamentally flawed approach")

def test_expand_context():
    server = ValidatorServer()
    server.expand_context("task_123", "Cannot verify database interactions")
