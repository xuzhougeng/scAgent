from __future__ import annotations

from typing import Any

from .cluster import HANDLERS as CLUSTER_HANDLERS
from .custom import HANDLERS as CUSTOM_HANDLERS
from .export import HANDLERS as EXPORT_HANDLERS
from .inspection import HANDLERS as INSPECTION_HANDLERS
from .plotting import HANDLERS as PLOTTING_HANDLERS
from .preprocess import HANDLERS as PREPROCESS_HANDLERS


def _merge_handlers(*groups: dict[str, Any]) -> dict[str, Any]:
    handlers: dict[str, Any] = {}
    for group in groups:
        for name, handler in group.items():
            if name in handlers:
                raise ValueError(f"duplicate builtin skill registration: {name}")
            handlers[name] = handler
    return handlers


HANDLERS = _merge_handlers(
    INSPECTION_HANDLERS,
    PREPROCESS_HANDLERS,
    CLUSTER_HANDLERS,
    PLOTTING_HANDLERS,
    CUSTOM_HANDLERS,
    EXPORT_HANDLERS,
)


def register_builtin_skills(registry: Any) -> None:
    for name, handler in HANDLERS.items():
        registry.register_builtin(name, handler)
