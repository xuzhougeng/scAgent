from __future__ import annotations

from typing import Any

from i18n import t

from .base import SkillExecutionContext
from .common import require_target


def subset_cells(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    adata = state._load_adata(target)
    obs_field = str(ctx.params.get("obs_field") or "").strip()
    op = str(ctx.params.get("op") or "eq").strip()
    value = ctx.params.get("value")
    mask = state._build_obs_mask(adata, obs_field, op, value)

    subset = adata[mask].copy()
    if subset.n_obs == 0:
        raise RuntimeError(t("error.subsetEmpty"))

    subset_label = f"subset_{obs_field}_{state.slug(str(value)) or 'selected'}"
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=subset_label,
        kind="subset",
        adata=subset,
        summary=t("runtime.subsetCellsSummary", label=target.label, n_obs=subset.n_obs, subset_label=subset_label),
        request_id=ctx.request_id,
    )


def subcluster_from_global(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    adata = state._load_counts_adata(target)
    obs_field = str(ctx.params.get("obs_field") or "").strip()
    op = str(ctx.params.get("op") or "eq").strip()
    value = ctx.params.get("value")
    mask = state._build_obs_mask(adata, obs_field, op, value)
    subset = adata[mask].copy()
    if subset.n_obs == 0:
        raise RuntimeError(t("error.subclusterSubsetEmpty"))

    analyzed_subset, workflow = state._run_subcluster_workflow(subset, ctx.params)
    subset_label = f"subcluster_{obs_field}_{state.slug(str(value)) or 'selected'}"
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=subset_label,
        kind="reclustered_subset",
        adata=analyzed_subset,
        summary=(
            t("runtime.subclusterFromGlobalSummary",
              label=target.label, obs_field=obs_field, value=value,
              n_obs=analyzed_subset.n_obs, resolution=workflow['resolution'])
        ),
        request_id=ctx.request_id,
    )


def score_gene_set(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, sc, _, _, _ = state.analysis_modules()
    adata = state._load_adata(target)
    requested_genes, resolved_genes, missing_genes = state._resolve_gene_keys(
        adata,
        ctx.params.get("genes"),
        require_at_least_one=True,
    )
    score_name = (
        str(ctx.params.get("score_name") or "").strip()
        or f"score_{state.slug('_'.join(item['requested'] for item in resolved_genes[:4])) or 'gene_set'}"
    )
    sc.tl.score_genes(
        adata,
        gene_list=[item["feature_key"] for item in resolved_genes],
        score_name=score_name,
        use_raw=adata.raw is not None,
    )

    summary_bits = [t("runtime.scoreGeneSetSummary", label=target.label, score_name=score_name)]
    if missing_genes:
        summary_bits.append(t("runtime.missingGenes", genes=state.format_list_zh(missing_genes)))
    persisted = state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"scored_{target.label}",
        kind=state._default_kind_after_processing(target),
        adata=adata,
        summary="".join(summary_bits),
        request_id=ctx.request_id,
    )
    persisted["metadata"] = {
        "score_name": score_name,
        "genes": requested_genes,
        "resolved_genes": resolved_genes,
        "missing_genes": missing_genes,
    }
    return persisted


def recluster(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, sc, _, _, _ = state.analysis_modules()
    adata = state._load_adata(target)
    if "X_pca" not in adata.obsm:
        raise RuntimeError(t("error.missingPCAForRecluster"))
    if "neighbors" not in adata.uns and "connectivities" not in getattr(adata, "obsp", {}):
        sc.pp.neighbors(adata, n_neighbors=15, n_pcs=min(30, adata.obsm["X_pca"].shape[1]))

    resolution = ctx.params.get("resolution", 0.6)
    sc.tl.leiden(adata, resolution=float(resolution), key_added="leiden")
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"reclustered_{target.label}",
        kind="reclustered_subset",
        adata=adata,
        summary=t("runtime.reclusterSummary", label=target.label, resolution=resolution),
        request_id=ctx.request_id,
    )


