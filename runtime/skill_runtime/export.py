from __future__ import annotations

from typing import Any

from i18n import t

from .base import SkillExecutionContext
from .common import require_target


def export_h5ad(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    export_path = state._artifact_path(ctx.workspace_root, state.slug(target.label) or "dataset", "h5ad", ctx.request_id)
    state._copy_file_atomic(target.materialized_path, export_path)
    return {
        "summary": t("runtime.exportH5adSummary", label=target.label),
        "artifacts": [
            {
                "kind": "file",
                "title": f"{target.label}.h5ad",
                "path": str(export_path),
                "content_type": "application/octet-stream",
                "summary": t("runtime.exportH5adArtifactSummary"),
            }
        ],
    }


def export_markers_csv(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    import pandas as pd

    target = require_target(state, ctx)
    source_path = state._table_source_path(target)
    export_path = state._artifact_path(ctx.workspace_root, f"{target.label}_markers", "csv", ctx.request_id)
    suffix = source_path.suffix.lower()
    if suffix == ".csv":
        state._copy_file_atomic(source_path, export_path)
    elif suffix in {".tsv", ".txt"}:
        table = pd.read_csv(source_path, sep="\t")
        state._write_table_atomic(table, export_path, index=False)
    else:
        try:
            table = pd.read_csv(source_path)
        except Exception:
            table = pd.read_csv(source_path, sep="\t")
        state._write_table_atomic(table, export_path, index=False)

    return {
        "summary": t("runtime.exportMarkersCsvSummary", label=target.label),
        "artifacts": [
            {
                "kind": "file",
                "title": f"{target.label}.csv",
                "path": str(export_path),
                "content_type": "text/csv",
                "summary": t("runtime.exportMarkersCsvArtifactSummary"),
            }
        ],
        "metadata": {
            "source_path": str(source_path),
        },
    }


def write_method(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    filename = str(ctx.params.get("filename") or "Methods.md").strip() or "Methods.md"
    extra_context = str(ctx.params.get("extra_context") or "").strip()
    history = ctx.params.get("_analysis_history") or []

    content = state._generate_methods_section(history, ctx.target, extra_context)
    stem = filename.rsplit(".", 1)[0] if "." in filename else filename
    ext = filename.rsplit(".", 1)[1] if "." in filename else "md"
    method_path = state._artifact_path(ctx.workspace_root, state.slug(stem) or "methods", ext, ctx.request_id)
    state._write_text_atomic(method_path, content, encoding="utf-8")

    return {
        "summary": f"Methods section saved to {filename}.",
        "artifacts": [
            {
                "kind": "file",
                "title": filename,
                "path": str(method_path),
                "content_type": "text/markdown",
                "summary": "Methods section describing the analysis pipeline.",
            }
        ],
    }


HANDLERS = {
    "export_h5ad": export_h5ad,
    "export_markers_csv": export_markers_csv,
    "write_method": write_method,
}
