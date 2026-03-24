from __future__ import annotations

import io
import json
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from typing import Any

from i18n import t

from .base import SkillExecutionContext
from .common import require_target


def run_python_analysis(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    import pandas as pd

    target = require_target(state, ctx)
    _, sc, plt, np, _ = state.analysis_modules()
    adata = state._load_adata(target)
    counts_adata = state._load_counts_adata(target)
    code = str(ctx.params.get("code") or "").strip()
    if code == "":
        raise RuntimeError(t("error.customAnalysisMissingCode"))

    output_label = str(ctx.params.get("output_label") or f"custom_{target.label}").strip() or f"custom_{target.label}"
    persist_output = bool(ctx.params.get("persist_output"))
    stdout_buffer = io.StringIO()
    stderr_buffer = io.StringIO()
    exec_env: dict[str, Any] = {
        "__builtins__": state.safe_exec_builtins(),
        "adata": adata,
        "counts_adata": counts_adata,
        "sc": sc,
        "np": np,
        "pd": pd,
        "plt": plt,
        "Path": Path,
        "json": json,
        "workspace_root": ctx.workspace_root,
        "artifacts_dir": ctx.workspace_root / "artifacts",
        "result_summary": "",
        "result_text": "",
        "result_value": None,
        "output_adata": None,
        "persist_output": persist_output,
        "figure": None,
        "result_table": None,
    }
    plt.close("all")
    with redirect_stdout(stdout_buffer), redirect_stderr(stderr_buffer):
        exec(code, exec_env, exec_env)

    stdout_text = stdout_buffer.getvalue().strip()
    stderr_text = stderr_buffer.getvalue().strip()
    result_summary = str(exec_env.get("result_summary") or "").strip()
    result_text = str(exec_env.get("result_text") or "").strip()
    result_value = exec_env.get("result_value")
    output_adata = exec_env.get("output_adata")
    if output_adata is None and bool(exec_env.get("persist_output")):
        output_adata = exec_env.get("adata")

    artifacts: list[dict[str, Any]] = []
    figure = exec_env.get("figure")
    if figure is None and plt.get_fignums():
        figure = plt.gcf()
    if figure is not None and hasattr(figure, "savefig"):
        figure_path = state._save_custom_figure(
            figure,
            ctx.workspace_root,
            f"custom_plot_{state.slug(output_label)}",
            ctx.request_id,
        )
        artifacts.append(
            {
                "kind": "plot",
                "title": t("runtime.customPlotArtifactTitle", label=output_label),
                "path": str(figure_path),
                "content_type": "image/png",
                "summary": t("runtime.customPlotArtifactSummary"),
            }
        )

    result_table = exec_env.get("result_table")
    if result_table is not None and hasattr(result_table, "to_csv"):
        table_path = state._save_custom_table(
            result_table,
            ctx.workspace_root,
            f"custom_table_{state.slug(output_label)}",
            ctx.request_id,
        )
        artifacts.append(
            {
                "kind": "table",
                "title": t("runtime.customTableArtifactTitle", label=output_label),
                "path": str(table_path),
                "content_type": "text/csv",
                "summary": t("runtime.customTableArtifactSummary"),
            }
        )

    facts = state.build_custom_analysis_facts(
        output_label=output_label,
        result_value=result_value,
        result_text=result_text,
        result_summary=result_summary,
        stdout_text=stdout_text,
        result_table=result_table,
        output_adata=output_adata,
        artifacts=artifacts,
    )

    response: dict[str, Any] = {
        "summary": state.default_custom_analysis_summary(
            target_label=target.label,
            output_label=output_label,
            facts=facts,
            generated_object=output_adata is not None,
        ),
        "facts": facts,
        "metadata": {
            "code_executed": True,
            "stdout": stdout_text or None,
            "stderr": stderr_text or None,
        },
    }

    if output_adata is not None:
        persisted = state._persist_adata_object(
            session_id=ctx.session_id,
            workspace_root=ctx.workspace_root,
            label=output_label,
            kind=state._default_kind_after_processing(target),
            adata=output_adata,
            summary="",
            request_id=ctx.request_id,
        )
        response["object"] = persisted["object"]
        if not result_summary and not result_text and facts.get("result_value") is None:
            response["summary"] = t("runtime.customAnalysisWithObjectFallback", label=target.label, output_label=output_label)

    if artifacts:
        response["artifacts"] = artifacts

    return response


HANDLERS = {
    "run_python_analysis": run_python_analysis,
}
