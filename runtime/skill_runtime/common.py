from __future__ import annotations

from typing import Any

from .base import SkillExecutionContext


def require_target(state: Any, ctx: SkillExecutionContext) -> Any:
    return state._require_target(ctx.target, ctx.skill)
