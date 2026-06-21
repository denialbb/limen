import pytest
from limen.router.policy import RouterPolicy

def test_router_policy_statelessness():
    """Ensure the router policy evaluates purely statelessly."""
    policy = RouterPolicy()
    assert not hasattr(policy, "state")
    assert len(policy.__dict__) == 0, "RouterPolicy instance should be stateless"

def test_classify():
    policy = RouterPolicy()
    task = {"id": "task_1", "description": "Fix bug"}
    result = policy.classify(task)
    assert isinstance(result, str)

def test_estimate_complexity():
    policy = RouterPolicy()
    task = {"id": "task_1", "description": "Fix bug"}
    result = policy.estimate_complexity(task)
    assert result in ["easy", "normal", "hard"]

def test_decide_routing():
    policy = RouterPolicy()
    metadata = {
        "confidence": 0.95,
        "coverage_hint": 0.8
    }
    result = policy.decide_routing(metadata)
    assert result in ["proceed", "expand", "escalate"]
