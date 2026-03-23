from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable

from .base import SkillExecutionContext

BuiltinSkillHandler = Callable[[Any, SkillExecutionContext], dict[str, Any]]


@dataclass(frozen=True)
class PluginSkillDefinition:
    name: str
    bundle_id: str
    manifest_path: Path
    entrypoint: Path
    callable_name: str
    definition: dict[str, Any]


class SkillRegistry:
    def __init__(self, builtin_bundle_id: str) -> None:
        self._builtin_bundle_id = builtin_bundle_id
        self._builtin_handlers: dict[str, BuiltinSkillHandler] = {}

    def register_builtin(self, name: str, handler: BuiltinSkillHandler) -> None:
        skill_name = str(name or "").strip()
        if skill_name == "":
            raise ValueError("builtin skill name cannot be empty")
        if skill_name in self._builtin_handlers:
            raise ValueError(f"duplicate builtin skill: {skill_name}")
        self._builtin_handlers[skill_name] = handler

    def builtin_skills(self, state: Any) -> list[str]:
        if self._builtin_bundle_id in state.load_disabled_bundles():
            return []
        return list(self._builtin_handlers.keys())

    def load_plugin_skills(self, state: Any) -> dict[str, PluginSkillDefinition]:
        skills: dict[str, PluginSkillDefinition] = {}
        plugin_root = Path(state.plugin_root)
        if not plugin_root.exists():
            return skills

        disabled_bundles = state.load_disabled_bundles()
        for manifest_path in sorted(plugin_root.rglob("plugin.json")):
            try:
                payload = json.loads(manifest_path.read_text(encoding="utf-8"))
            except Exception:
                continue

            bundle_id = str(payload.get("id") or manifest_path.parent.name).strip() or manifest_path.parent.name
            if bundle_id in disabled_bundles:
                continue

            for skill_payload in payload.get("skills", []):
                if not isinstance(skill_payload, dict):
                    continue
                skill_name = str(skill_payload.get("name") or "").strip()
                if skill_name == "":
                    continue

                runtime_payload = skill_payload.get("runtime") or {}
                if not isinstance(runtime_payload, dict):
                    runtime_payload = {}
                kind = str(runtime_payload.get("kind") or "python").strip().lower()
                if kind != "python":
                    continue

                entrypoint_name = str(runtime_payload.get("entrypoint") or "plugin.py").strip() or "plugin.py"
                callable_name = str(runtime_payload.get("callable") or "run").strip() or "run"
                entrypoint_path = (manifest_path.parent / entrypoint_name).resolve()
                skills[skill_name] = PluginSkillDefinition(
                    name=skill_name,
                    bundle_id=bundle_id,
                    manifest_path=manifest_path,
                    entrypoint=entrypoint_path,
                    callable_name=callable_name,
                    definition=skill_payload,
                )

        return skills

    def skill_enabled(self, state: Any, skill_name: str) -> bool:
        if skill_name in self._builtin_handlers:
            return self._builtin_bundle_id not in state.load_disabled_bundles()
        return skill_name in self.load_plugin_skills(state)

    def execute_builtin(self, state: Any, ctx: SkillExecutionContext) -> dict[str, Any] | None:
        handler = self._builtin_handlers.get(ctx.skill)
        if handler is None:
            return None
        return handler(state, ctx)

    def plugin_skill(self, state: Any, skill_name: str) -> PluginSkillDefinition | None:
        if skill_name in self._builtin_handlers:
            return None
        return self.load_plugin_skills(state).get(skill_name)
