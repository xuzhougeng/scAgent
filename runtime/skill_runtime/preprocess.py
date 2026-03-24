from __future__ import annotations

from typing import Any

from i18n import t

from .base import SkillExecutionContext
from .common import require_target


def filter_cells(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, _, _, np, _ = state.analysis_modules()
    adata = state._load_qc_adata(target)
    state._ensure_qc_metrics(adata, ctx.params.get("mt_prefix"))

    thresholds: dict[str, float] = {}
    if ctx.params.get("min_genes") is not None:
        thresholds["min_genes"] = float(ctx.params["min_genes"])
    if ctx.params.get("max_genes") is not None:
        thresholds["max_genes"] = float(ctx.params["max_genes"])
    if ctx.params.get("max_mt_pct") is not None:
        thresholds["max_mt_pct"] = float(ctx.params["max_mt_pct"])
    if not thresholds:
        raise RuntimeError(t("error.filterCellsNoThreshold"))

    mask = np.ones(adata.n_obs, dtype=bool)
    if "min_genes" in thresholds:
        mask &= np.asarray(adata.obs["n_genes_by_counts"], dtype=float) >= thresholds["min_genes"]
    if "max_genes" in thresholds:
        mask &= np.asarray(adata.obs["n_genes_by_counts"], dtype=float) <= thresholds["max_genes"]
    if "max_mt_pct" in thresholds:
        mask &= np.asarray(adata.obs["pct_counts_mt"], dtype=float) <= thresholds["max_mt_pct"]

    filtered = adata[mask].copy()
    if filtered.n_obs == 0:
        raise RuntimeError(t("error.filterCellsEmpty"))

    removed = adata.n_obs - filtered.n_obs
    threshold_bits = [f"{name}={value:g}" for name, value in thresholds.items()]
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"filtered_cells_{target.label}",
        kind="filtered_dataset",
        adata=filtered,
        summary=t("runtime.filterCellsSummary", label=target.label, thresholds=', '.join(threshold_bits), kept=filtered.n_obs, removed=removed),
        request_id=ctx.request_id,
    )


def filter_genes(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, _, _, np, sp = state.analysis_modules()
    adata = state._load_qc_adata(target)

    thresholds: dict[str, float] = {}
    if ctx.params.get("min_cells") is not None:
        thresholds["min_cells"] = float(ctx.params["min_cells"])
    if ctx.params.get("min_counts") is not None:
        thresholds["min_counts"] = float(ctx.params["min_counts"])
    if not thresholds:
        raise RuntimeError(t("error.filterGenesNoThreshold"))

    matrix = adata.X
    if sp.issparse(matrix):
        detected_cells = np.asarray((matrix > 0).sum(axis=0)).reshape(-1)
        total_counts = np.asarray(matrix.sum(axis=0)).reshape(-1)
    else:
        dense = np.asarray(matrix)
        detected_cells = (dense > 0).sum(axis=0)
        total_counts = dense.sum(axis=0)

    gene_mask = np.ones(adata.n_vars, dtype=bool)
    if "min_cells" in thresholds:
        gene_mask &= detected_cells >= thresholds["min_cells"]
    if "min_counts" in thresholds:
        gene_mask &= total_counts >= thresholds["min_counts"]

    filtered = adata[:, gene_mask].copy()
    if filtered.n_vars == 0:
        raise RuntimeError(t("error.filterGenesEmpty"))

    removed = adata.n_vars - filtered.n_vars
    threshold_bits = [f"{name}={value:g}" for name, value in thresholds.items()]
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"filtered_genes_{target.label}",
        kind="filtered_dataset",
        adata=filtered,
        summary=t("runtime.filterGenesSummary", label=target.label, thresholds=', '.join(threshold_bits), kept=filtered.n_vars, removed=removed),
        request_id=ctx.request_id,
    )


def normalize_total(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, sc, _, _, _ = state.analysis_modules()
    adata = state._load_counts_adata(target)
    target_sum = float(ctx.params.get("target_sum") or 1e4)
    sc.pp.normalize_total(adata, target_sum=target_sum)
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"normalized_{target.label}",
        kind=state._default_kind_after_processing(target),
        adata=adata,
        summary=t("runtime.normalizeTotalSummary", label=target.label, target_sum=f"{target_sum:g}"),
        request_id=ctx.request_id,
    )


def log1p_transform(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, sc, _, _, _ = state.analysis_modules()
    adata = state._load_counts_adata(target)
    sc.pp.log1p(adata)
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"log1p_{target.label}",
        kind=state._default_kind_after_processing(target),
        adata=adata,
        summary=t("runtime.log1pSummary", label=target.label),
        request_id=ctx.request_id,
    )


