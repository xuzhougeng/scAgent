from __future__ import annotations

from typing import Any

from i18n import t

from .base import SkillExecutionContext
from .common import require_target


def plot_umap(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    adata = state._load_adata(target)
    color_by = str(ctx.params.get("color_by") or "").strip()
    legend_loc_param = str(ctx.params.get("legend_loc") or "").strip()
    legend_loc = state._normalize_legend_loc(legend_loc_param)
    palette = str(ctx.params.get("palette") or "").strip() or None
    title = str(ctx.params.get("title") or "").strip() or None
    point_size = state._coerce_positive_float(ctx.params.get("point_size"), 8.0)
    figure_width = state._coerce_positive_float(ctx.params.get("figure_width"), 6.2)
    figure_height = state._coerce_positive_float(ctx.params.get("figure_height"), 4.8)
    if not color_by:
        try:
            color_by = state._cluster_field(target, adata, None)
        except RuntimeError:
            color_by = ""
    if color_by and color_by not in adata.obs.columns:
        raise RuntimeError(t("error.plotUmapNotObsField", field=color_by))
    if color_by and legend_loc_param == "":
        series = adata.obs[color_by]
        if getattr(series.dtype, "kind", "") not in {"i", "u", "f"}:
            categories = series.astype("category")
            if len(categories.cat.categories) > 4:
                legend_loc = "on data"

    path = state._plot_path(ctx.workspace_root, ctx.skill, target.label, ctx.request_id)
    state._save_umap_plot(
        adata,
        path,
        color_by or None,
        legend_loc=legend_loc,
        palette=palette,
        title=title,
        point_size=point_size,
        figure_width=figure_width,
        figure_height=figure_height,
    )

    summary_bits = [t("runtime.plotUmapSummary", label=target.label)]
    if color_by:
        summary_bits.append(t("runtime.plotUmapColorBy", color_by=color_by))
    if legend_loc != "best":
        summary_bits.append(t("runtime.plotUmapLegendLoc", legend_loc=legend_loc))
    if title:
        summary_bits.append(t("runtime.plotUmapTitle", title=title))
    return {
        "summary": "".join(summary_bits),
        "artifacts": [
            {
                "kind": "plot",
                "title": t("runtime.plotUmapArtifactTitle", label=target.label),
                "path": str(path),
                "content_type": "image/png",
                "summary": t("runtime.plotUmapArtifactSummary", label=target.label),
            }
        ],
        "metadata": {
            "placeholder_plot": False,
            "color_by": color_by or None,
            "legend_loc": legend_loc,
            "palette": palette,
            "title": title,
            "point_size": point_size,
            "figure_width": figure_width,
            "figure_height": figure_height,
        },
    }


def plot_gene_umap(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    adata = state._load_adata(target)
    requested_genes = state._normalize_gene_list(ctx.params.get("genes"))
    if not requested_genes:
        raise RuntimeError(t("error.plotGeneUmapNoGenes"))

    layer_name = str(ctx.params.get("layer") or "").strip() or None
    artifacts: list[dict[str, Any]] = []
    resolved_genes: list[dict[str, str]] = []
    for requested_gene in requested_genes:
        display_gene, gene_key, expression, source = state._resolve_gene_expression(adata, requested_gene, layer_name)
        path = state._plot_path(ctx.workspace_root, ctx.skill, f"{target.label}_{display_gene}", ctx.request_id)
        state._save_gene_umap_plot(adata, path, display_gene, expression)
        artifacts.append(
            {
                "kind": "plot",
                "title": t("runtime.plotGeneUmapArtifactTitle", label=target.label, gene=display_gene),
                "path": str(path),
                "content_type": "image/png",
                "summary": t("runtime.plotGeneUmapArtifactSummary", label=target.label, gene=display_gene),
            }
        )
        resolved_genes.append(
            {
                "requested": display_gene,
                "feature_key": gene_key,
                "source": source,
            }
        )

    summary_bits = [t("runtime.plotGeneUmapSummary", label=target.label, count=len(artifacts), genes=state.format_list_zh(requested_genes))]
    if layer_name:
        summary_bits.append(t("runtime.plotGeneUmapLayer", layer=layer_name))
    return {
        "summary": "".join(summary_bits),
        "artifacts": artifacts,
        "metadata": {
            "placeholder_plot": False,
            "genes": requested_genes,
            "resolved_genes": resolved_genes,
            "layer": layer_name,
        },
    }


def plot_dotplot(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, _, _, np, _ = state.analysis_modules()
    adata = state._load_adata(target)
    requested_genes, resolved_genes, missing_genes = state._resolve_gene_keys(
        adata,
        ctx.params.get("genes"),
        require_at_least_one=True,
    )
    requested_groupby = str(ctx.params.get("groupby") or "").strip()
    groupby = state._categorical_field(target, adata, requested_groupby if requested_groupby else None)
    categories = adata.obs[groupby].astype("category")
    codes = categories.cat.codes.to_numpy()
    group_labels = [str(item) for item in categories.cat.categories]
    gene_keys = [item["feature_key"] for item in resolved_genes]
    gene_labels = [item["requested"] for item in resolved_genes]
    expression = state._dense_matrix(adata[:, gene_keys].X)

    mean_values = []
    pct_values = []
    for index in range(len(group_labels)):
        group_mask = codes == index
        subset = expression[group_mask]
        if subset.shape[0] == 0:
            mean_values.append(np.zeros(len(gene_keys), dtype=float))
            pct_values.append(np.zeros(len(gene_keys), dtype=float))
            continue
        mean_values.append(np.asarray(subset.mean(axis=0), dtype=float).reshape(-1))
        pct_values.append(np.asarray((subset > 0).mean(axis=0) * 100.0, dtype=float).reshape(-1))

    title = str(ctx.params.get("title") or "").strip() or None
    palette = str(ctx.params.get("palette") or "").strip() or None
    path = state._plot_path(ctx.workspace_root, ctx.skill, target.label, ctx.request_id)
    state._save_dotplot(
        path,
        group_labels,
        gene_labels,
        np.vstack(mean_values),
        np.vstack(pct_values),
        title=title,
        palette=palette,
    )

    summary_bits = [t("runtime.plotDotplotSummary", label=target.label, groupby=groupby, genes=state.format_list_zh(gene_labels))]
    if missing_genes:
        summary_bits.append(t("runtime.missingGenes", genes=state.format_list_zh(missing_genes)))
    return {
        "summary": "".join(summary_bits),
        "artifacts": [
            {
                "kind": "plot",
                "title": t("runtime.plotDotplotArtifactTitle", label=target.label),
                "path": str(path),
                "content_type": "image/png",
                "summary": t("runtime.plotDotplotArtifactSummary", label=target.label, groupby=groupby),
            }
        ],
        "metadata": {
            "groupby": groupby,
            "genes": requested_genes,
            "resolved_genes": resolved_genes,
            "missing_genes": missing_genes,
        },
    }


def plot_violin(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, _, _, np, _ = state.analysis_modules()
    adata = state._load_adata(target)
    requested_genes, resolved_genes, missing_genes = state._resolve_gene_keys(
        adata,
        ctx.params.get("genes"),
        require_at_least_one=True,
    )
    requested_groupby = str(ctx.params.get("groupby") or "").strip()
    if requested_groupby:
        if requested_groupby not in adata.obs.columns:
            raise RuntimeError(t("error.missingObsField", field=requested_groupby))
        groupby = requested_groupby
    else:
        try:
            groupby = state._categorical_field(target, adata, None)
        except RuntimeError:
            groupby = ""

    if groupby:
        categories = adata.obs[groupby].astype("category")
        codes = categories.cat.codes.to_numpy()
        group_labels = [str(item) for item in categories.cat.categories]
    else:
        codes = np.zeros(adata.n_obs, dtype=int)
        group_labels = ["all cells"]

    grouped_values: list[list[Any]] = []
    for item in resolved_genes:
        expression = state._dense_vector(adata[:, [item["feature_key"]]].X)
        gene_groups = []
        for index in range(len(group_labels)):
            gene_groups.append(expression[codes == index])
        grouped_values.append(gene_groups)

    title = str(ctx.params.get("title") or "").strip() or None
    palette = str(ctx.params.get("palette") or "").strip() or None
    path = state._plot_path(ctx.workspace_root, ctx.skill, target.label, ctx.request_id)
    state._save_violin_plot(
        adata,
        path,
        groupby or None,
        group_labels,
        [item["requested"] for item in resolved_genes],
        grouped_values,
        title=title,
        palette=palette,
    )

    summary_bits = [t("runtime.plotViolinSummary", label=target.label, genes=state.format_list_zh([item['requested'] for item in resolved_genes]))]
    if groupby:
        summary_bits.append(t("runtime.plotViolinGroupby", groupby=groupby))
    if missing_genes:
        summary_bits.append(t("runtime.missingGenes", genes=state.format_list_zh(missing_genes)))
    return {
        "summary": "".join(summary_bits),
        "artifacts": [
            {
                "kind": "plot",
                "title": t("runtime.plotViolinArtifactTitle", label=target.label),
                "path": str(path),
                "content_type": "image/png",
                "summary": t("runtime.plotViolinArtifactSummary", label=target.label),
            }
        ],
        "metadata": {
            "groupby": groupby or None,
            "genes": requested_genes,
            "resolved_genes": resolved_genes,
            "missing_genes": missing_genes,
            "palette": palette,
        },
    }


def plot_heatmap(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, _, _, np, _ = state.analysis_modules()
    adata = state._load_adata(target)
    requested_genes, resolved_genes, missing_genes = state._resolve_gene_keys(
        adata,
        ctx.params.get("genes"),
        require_at_least_one=True,
    )
    requested_groupby = str(ctx.params.get("groupby") or "").strip()
    if requested_groupby:
        if requested_groupby not in adata.obs.columns:
            raise RuntimeError(t("error.missingObsField", field=requested_groupby))
        groupby = requested_groupby
    else:
        try:
            groupby = state._categorical_field(target, adata, None)
        except RuntimeError:
            groupby = ""

    gene_keys = [item["feature_key"] for item in resolved_genes]
    expression = state._dense_matrix(adata[:, gene_keys].X)
    if groupby:
        categories = adata.obs[groupby].astype("category")
        codes = categories.cat.codes.to_numpy()
        group_labels = [str(item) for item in categories.cat.categories]
        mean_values = []
        for index in range(len(group_labels)):
            subset = expression[codes == index]
            if subset.shape[0] == 0:
                mean_values.append(np.zeros(len(gene_keys), dtype=float))
            else:
                mean_values.append(np.asarray(subset.mean(axis=0), dtype=float).reshape(-1))
        heatmap_values = np.vstack(mean_values)
    else:
        group_labels = ["all cells"]
        heatmap_values = np.asarray(expression.mean(axis=0), dtype=float).reshape(1, -1)

    title = str(ctx.params.get("title") or "").strip() or None
    palette = str(ctx.params.get("palette") or "").strip() or None
    path = state._plot_path(ctx.workspace_root, ctx.skill, target.label, ctx.request_id)
    state._save_group_heatmap(
        path,
        group_labels,
        [item["requested"] for item in resolved_genes],
        heatmap_values,
        title=title,
        palette=palette,
    )

    summary_bits = [t("runtime.plotHeatmapSummary", label=target.label, genes=state.format_list_zh([item['requested'] for item in resolved_genes]))]
    if groupby:
        summary_bits.append(t("runtime.plotHeatmapGroupby", groupby=groupby))
    if missing_genes:
        summary_bits.append(t("runtime.missingGenes", genes=state.format_list_zh(missing_genes)))
    return {
        "summary": "".join(summary_bits),
        "artifacts": [
            {
                "kind": "plot",
                "title": t("runtime.plotHeatmapArtifactTitle", label=target.label),
                "path": str(path),
                "content_type": "image/png",
                "summary": t("runtime.plotHeatmapArtifactSummary", label=target.label),
            }
        ],
        "metadata": {
            "groupby": groupby or None,
            "genes": requested_genes,
            "resolved_genes": resolved_genes,
            "missing_genes": missing_genes,
        },
    }


def plot_celltype_composition(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    import pandas as pd

    target = require_target(state, ctx)
    adata = state._load_adata(target)
    groupby = str(ctx.params.get("groupby") or "").strip()
    split_by = str(ctx.params.get("split_by") or "").strip()
    if groupby == "" or split_by == "":
        raise RuntimeError(t("error.compositionMissingParams"))
    if groupby not in adata.obs.columns:
        raise RuntimeError(t("error.missingObsField", field=groupby))
    if split_by not in adata.obs.columns:
        raise RuntimeError(t("error.missingObsField", field=split_by))

    composition = pd.crosstab(
        adata.obs[split_by].astype(str),
        adata.obs[groupby].astype(str),
        normalize="index",
    ) * 100.0
    if composition.empty:
        raise RuntimeError(t("error.compositionNoData"))

    title = str(ctx.params.get("title") or "").strip() or None
    path = state._plot_path(ctx.workspace_root, ctx.skill, target.label, ctx.request_id)
    state._save_stacked_bar_plot(path, composition, title=title)
    return {
        "summary": t("runtime.plotCompositionSummary", label=target.label, groupby=groupby, split_by=split_by),
        "artifacts": [
            {
                "kind": "plot",
                "title": t("runtime.plotCompositionArtifactTitle", label=target.label),
                "path": str(path),
                "content_type": "image/png",
                "summary": t("runtime.plotCompositionArtifactSummary", label=target.label, split_by=split_by, groupby=groupby),
            }
        ],
        "metadata": {
            "groupby": groupby,
            "split_by": split_by,
            "n_groups": int(len(composition.columns)),
            "n_splits": int(len(composition.index)),
        },
    }


HANDLERS = {
    "plot_umap": plot_umap,
    "plot_gene_umap": plot_gene_umap,
    "plot_dotplot": plot_dotplot,
    "plot_violin": plot_violin,
    "plot_heatmap": plot_heatmap,
    "plot_celltype_composition": plot_celltype_composition,
}
