from typing import Dict, Any

class WorkerServer:
    """
    Worker Server Stub.
    This server must not maintain any local state (e.g. tracking tasks).
    All canonical state is maintained by the Go Core.
    """
    def __init__(self):
        # Enforce statelessness
        pass

    def get_context(self, task_id: str) -> Dict[str, Any]:
        """
        Requests the initially compiled context payload.
        Returns a dictionary matching the retrieval contract:
        query_id, chunks, sources, confidence, coverage_hint.
        """
        raise NotImplementedError("Stub: get_context not implemented")

    def request_more_context(self, task_id: str, reason: str) -> None:
        """
        Signals that the provided context is insufficient.
        """
        raise NotImplementedError("Stub: request_more_context not implemented")

    def submit_work(self, task_id: str, summary: str) -> None:
        """
        Signals intent that the worker has finished editing and is ready for validation.
        """
        raise NotImplementedError("Stub: submit_work not implemented")