def reanalyze_subset(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    adata = state._load_counts_adata(target)
    analyzed_subset, workflow = state._run_subcluster_workflow(adata, ctx.params)
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"reanalyzed_{target.label}",
        kind="reclustered_subset",
        adata=analyzed_subset,
        summary=(
            t("runtime.reanalyzeSubsetSummary",
              label=target.label, resolution=workflow['resolution'])
        ),
        request_id=ctx.request_id,
    )


def subcluster_group(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    adata = state._load_counts_adata(target)
    groupby = str(ctx.params.get("groupby") or "").strip()
    groups = ctx.params.get("groups")
    if groupby == "":
        raise RuntimeError(t("error.subclusterGroupRequiresGroupby"))
    if not isinstance(groups, list) or not groups:
        raise RuntimeError(t("error.subclusterGroupRequiresGroups"))

    mask = state._build_obs_mask(adata, groupby, "in", groups)
    subset = adata[mask].copy()
    if subset.n_obs == 0:
        raise RuntimeError(t("error.subclusterGroupSubsetEmpty"))

    analyzed_subset, workflow = state._run_subcluster_workflow(subset, ctx.params)
    group_label = state.slug("_".join(str(item) for item in groups)) or "selected"
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"subcluster_{groupby}_{group_label}",
        kind="reclustered_subset",
        adata=analyzed_subset,
        summary=(
            t("runtime.subclusterGroupSummary",
              label=target.label, groupby=groupby,
              groups=state.format_list_zh([str(item) for item in groups]),
              n_obs=analyzed_subset.n_obs, resolution=workflow['resolution'])
        ),
        request_id=ctx.request_id,
    )


def rename_clusters(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    adata = state._load_adata(target)
    groupby = str(ctx.params.get("groupby") or "").strip()
    mapping = ctx.params.get("mapping")
    if groupby == "":
        raise RuntimeError(t("error.renameClustersRequiresGroupby"))
    if groupby not in adata.obs.columns:
        raise RuntimeError(t("error.missingObsField", field=groupby))
    if not isinstance(mapping, dict) or not mapping:
        raise RuntimeError(t("error.renameClustersRequiresMapping"))

    renamed = adata.obs[groupby].astype(str).replace({str(key): str(value) for key, value in mapping.items()})
    adata.obs[groupby] = renamed.astype("category")
    return state._persist_adata_object(
        session_id=ctx.session_id,
        workspace_root=ctx.workspace_root,
        label=f"renamed_{target.label}",
        kind=target.kind,
        adata=adata,
        summary=t("runtime.renameClustersSummary", label=target.label, groupby=groupby, count=len(mapping)),
        request_id=ctx.request_id,
    )


def find_markers(state: Any, ctx: SkillExecutionContext) -> dict[str, Any]:
    target = require_target(state, ctx)
    _, sc, _, _, _ = state.analysis_modules()
    adata = state._load_adata(target)
    groupby = state._cluster_field(target, adata, str(ctx.params.get("groupby") or ""))
    adata.obs[groupby] = adata.obs[groupby].astype("category")
    path = state._artifact_path(ctx.workspace_root, f"markers_{target.label}", "csv", ctx.request_id)
    sc.tl.rank_genes_groups(adata, groupby=groupby, method="wilcoxon", use_raw=adata.raw is not None)
    markers = sc.get.rank_genes_groups_df(adata, group=None)
    state._write_table_atomic(markers, path, index=False)
    return {
        "summary": t("runtime.findMarkersSummary", label=target.label, groupby=groupby),
        "artifacts": [
            {
                "kind": "table",
                "title": t("runtime.findMarkersArtifactTitle", label=target.label),
                "path": str(path),
                "content_type": "text/csv",
                "summary": t("runtime.findMarkersArtifactSummary", label=target.label, groupby=groupby),
            }
        ],
        "metadata": {"groupby": groupby},
    }


HANDLERS = {
    "subset_cells": subset_cells,
    "subcluster_from_global": subcluster_from_global,
    "score_gene_set": score_gene_set,
    "recluster": recluster,
    "reanalyze_subset": reanalyze_subset,
    "subcluster_group": subcluster_group,
    "rename_clusters": rename_clusters,
    "find_markers": find_markers,
}
