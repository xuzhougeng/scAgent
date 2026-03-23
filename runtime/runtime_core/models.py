from __future__ import annotations

from dataclasses import dataclass
from typing import Any


@dataclass
class RuntimeObject:
    backend_ref: str
    session_id: str
    label: str
    kind: str
    n_obs: int
    n_vars: int
    state: str
    in_memory: bool
    materialized_path: str
    metadata: dict[str, Any]
