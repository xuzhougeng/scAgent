from __future__ import annotations

from typing import Any

from i18n import t

from .base import SkillExecutionContext
from .common import require_target


def inspect_dataset(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    metadata = target.metadata or {}
    return {
        "summary": (
            t("runtime.inspectDatasetSummary",
              label=target.label, n_obs=target.n_obs, n_vars=target.n_vars,
              state=state.format_object_state_zh(target.state),
              annotation_note=state.describe_annotation_summary(metadata))
        ),
        "facts": state.build_inspect_dataset_facts(target),
        "metadata": {
            "available_obs": metadata.get("obs_fields", []),
            "available_embeddings": metadata.get("obsm_keys", []),
            "cell_type_annotation": metadata.get("cell_type_annotation"),
            "cluster_annotation": metadata.get("cluster_annotation"),
            "categorical_obs_fields": metadata.get("categorical_obs_fields", []),
            "assessment": metadata.get("assessment", {}),
        },
    }


def summarize_qc(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    adata = state._load_qc_adata(target)
    qc_info = state._ensure_qc_metrics(adata, ctx.params.get("mt_prefix"))
    stats = {metric: state._metric_stats(adata, metric) for metric in qc_info["metrics"]}
    facts = {
        "cell_count": int(adata.n_obs),
        "gene_count": int(adata.n_vars),
        "mt_prefix": qc_info["mt_prefix"],
        "qc_metrics": stats,
    }

    summary_bits = [t("runtime.summarizeQcSummary", label=target.label)]
    total_counts_stats = stats.get("total_counts") or {}
    genes_stats = stats.get("n_genes_by_counts") or {}
    mt_stats = stats.get("pct_counts_mt") or {}
    if total_counts_stats:
        summary_bits.append(t("runtime.qcTotalCountsMedian", median=f"{total_counts_stats['median']:g}"))
    if genes_stats:
        summary_bits.append(t("runtime.qcGenesMedian", median=f"{genes_stats['median']:g}"))
    if mt_stats:
        summary_bits.append(t("runtime.qcMtMedian", median=f"{mt_stats['median']:g}"))
    if qc_info["mt_prefix"]:
        summary_bits.append(t("runtime.qcMtPrefixDetected", prefix=qc_info['mt_prefix']))
    else:
        summary_bits.append(t("runtime.qcMtPrefixNotDetected"))

    return {
        "summary": "".join(summary_bits),
        "facts": facts,
        "metadata": {
            "qc_metrics": stats,
            "mt_prefix": qc_info["mt_prefix"],
        },
    }


def plot_qc_metrics(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    adata = state._load_qc_adata(target)
    qc_info = state._ensure_qc_metrics(adata, ctx.params.get("mt_prefix"))
    metrics = state._normalize_qc_metric_names(ctx.params.get("metrics"), qc_info["metrics"])
    if not metrics:
        raise RuntimeError(t("error.plotQcMetricsNoMetrics"))

    title = str(ctx.params.get("title") or "").strip() or None
    path = state._plot_path(ctx.workspace_root, ctx.skill, target.label, ctx.request_id)
    state._save_qc_metrics_plot(adata, path, metrics, title=title)
    return {
        "summary": t("runtime.plotQcMetricsSummary", label=target.label, metrics=state.format_list_zh(metrics)),
        "artifacts": [
            {
                "kind": "plot",
                "title": t("runtime.plotQcMetricsArtifactTitle", label=target.label),
                "path": str(path),
                "content_type": "image/png",
                "summary": t("runtime.plotQcMetricsArtifactSummary", label=target.label),
            }
        ],
        "metadata": {
            "metrics": metrics,
            "mt_prefix": qc_info["mt_prefix"],
        },
    }


HANDLERS = {
    "inspect_dataset": inspect_dataset,
    "assess_dataset": inspect_dataset,
    "summarize_qc": summarize_qc,
    "plot_qc_metrics": plot_qc_metrics,
}
