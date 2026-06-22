import json
import subprocess
from typing import List

class ValidatorServer:
    """
    Validator Server Stub.
    Must remain completely stateless.
    """
    def __init__(self):
        pass

    def _run_command(self, args: List[str]) -> None:
        try:
            subprocess.run(args, check=True, capture_output=True, text=True)
        except subprocess.CalledProcessError as e:
            raise RuntimeError(f"Command '{' '.join(args)}' failed with exit status {e.returncode}: {e.stderr}") from e

    def approve(self, task_id: str) -> None:
        """
        Signals absolute correctness. Triggers merge and cleanup.
        """
        self._run_command(["limen", "validator", "approve", task_id])

    def request_revision(self, task_id: str, issues: List[str]) -> None:
        """
        Signals that flaws exist and returns structured issues.
        """
        self._run_command(["limen", "validator", "request-revision", task_id, json.dumps(issues)])

    def reject(self, task_id: str, reason: str) -> None:
        """
        Fatal signal. Escalate to the User immediately.
        """
        self._run_command(["limen", "validator", "reject", task_id, reason])

    def expand_context(self, task_id: str, reason: str) -> None:
        """
        Signals Validator lacks sufficient context to prove correctness.
        """
        self._run_command(["limen", "validator", "expand-context", task_id, reason])