def select_hvg(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, sc, _, _, _ = state.analysis_modules()
    adata = state._load_adata(target)
    assessment = (target.metadata or {}).get("assessment") or {}
    needs_recipe = (
        target.kind == "raw_dataset"
        or assessment.get("preprocessing_state") == "raw_like"
        or state.matrix_has_negative_values(adata.X)
    )
    if needs_recipe:
        adata = state._load_counts_adata(target)
        sc.pp.normalize_total(adata, target_sum=1e4)
        sc.pp.log1p(adata)

    n_top_genes = int(ctx.params.get("n_top_genes") or 2000)
    flavor = str(ctx.params.get("flavor") or "seurat")
    sc.pp.highly_variable_genes(adata, n_top_genes=n_top_genes, flavor=flavor, subset=False)
    n_hvg = int(adata.var.get("highly_variable", []).sum()) if "highly_variable" in adata.var else 0
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"hvg_{target.label}",
        kind=state._default_kind_after_processing(target),
        adata=adata,
        summary=t("runtime.selectHvgSummary", label=target.label, n_top_genes=n_top_genes, n_hvg=n_hvg),
        request_id=ctx.request_id,
    )


def scale_matrix(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, sc, _, _, _ = state.analysis_modules()
    adata = state._load_adata(target)
    max_value = ctx.params.get("max_value")
    if max_value is not None:
        sc.pp.scale(adata, max_value=float(max_value))
        summary = t("runtime.scaleMatrixSummaryWithMax", label=target.label, max_value=f"{float(max_value):g}")
    else:
        sc.pp.scale(adata)
        summary = t("runtime.scaleMatrixSummary", label=target.label)

    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"scaled_{target.label}",
        kind=state._default_kind_after_processing(target),
        adata=adata,
        summary=summary,
        request_id=ctx.request_id,
    )


def run_pca(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, sc, _, _, _ = state.analysis_modules()
    adata = state._load_adata(target)
    n_comps = int(ctx.params.get("n_comps") or 30)
    if "highly_variable" in adata.var and bool(adata.var["highly_variable"].sum()):
        adata = adata[:, adata.var["highly_variable"]].copy()
    max_comps = max(2, min(n_comps, adata.n_obs - 1, adata.n_vars - 1))
    sc.pp.pca(adata, n_comps=max_comps)
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"pca_{target.label}",
        kind=state._default_kind_after_processing(target),
        adata=adata,
        summary=t("runtime.runPcaSummary", label=target.label, n_comps=max_comps),
        request_id=ctx.request_id,
    )


def compute_neighbors(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, sc, _, _, _ = state.analysis_modules()
    adata = state._load_adata(target)
    if "X_pca" not in adata.obsm:
        raise RuntimeError(t("error.missingPCA"))

    n_neighbors = int(ctx.params.get("n_neighbors") or 15)
    use_rep = ctx.params.get("use_rep")
    if use_rep:
        sc.pp.neighbors(adata, n_neighbors=n_neighbors, use_rep=str(use_rep))
    else:
        sc.pp.neighbors(adata, n_neighbors=n_neighbors, n_pcs=min(30, adata.obsm["X_pca"].shape[1]))

    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"neighbors_{target.label}",
        kind=state._default_kind_after_processing(target),
        adata=adata,
        summary=t("runtime.computeNeighborsSummary", label=target.label, n_neighbors=n_neighbors),
        request_id=ctx.request_id,
    )


def run_umap(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, sc, _, _, _ = state.analysis_modules()
    adata = state._load_adata(target)
    if "neighbors" not in adata.uns and "connectivities" not in getattr(adata, "obsp", {}):
        raise RuntimeError(t("error.missingNeighborGraph"))

    min_dist = float(ctx.params.get("min_dist") or 0.5)
    sc.tl.umap(adata, min_dist=min_dist)
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"umap_{target.label}",
        kind=state._default_kind_after_processing(target),
        adata=adata,
        summary=t("runtime.runUmapSummary", label=target.label, min_dist=f"{min_dist:g}"),
        request_id=ctx.request_id,
    )


def prepare_umap(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, sc, _, _, _ = state.analysis_modules()
    adata = state._load_counts_adata(target)
    sc.pp.normalize_total(adata, target_sum=1e4)
    sc.pp.log1p(adata)
    sc.pp.highly_variable_genes(adata, n_top_genes=2000, flavor="seurat", subset=True)
    sc.pp.pca(adata, n_comps=min(30, adata.n_obs - 1, adata.n_vars - 1))
    sc.pp.neighbors(adata, n_neighbors=15, n_pcs=min(30, adata.obsm["X_pca"].shape[1]))
    sc.tl.umap(adata)
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"prepared_{target.label}",
        kind=state._default_kind_after_processing(target),
        adata=adata,
        summary=t("runtime.prepareUmapSummary", label=target.label),
        request_id=ctx.request_id,
    )


HANDLERS = {
    "filter_cells": filter_cells,
    "filter_genes": filter_genes,
    "normalize_total": normalize_total,
    "log1p_transform": log1p_transform,
    "select_hvg": select_hvg,
    "scale_matrix": scale_matrix,
    "run_pca": run_pca,
    "compute_neighbors": compute_neighbors,
    "run_umap": run_umap,
    "prepare_umap": prepare_umap,
}
