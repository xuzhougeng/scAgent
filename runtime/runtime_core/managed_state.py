from __future__ import annotations

from pathlib import Path
from typing import Any

from .state import RuntimeState
from .worker_manager import SessionWorkerManager


class ManagedRuntimeState(RuntimeState):
    def __init__(self, logger: Any) -> None:
        super().__init__()
        self._workers = SessionWorkerManager(logger)

    def create_workspace_root(self, session_id: str, label: str, workspace_root: Path) -> dict[str, Any]:
        return self._workers.create_workspace_root(
            session_id=session_id,
            label=label,
            workspace_root=workspace_root,
        )

    def load_file(self, session_id: str, file_path: Path, label: str) -> dict[str, Any]:
        return self._workers.load_file(
            session_id=session_id,
            file_path=file_path,
            label=label,
        )

    def ensure_object(self, session_id: str, descriptor: dict[str, Any]) -> dict[str, Any]:
        return self._workers.ensure_object(
            session_id=session_id,
            descriptor=descriptor,
        )

    def execute(self, payload: dict[str, Any]) -> dict[str, Any]:
        return self._workers.execute(payload)

    def cancel_execution(self, session_id: str, request_id: str | None = None) -> dict[str, Any]:
        return self._workers.cancel_execution(
            session_id=session_id,
            request_id=request_id,
        )
