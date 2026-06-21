from typing import List

class ValidatorServer:
    """
    Validator Server Stub.
    Must remain completely stateless.
    """
    def __init__(self):
        pass

    def approve(self, task_id: str) -> None:
        """
        Signals absolute correctness. Triggers merge and cleanup.
        """
        raise NotImplementedError("Stub: approve not implemented")

    def request_revision(self, task_id: str, issues: List[str]) -> None:
        """
        Signals that flaws exist and returns structured issues.
        """
        raise NotImplementedError("Stub: request_revision not implemented")

    def reject(self, task_id: str, reason: str) -> None:
        """
        Fatal signal. Escalate to the User immediately.
        """
        raise NotImplementedError("Stub: reject not implemented")

    def expand_context(self, task_id: str, reason: str) -> None:
        """
        Signals Validator lacks sufficient context to prove correctness.
        """
        raise NotImplementedError("Stub: expand_context not implemented")
