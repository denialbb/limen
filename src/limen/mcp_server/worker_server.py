import subprocess
import json
from typing import Dict, Any

class WorkerServer:
    """
    Worker Server.
    This server must not maintain any local state (e.g. tracking tasks).
    All canonical state is maintained by the Go Core.
    """
    def __init__(self):
        # Enforce statelessness
        pass

    def _run_cli(self, args: list[str]) -> subprocess.CompletedProcess:
        try:
            result = subprocess.run(
                args,
                capture_output=True,
                text=True,
                check=True
            )
            return result
        except subprocess.CalledProcessError as e:
            stderr = e.stderr.strip() if e.stderr else "Unknown error"
            raise RuntimeError(f"Command '{' '.join(args)}' failed with exit code {e.returncode}: {stderr}") from e

    def get_context(self, task_id: str) -> Dict[str, Any]:
        """
        Requests the initially compiled context payload.
        Returns a dictionary matching the retrieval contract:
        query_id, chunks, sources, confidence, coverage_hint.
        """
        result = self._run_cli(["limen", "worker", "get-context", task_id])
        try:
            return json.loads(result.stdout)
        except json.JSONDecodeError as e:
            raise RuntimeError(f"Failed to parse JSON output: {result.stdout}") from e

    def request_more_context(self, task_id: str, reason: str) -> None:
        """
        Signals that the provided context is insufficient.
        """
        self._run_cli(["limen", "worker", "request-more-context", task_id, reason])

    def submit_work(self, task_id: str, summary: str) -> None:
        """
        Signals intent that the worker has finished editing and is ready for validation.
        """
        self._run_cli(["limen", "worker", "submit-work", task_id, summary])
