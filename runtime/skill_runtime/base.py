from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Any


@dataclass(frozen=True)
class SkillExecutionContext:
    payload: dict[str, Any]
    skill: str
    session_id: str
    workspace_root: Path
    request_id: str | None
    target: Any
    params: dict[str, Any]

    @classmethod
    def from_payload(cls, payload: dict[str, Any], target: Any) -> "SkillExecutionContext":
        raw_params = payload.get("params") or {}
        params = raw_params if isinstance(raw_params, dict) else {}
        return cls(
            payload=payload,
            skill=str(payload["skill"]),
            session_id=str(payload["session_id"]),
            workspace_root=Path(str(payload["workspace_root"])),
            request_id=str(payload.get("request_id") or "").strip() or None,
            target=target,
            params=params,
        )
