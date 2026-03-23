from __future__ import annotations

import io
import json
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from typing import Any

from .base import SkillExecutionContext
from .registry import PluginSkillDefinition


def execute_plugin_skill(state: Any, ctx: SkillExecutionContext, plugin: PluginSkillDefinition) -> dict[str, Any]:
    entrypoint = Path(plugin.entrypoint)
    if not entrypoint.exists():
        raise RuntimeError(f"插件技能 `{ctx.skill}` 缺少入口脚本：{entrypoint.name}")

    params = ctx.params
    target = ctx.target
    adata = state._load_adata(target) if target is not None else None
    counts_adata = state._load_counts_adata(target) if target is not None else None
    _, sc, plt, np, _ = state.analysis_modules()

    def persist_adata(label: str, output_adata: Any, *, kind: str | None = None) -> dict[str, Any]:
        if target is None:
            raise RuntimeError("当前插件技能没有可持久化的目标对象。")
        persisted = state._persist_adata_object(
            session_id=ctx.session_id,
            workspace_root=ctx.workspace_root,
            label=str(label or f"{ctx.skill}_{target.label}"),
            kind=kind or state._default_kind_after_processing(target),
            adata=output_adata,
            summary="",
            request_id=ctx.request_id,
        )
        return persisted["object"]

    def save_figure(figure: Any, stem: str, *, title: str = "", summary: str = "") -> dict[str, Any]:
        figure_path = state._save_custom_figure(
            figure,
            ctx.workspace_root,
            stem or f"{ctx.skill}_{state.slug(target.label if target else ctx.skill)}",
            ctx.request_id,
        )
        return {
            "kind": "plot",
            "title": title or f"{ctx.skill} 输出图",
            "path": str(figure_path),
            "content_type": "image/png",
            "summary": summary or "由 Skill Hub 插件生成的图。",
        }

    def save_table(table: Any, stem: str, *, title: str = "", summary: str = "") -> dict[str, Any]:
        table_path = state._save_custom_table(
            table,
            ctx.workspace_root,
            stem or f"{ctx.skill}_{state.slug(target.label if target else ctx.skill)}",
            ctx.request_id,
        )
        return {
            "kind": "table",
            "title": title or f"{ctx.skill} 输出表",
            "path": str(table_path),
            "content_type": "text/csv",
            "summary": summary or "由 Skill Hub 插件生成的表。",
        }

    context = {
        "skill_name": ctx.skill,
        "bundle_id": plugin.bundle_id,
        "params": params,
        "session_id": ctx.session_id,
        "request_id": ctx.payload.get("request_id"),
        "target": state._plugin_object_context(target),
        "adata": adata,
        "counts_adata": counts_adata,
        "sc": sc,
        "np": np,
        "plt": plt,
        "json": json,
        "Path": Path,
        "workspace_root": ctx.workspace_root,
        "artifacts_dir": ctx.workspace_root / "artifacts",
        "plugin_dir": entrypoint.parent,
        "persist_adata": persist_adata,
        "save_figure": save_figure,
        "save_table": save_table,
    }

    exec_env: dict[str, Any] = {
        "__builtins__": state.safe_exec_builtins(),
        "context": context,
        "adata": adata,
        "counts_adata": counts_adata,
        "sc": sc,
        "np": np,
        "plt": plt,
        "json": json,
        "Path": Path,
    }
    stdout_buffer = io.StringIO()
    stderr_buffer = io.StringIO()
    with redirect_stdout(stdout_buffer), redirect_stderr(stderr_buffer):
        exec(entrypoint.read_text(encoding="utf-8"), exec_env, exec_env)
        handler = exec_env.get(plugin.callable_name)
        if not callable(handler):
            raise RuntimeError(f"插件技能 `{ctx.skill}` 未定义可调用入口 `{plugin.callable_name}`。")
        response = handler(context)

    if response is None:
        response = {}
    if not isinstance(response, dict):
        raise RuntimeError(f"插件技能 `{ctx.skill}` 返回值必须是 dict。")

    metadata = response.get("metadata")
    if not isinstance(metadata, dict):
        metadata = {}
    metadata["plugin_bundle_id"] = plugin.bundle_id
    metadata["plugin_skill"] = ctx.skill
    stdout_text = stdout_buffer.getvalue().strip()
    stderr_text = stderr_buffer.getvalue().strip()
    if stdout_text:
        metadata["stdout"] = stdout_text
    if stderr_text:
        metadata["stderr"] = stderr_text
    response["metadata"] = metadata
    return response
