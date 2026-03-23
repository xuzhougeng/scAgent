from __future__ import annotations

import math
import re
from pathlib import Path
from typing import Any

from .base import SkillExecutionContext
from .plugins import execute_plugin_skill


class SkillRuntimeSupport:
    object_type: type[Any]
    objects: dict[str, Any]

    def _require_target(self, target: Any | None, skill: str) -> Any:
        if target is None:
            raise RuntimeError(f"{skill} 需要一个目标对象")
        return target

    def _load_adata(self, target: Any) -> Any:
        ad, _, _, _, _ = self.analysis_modules()
        adata = ad.read_h5ad(target.materialized_path)
        adata.var_names_make_unique()
        return adata

    def _load_counts_adata(self, target: Any) -> Any:
        adata = self._load_adata(target)
        if self.matrix_has_negative_values(adata.X):
            if adata.raw is None:
                raise RuntimeError("当前对象缺少可用于预处理的原始 counts，请先提供原始矩阵或带 raw 的 h5ad。")
            adata = adata.raw.to_adata()
            adata.var_names_make_unique()
        return adata

    def _load_qc_adata(self, target: Any) -> Any:
        adata = self._load_adata(target)
        assessment = (target.metadata or {}).get("assessment") or {}
        if adata.raw is not None and assessment.get("preprocessing_state") not in {"", "raw_like"}:
            adata = adata.raw.to_adata()
            adata.var_names_make_unique()
        return adata

    def _persist_adata_object(
        self,
        session_id: str,
        workspace_root: Path,
        label: str,
        kind: str,
        adata: Any,
        summary: str,
    ) -> dict[str, Any]:
        backend_ref = self.next_ref(session_id)
        suffix = backend_ref.split(":")[-1]
        materialized_path = workspace_root / "objects" / f"{self.slug(label)}_{self.slug(suffix)}.h5ad"
        materialized_path.parent.mkdir(parents=True, exist_ok=True)
        adata.write_h5ad(materialized_path)

        n_obs, n_vars = self.inspect_h5ad_shape(materialized_path)
        metadata = self.inspect_h5ad_metadata(materialized_path)
        obj = self.object_type(
            backend_ref=backend_ref,
            session_id=session_id,
            label=label,
            kind=kind,
            n_obs=n_obs,
            n_vars=n_vars,
            state="resident",
            in_memory=True,
            materialized_path=str(materialized_path),
            metadata=metadata,
        )
        self.objects[backend_ref] = obj
        return {
            "summary": summary,
            "object": self._descriptor(obj),
        }

    def _cluster_field(self, target: Any, adata: Any, requested: str | None = None) -> str:
        if requested and requested in adata.obs.columns:
            return requested

        cluster_annotation = (target.metadata or {}).get("cluster_annotation") or {}
        field = cluster_annotation.get("field")
        if field and field in adata.obs.columns:
            return str(field)

        for candidate in ("leiden", "louvain", "cluster", "clusters"):
            if candidate in adata.obs.columns:
                return candidate

        raise RuntimeError("当前对象缺少可用的聚类字段。")

    def _categorical_field(self, target: Any, adata: Any, requested: str | None = None) -> str:
        if requested and requested in adata.obs.columns:
            return requested

        metadata = target.metadata or {}
        for annotation_key in ("cluster_annotation", "cell_type_annotation"):
            annotation = metadata.get(annotation_key) or {}
            field = str(annotation.get("field") or "").strip()
            if field and field in adata.obs.columns:
                return field

        for item in metadata.get("categorical_obs_fields", []):
            field = str(item.get("field") or "").strip()
            if field and field in adata.obs.columns:
                return field

        return self._cluster_field(target, adata, requested)

    def _default_kind_after_processing(self, target: Any) -> str:
        if target.kind == "raw_dataset":
            return "filtered_dataset"
        return target.kind

    def _build_obs_mask(self, adata: Any, obs_field: str, op: str, value: Any) -> Any:
        _, _, _, np, _ = self.analysis_modules()
        field_name = str(obs_field or "").strip()
        if field_name == "" or field_name not in adata.obs.columns:
            raise RuntimeError(f"当前对象缺少 obs 字段 `{field_name}`。")

        series = adata.obs[field_name]
        operator = str(op or "eq").strip()
        if operator == "eq":
            mask = series.astype(str) == str(value)
        elif operator == "in":
            values = value if isinstance(value, list) else [value]
            mask = series.astype(str).isin([str(item) for item in values])
        elif operator == "gt":
            mask = series.astype(float) > float(value)
        elif operator == "lt":
            mask = series.astype(float) < float(value)
        else:
            raise RuntimeError(f"不支持的筛选操作符：{operator}")

        if hasattr(mask, "to_numpy"):
            return mask.to_numpy()
        return np.asarray(mask)

    def _run_subcluster_workflow(self, adata: Any, params: dict[str, Any]) -> tuple[Any, dict[str, Any]]:
        _, sc, _, _, _ = self.analysis_modules()
        if adata.n_obs < 3 or adata.n_vars < 3:
            raise RuntimeError("亚群分析至少需要 3 个细胞和 3 个基因。")

        n_top_genes = int(params.get("n_top_genes") or 2000)
        n_neighbors = int(params.get("n_neighbors") or 15)
        min_dist = float(params.get("min_dist") or 0.5)
        resolution = float(params.get("resolution") or 0.6)

        sc.pp.normalize_total(adata, target_sum=1e4)
        sc.pp.log1p(adata)
        sc.pp.highly_variable_genes(adata, n_top_genes=n_top_genes, flavor="seurat", subset=True)
        if adata.n_obs < 3 or adata.n_vars < 3:
            raise RuntimeError("亚群分析后的对象维度过小，无法继续 PCA/邻接图/UMAP。")

        max_comps = min(30, adata.n_obs - 1, adata.n_vars - 1)
        if max_comps < 2:
            raise RuntimeError("亚群分析至少需要两个主成分，请检查筛选后的细胞数和基因数。")

        sc.pp.pca(adata, n_comps=max_comps)
        sc.pp.neighbors(adata, n_neighbors=n_neighbors, n_pcs=min(30, adata.obsm["X_pca"].shape[1]))
        sc.tl.umap(adata, min_dist=min_dist)
        sc.tl.leiden(adata, resolution=resolution, key_added="leiden")

        return adata, {
            "n_top_genes": n_top_genes,
            "n_neighbors": n_neighbors,
            "min_dist": min_dist,
            "resolution": resolution,
            "n_comps": max_comps,
        }

    def _artifact_path(self, workspace_root: Path, stem: str, extension: str, request_id: str | None = None) -> Path:
        artifacts_dir = workspace_root / "artifacts"
        artifacts_dir.mkdir(parents=True, exist_ok=True)

        normalized_extension = extension.lstrip(".").lower() or "bin"
        base_stem = self.slug(stem) or "artifact"
        request_suffix = self.slug(str(request_id or "").strip())
        file_stem = f"{base_stem}_{request_suffix}" if request_suffix else base_stem
        candidate = artifacts_dir / f"{file_stem}.{normalized_extension}"
        duplicate_index = 2
        while candidate.exists():
            candidate = artifacts_dir / f"{file_stem}_{duplicate_index}.{normalized_extension}"
            duplicate_index += 1
        return candidate

    def _plot_path(self, workspace_root: Path, skill: str, label: str, request_id: str | None = None) -> Path:
        return self._artifact_path(workspace_root, f"{skill}_{label}", "png", request_id)

    def _infer_mt_prefix(self, adata: Any, explicit_prefix: Any = None) -> str | None:
        prefix = str(explicit_prefix or "").strip()
        if prefix:
            return prefix

        for candidate in ("MT-", "mt-", "Mt-"):
            if any(str(name).startswith(candidate) for name in adata.var_names):
                return candidate

        for column in ("gene_symbol", "gene_name", "symbol", "feature_name", "features"):
            if column not in adata.var.columns:
                continue
            values = adata.var[column].astype(str)
            for candidate in ("MT-", "mt-", "Mt-"):
                if bool(values.str.startswith(candidate).any()):
                    return candidate
        return None

    def _ensure_qc_metrics(self, adata: Any, mt_prefix: Any = None) -> dict[str, Any]:
        _, sc, _, _, _ = self.analysis_modules()
        prefix = self._infer_mt_prefix(adata, mt_prefix)
        qc_vars: list[str] | None = None
        if prefix is not None:
            adata.var["mt"] = [str(name).startswith(prefix) for name in adata.var_names]
            qc_vars = ["mt"]

        sc.pp.calculate_qc_metrics(adata, qc_vars=qc_vars, percent_top=None, log1p=False, inplace=True)
        if "pct_counts_mt" not in adata.obs.columns:
            adata.obs["pct_counts_mt"] = 0.0
        else:
            adata.obs["pct_counts_mt"] = adata.obs["pct_counts_mt"].fillna(0.0)

        metrics = [metric for metric in ("total_counts", "n_genes_by_counts", "pct_counts_mt") if metric in adata.obs.columns]
        return {
            "metrics": metrics,
            "mt_prefix": prefix,
        }

    def _normalize_qc_metric_names(self, raw_value: Any, available_fields: list[str] | None = None) -> list[str]:
        available = set(available_fields or [])
        if raw_value is None:
            requested: list[str] = []
        elif isinstance(raw_value, str):
            requested = [item for item in re.split(r"[\s,，;；]+", raw_value) if item]
        elif isinstance(raw_value, list):
            requested = [str(item or "").strip() for item in raw_value if str(item or "").strip()]
        else:
            text = str(raw_value or "").strip()
            requested = [text] if text else []

        aliases = {
            "counts": "total_counts",
            "ncounts": "total_counts",
            "totalcounts": "total_counts",
            "umi": "total_counts",
            "umis": "total_counts",
            "genes": "n_genes_by_counts",
            "ngenes": "n_genes_by_counts",
            "detectedgenes": "n_genes_by_counts",
            "genesbycounts": "n_genes_by_counts",
            "ngenesbycounts": "n_genes_by_counts",
            "mt": "pct_counts_mt",
            "mito": "pct_counts_mt",
            "mitochondrial": "pct_counts_mt",
            "pctmt": "pct_counts_mt",
            "percentmt": "pct_counts_mt",
            "mtpct": "pct_counts_mt",
            "mitopct": "pct_counts_mt",
            "mitochondrialpct": "pct_counts_mt",
            "pctcountsmt": "pct_counts_mt",
        }

        normalized: list[str] = []
        for item in requested:
            if item in available:
                normalized.append(item)
                continue
            key = re.sub(r"[^a-z0-9]+", "", item.lower())
            candidate = aliases.get(key)
            if candidate:
                normalized.append(candidate)
                continue
            normalized.append(item)

        if not normalized:
            normalized = ["total_counts", "n_genes_by_counts", "pct_counts_mt"]

        return self.dedupe_list([item for item in normalized if not available or item in available])

    def _metric_stats(self, adata: Any, metric: str) -> dict[str, float] | None:
        _, _, _, np, _ = self.analysis_modules()
        if metric not in adata.obs.columns:
            return None
        values = np.asarray(adata.obs[metric], dtype=float)
        values = values[np.isfinite(values)]
        if values.size == 0:
            return None
        return {
            "min": round(float(values.min()), 4),
            "median": round(float(np.median(values)), 4),
            "mean": round(float(values.mean()), 4),
            "max": round(float(values.max()), 4),
        }

    def _normalize_gene_list(self, raw_value: Any) -> list[str]:
        if raw_value is None:
            return []

        candidates: list[str] = []
        if isinstance(raw_value, str):
            candidates = re.split(r"[\s,，;；]+", raw_value)
        elif isinstance(raw_value, list):
            for item in raw_value:
                text = str(item or "").strip()
                if text:
                    candidates.append(text)
        else:
            text = str(raw_value or "").strip()
            if text:
                candidates.append(text)

        return self.dedupe_list([candidate for candidate in candidates if candidate])

    def _resolve_gene_keys(self, adata: Any, raw_value: Any, *, require_at_least_one: bool = False) -> tuple[list[str], list[dict[str, str]], list[str]]:
        requested = self._normalize_gene_list(raw_value)
        resolved: list[dict[str, str]] = []
        missing: list[str] = []
        seen_keys: set[str] = set()

        for gene in requested:
            gene_key = self._resolve_gene_var_key(adata, gene)
            if gene_key is None:
                missing.append(gene)
                continue
            if gene_key in seen_keys:
                continue
            seen_keys.add(gene_key)
            resolved.append(
                {
                    "requested": gene,
                    "feature_key": gene_key,
                }
            )

        if require_at_least_one and not resolved:
            if missing:
                raise RuntimeError(f"当前对象中未找到请求基因：{self.format_list_zh(missing)}。")
            raise RuntimeError("至少需要一个基因。")

        return requested, resolved, missing

    def _resolve_gene_var_key(self, adata: Any, gene: str) -> str | None:
        requested = str(gene or "").strip()
        if requested == "":
            return None

        if requested in adata.var_names:
            return requested

        requested_lower = requested.lower()
        for candidate in adata.var_names:
            candidate_text = str(candidate)
            if candidate_text.lower() == requested_lower:
                return candidate_text

        for column in ("gene_symbol", "gene_name", "symbol", "feature_name", "features"):
            if column not in adata.var.columns:
                continue
            values = adata.var[column].astype(str)
            exact_matches = values.index[values == requested]
            if len(exact_matches) > 0:
                return str(exact_matches[0])
            casefold_matches = values.index[values.str.lower() == requested_lower]
            if len(casefold_matches) > 0:
                return str(casefold_matches[0])

        return None

    def _dense_vector(self, values: Any) -> Any:
        _, _, _, np, sp = self.analysis_modules()
        if sp.issparse(values):
            return np.asarray(values.toarray()).reshape(-1)
        return np.asarray(values).reshape(-1)

    def _dense_matrix(self, values: Any) -> Any:
        _, _, _, np, sp = self.analysis_modules()
        if sp.issparse(values):
            return np.asarray(values.toarray())
        return np.asarray(values)

    def _resolve_gene_expression(self, adata: Any, gene: str, layer: str | None = None) -> tuple[str, str, Any, str]:
        requested = str(gene or "").strip()
        if requested == "":
            raise RuntimeError("基因名不能为空。")

        layer_name = str(layer or "").strip()
        if layer_name:
            if layer_name not in adata.layers:
                raise RuntimeError(f"当前对象缺少 layer `{layer_name}`。")
            gene_key = self._resolve_gene_var_key(adata, requested)
            if gene_key is None:
                raise RuntimeError(f"当前对象缺少基因 `{requested}`。")
            expression = self._dense_vector(adata[:, [gene_key]].layers[layer_name])
            return requested, gene_key, expression, f"layer:{layer_name}"

        gene_key = self._resolve_gene_var_key(adata, requested)
        if gene_key is not None:
            expression = self._dense_vector(adata[:, [gene_key]].X)
            return requested, gene_key, expression, "X"

        if adata.raw is not None:
            raw_adata = adata.raw.to_adata()
            raw_adata.var_names_make_unique()
            raw_gene_key = self._resolve_gene_var_key(raw_adata, requested)
            if raw_gene_key is not None:
                expression = self._dense_vector(raw_adata[:, [raw_gene_key]].X)
                return requested, raw_gene_key, expression, "raw"

        raise RuntimeError(f"当前对象缺少基因 `{requested}`。")

    def _normalize_legend_loc(self, raw_value: Any) -> str:
        value = str(raw_value or "best").strip().lower().replace("_", " ")
        if value in {"on data", "best", "right", "none"}:
            return value
        return "best"

    def _coerce_positive_float(self, raw_value: Any, default: float) -> float:
        try:
            value = float(raw_value)
        except (TypeError, ValueError):
            return default
        if not math.isfinite(value) or value <= 0:
            return default
        return value

    def _categorical_palette_colors(self, plt: Any, np: Any, categories: list[str], palette: str | None) -> list[Any]:
        cmap_name = str(palette or "tab20")
        try:
            cmap = plt.get_cmap(cmap_name)
        except ValueError:
            cmap = plt.get_cmap("tab20")

        if len(categories) <= 1:
            return [cmap(0.5)]
        return [cmap(position) for position in np.linspace(0.0, 1.0, len(categories))]

    def _render_categorical_umap_legend(
        self,
        ax: Any,
        plt: Any,
        np: Any,
        coords: Any,
        codes: Any,
        categories: list[str],
        colors: list[Any],
        color_by: str,
        legend_loc: str,
    ) -> None:
        if legend_loc == "none":
            return

        if legend_loc == "on data":
            for index, label in enumerate(categories):
                mask = codes == index
                if not mask.any():
                    continue
                subset = coords[mask]
                center_x = float(np.median(subset[:, 0]))
                center_y = float(np.median(subset[:, 1]))
                ax.text(
                    center_x,
                    center_y,
                    str(label),
                    fontsize=8,
                    ha="center",
                    va="center",
                    color=colors[index],
                    bbox={
                        "boxstyle": "round,pad=0.2",
                        "facecolor": "white",
                        "edgecolor": colors[index],
                        "alpha": 0.85,
                    },
                )
            return

        from matplotlib.lines import Line2D

        handles = [
            Line2D([0], [0], marker="o", linestyle="", markersize=6, markerfacecolor=colors[index], markeredgecolor="none")
            for index in range(len(categories))
        ]
        legend_kwargs = {
            "title": color_by,
            "frameon": False,
            "fontsize": 7,
        }
        if legend_loc == "right":
            legend_kwargs["loc"] = "center left"
            legend_kwargs["bbox_to_anchor"] = (1.02, 0.5)
        else:
            legend_kwargs["loc"] = "best"
        ax.legend(handles, categories, **legend_kwargs)

    def _save_umap_plot(
        self,
        adata: Any,
        path: Path,
        color_by: str | None,
        *,
        legend_loc: str = "best",
        palette: str | None = None,
        title: str | None = None,
        point_size: float = 8.0,
        figure_width: float = 6.2,
        figure_height: float = 4.8,
    ) -> None:
        _, _, plt, np, _ = self.analysis_modules()
        coords = adata.obsm.get("X_umap")
        if coords is None or len(coords.shape) != 2 or coords.shape[1] < 2:
            raise RuntimeError("当前对象缺少 `X_umap`，请先执行 run_umap。")

        legend_loc = self._normalize_legend_loc(legend_loc)
        point_size = self._coerce_positive_float(point_size, 8.0)
        figure_width = self._coerce_positive_float(figure_width, 6.2)
        figure_height = self._coerce_positive_float(figure_height, 4.8)
        figure_title = str(title or "UMAP").strip() or "UMAP"

        fig, ax = plt.subplots(figsize=(figure_width, figure_height))
        if color_by and color_by in adata.obs.columns:
            series = adata.obs[color_by]
            if getattr(series.dtype, "kind", "") in {"i", "u", "f"}:
                cmap_name = str(palette or "viridis")
                try:
                    scatter = ax.scatter(coords[:, 0], coords[:, 1], c=series.to_numpy(), s=point_size, cmap=cmap_name, linewidths=0)
                except ValueError:
                    scatter = ax.scatter(coords[:, 0], coords[:, 1], c=series.to_numpy(), s=point_size, cmap="viridis", linewidths=0)
                if legend_loc != "none":
                    fig.colorbar(scatter, ax=ax, fraction=0.045, pad=0.04)
            else:
                categories = series.astype("category")
                codes = categories.cat.codes.to_numpy()
                category_labels = [str(item) for item in categories.cat.categories]
                colors = self._categorical_palette_colors(plt, np, category_labels, palette)
                point_colors = [colors[max(code, 0)] if 0 <= code < len(colors) else (0.7, 0.7, 0.7, 0.8) for code in codes]
                ax.scatter(coords[:, 0], coords[:, 1], c=point_colors, s=point_size, linewidths=0)
                self._render_categorical_umap_legend(ax, plt, np, coords, codes, category_labels, colors, color_by, legend_loc)
        else:
            ax.scatter(coords[:, 0], coords[:, 1], s=point_size, c="#2f7d4a", alpha=0.85, linewidths=0)

        ax.set_title(figure_title)
        ax.set_xlabel("UMAP1")
        ax.set_ylabel("UMAP2")
        ax.spines["top"].set_visible(False)
        ax.spines["right"].set_visible(False)
        fig.tight_layout()
        fig.savefig(path, format="png", dpi=180, bbox_inches="tight", facecolor="white")
        plt.close(fig)

    def _save_gene_umap_plot(
        self,
        adata: Any,
        path: Path,
        gene_label: str,
        expression_values: Any,
        *,
        title: str | None = None,
        point_size: float = 8.0,
        figure_width: float = 6.2,
        figure_height: float = 4.8,
        palette: str | None = None,
    ) -> None:
        _, _, plt, np, _ = self.analysis_modules()
        coords = adata.obsm.get("X_umap")
        if coords is None or len(coords.shape) != 2 or coords.shape[1] < 2:
            raise RuntimeError("当前对象缺少 `X_umap`，请先执行 run_umap。")

        point_size = self._coerce_positive_float(point_size, 8.0)
        figure_width = self._coerce_positive_float(figure_width, 6.2)
        figure_height = self._coerce_positive_float(figure_height, 4.8)
        figure_title = str(title or f"UMAP: {gene_label}").strip() or f"UMAP: {gene_label}"
        values = np.nan_to_num(np.asarray(expression_values, dtype=float), nan=0.0, posinf=0.0, neginf=0.0)

        fig, ax = plt.subplots(figsize=(figure_width, figure_height))
        cmap_name = str(palette or "viridis")
        try:
            scatter = ax.scatter(coords[:, 0], coords[:, 1], c=values, s=point_size, cmap=cmap_name, linewidths=0)
        except ValueError:
            scatter = ax.scatter(coords[:, 0], coords[:, 1], c=values, s=point_size, cmap="viridis", linewidths=0)
        colorbar = fig.colorbar(scatter, ax=ax, fraction=0.045, pad=0.04)
        colorbar.set_label(str(gene_label))

        ax.set_title(figure_title)
        ax.set_xlabel("UMAP1")
        ax.set_ylabel("UMAP2")
        ax.spines["top"].set_visible(False)
        ax.spines["right"].set_visible(False)
        fig.tight_layout()
        fig.savefig(path, format="png", dpi=180, bbox_inches="tight", facecolor="white")
        plt.close(fig)

    def _save_qc_metrics_plot(self, adata: Any, path: Path, metrics: list[str], *, title: str | None = None) -> None:
        _, _, plt, np, _ = self.analysis_modules()
        figure_title = str(title or "QC metrics").strip() or "QC metrics"
        figure_width = max(5.4, len(metrics) * 3.2)
        fig, axes = plt.subplots(1, len(metrics), figsize=(figure_width, 3.8))
        if len(metrics) == 1:
            axes = [axes]

        for axis, metric in zip(axes, metrics):
            values = np.asarray(adata.obs[metric], dtype=float)
            values = values[np.isfinite(values)]
            bins = min(40, max(10, int(np.sqrt(max(len(values), 1)))))
            axis.hist(values, bins=bins, color="#2f7d4a", alpha=0.85, edgecolor="white")
            if values.size > 0:
                median = float(np.median(values))
                axis.axvline(median, color="#c44e52", linestyle="--", linewidth=1.1)
            axis.set_title(metric)
            axis.set_xlabel(metric)
            axis.set_ylabel("Cells")
            axis.spines["top"].set_visible(False)
            axis.spines["right"].set_visible(False)

        fig.suptitle(figure_title)
        fig.tight_layout()
        fig.savefig(path, format="png", dpi=180, bbox_inches="tight", facecolor="white")
        plt.close(fig)

    def _save_dotplot(
        self,
        path: Path,
        group_labels: list[str],
        gene_labels: list[str],
        mean_values: Any,
        pct_values: Any,
        *,
        title: str | None = None,
        palette: str | None = None,
    ) -> None:
        _, _, plt, np, _ = self.analysis_modules()
        figure_title = str(title or "Dotplot").strip() or "Dotplot"
        fig, ax = plt.subplots(
            figsize=(max(5.8, len(gene_labels) * 0.72 + 2.8), max(4.2, len(group_labels) * 0.48 + 1.8))
        )

        xs = np.tile(np.arange(len(gene_labels)), len(group_labels))
        ys = np.repeat(np.arange(len(group_labels)), len(gene_labels))
        colors = np.asarray(mean_values, dtype=float).reshape(-1)
        pct = np.clip(np.asarray(pct_values, dtype=float).reshape(-1), 0.0, 100.0)
        sizes = pct * 4.5 + 12.0

        cmap_name = str(palette or "viridis")
        try:
            scatter = ax.scatter(xs, ys, s=sizes, c=colors, cmap=cmap_name, edgecolors="none")
        except ValueError:
            scatter = ax.scatter(xs, ys, s=sizes, c=colors, cmap="viridis", edgecolors="none")

        ax.set_xticks(np.arange(len(gene_labels)), gene_labels, rotation=45, ha="right")
        ax.set_yticks(np.arange(len(group_labels)), group_labels)
        ax.invert_yaxis()
        ax.set_xlabel("Genes")
        ax.set_ylabel("Groups")
        ax.set_title(figure_title)
        ax.grid(axis="x", linestyle=":", linewidth=0.5, alpha=0.4)
        ax.spines["top"].set_visible(False)
        ax.spines["right"].set_visible(False)

        colorbar = fig.colorbar(scatter, ax=ax, fraction=0.045, pad=0.04)
        colorbar.set_label("Mean expression")

        for value in (25, 50, 75):
            ax.scatter([], [], s=value * 4.5 + 12.0, c="#b8c2cc", label=f"{value}%")
        ax.legend(title="% expressing", frameon=False, loc="upper left", bbox_to_anchor=(1.02, 1.0))

        fig.tight_layout()
        fig.savefig(path, format="png", dpi=180, bbox_inches="tight", facecolor="white")
        plt.close(fig)

    def _save_group_heatmap(
        self,
        path: Path,
        group_labels: list[str],
        gene_labels: list[str],
        values: Any,
        *,
        title: str | None = None,
        palette: str | None = None,
    ) -> None:
        _, _, plt, np, _ = self.analysis_modules()
        figure_title = str(title or "Heatmap").strip() or "Heatmap"
        fig, ax = plt.subplots(
            figsize=(max(5.2, len(gene_labels) * 0.62 + 2.6), max(4.0, len(group_labels) * 0.46 + 1.8))
        )

        cmap_name = str(palette or "viridis")
        try:
            image = ax.imshow(np.asarray(values, dtype=float), aspect="auto", cmap=cmap_name)
        except ValueError:
            image = ax.imshow(np.asarray(values, dtype=float), aspect="auto", cmap="viridis")

        ax.set_xticks(np.arange(len(gene_labels)), gene_labels, rotation=45, ha="right")
        ax.set_yticks(np.arange(len(group_labels)), group_labels)
        ax.set_xlabel("Genes")
        ax.set_ylabel("Groups")
        ax.set_title(figure_title)
        ax.spines["top"].set_visible(False)
        ax.spines["right"].set_visible(False)

        colorbar = fig.colorbar(image, ax=ax, fraction=0.045, pad=0.04)
        colorbar.set_label("Mean expression")

        fig.tight_layout()
        fig.savefig(path, format="png", dpi=180, bbox_inches="tight", facecolor="white")
        plt.close(fig)

    def _save_violin_plot(
        self,
        path: Path,
        group_labels: list[str],
        gene_labels: list[str],
        grouped_values: list[list[Any]],
        *,
        title: str | None = None,
    ) -> None:
        _, _, plt, np, _ = self.analysis_modules()
        figure_title = str(title or "Violin plot").strip() or "Violin plot"
        fig, axes = plt.subplots(
            len(gene_labels),
            1,
            figsize=(max(6.2, len(group_labels) * 0.72 + 2.5), max(3.4, len(gene_labels) * 2.6)),
            squeeze=False,
        )
        positions = np.arange(1, len(group_labels) + 1)

        for index, gene_label in enumerate(gene_labels):
            axis = axes[index][0]
            values = []
            for group_values in grouped_values[index]:
                numeric = np.asarray(group_values, dtype=float)
                numeric = numeric[np.isfinite(numeric)]
                values.append(numeric if numeric.size > 0 else np.asarray([0.0]))

            violin = axis.violinplot(values, positions=positions, showmedians=True, showextrema=False)
            for body in violin["bodies"]:
                body.set_facecolor("#2f7d4a")
                body.set_edgecolor("none")
                body.set_alpha(0.72)
            if "cmedians" in violin:
                violin["cmedians"].set_color("#1f2933")
                violin["cmedians"].set_linewidth(1.1)

            axis.set_title(gene_label, loc="left", fontsize=10)
            axis.set_ylabel("Expression")
            axis.set_xticks(
                positions,
                group_labels if index == len(gene_labels) - 1 else [""] * len(group_labels),
                rotation=45,
                ha="right",
            )
            axis.spines["top"].set_visible(False)
            axis.spines["right"].set_visible(False)

        fig.suptitle(figure_title)
        fig.tight_layout()
        fig.savefig(path, format="png", dpi=180, bbox_inches="tight", facecolor="white")
        plt.close(fig)

    def _save_stacked_bar_plot(self, path: Path, composition: Any, *, title: str | None = None) -> None:
        _, _, plt, np, _ = self.analysis_modules()
        figure_title = str(title or "Composition").strip() or "Composition"
        fig, ax = plt.subplots(figsize=(max(6.2, len(composition.index) * 0.85 + 2.5), 4.8))
        x = np.arange(len(composition.index))
        bottom = np.zeros(len(composition.index), dtype=float)

        cmap = plt.get_cmap("tab20")
        denominator = max(1, len(composition.columns) - 1)
        for index, column in enumerate(composition.columns):
            values = composition[column].to_numpy(dtype=float)
            color = cmap(index / denominator)
            ax.bar(x, values, width=0.72, bottom=bottom, label=str(column), color=color)
            bottom += values

        ax.set_xticks(x, [str(item) for item in composition.index], rotation=45, ha="right")
        ax.set_ylim(0, 100)
        ax.set_ylabel("Fraction of cells (%)")
        ax.set_xlabel("Groups")
        ax.set_title(figure_title)
        ax.spines["top"].set_visible(False)
        ax.spines["right"].set_visible(False)
        ax.legend(title="Cell groups", frameon=False, loc="upper left", bbox_to_anchor=(1.02, 1.0))

        fig.tight_layout()
        fig.savefig(path, format="png", dpi=180, bbox_inches="tight", facecolor="white")
        plt.close(fig)

    def _save_custom_figure(self, figure: Any, workspace_root: Path, stem: str, request_id: str | None = None) -> Path:
        path = self._artifact_path(workspace_root, stem, "png", request_id)
        figure.savefig(path, format="png", dpi=180, bbox_inches="tight", facecolor="white")
        _, _, plt, _, _ = self.analysis_modules()
        plt.close(figure)
        return path

    def _save_custom_table(self, table: Any, workspace_root: Path, stem: str, request_id: str | None = None) -> Path:
        path = self._artifact_path(workspace_root, stem, "csv", request_id)
        table.to_csv(path, index=False)
        return path

    def _table_source_path(self, target: Any) -> Path:
        candidates = [target.materialized_path]
        metadata = target.metadata or {}
        for key in ("csv_path", "table_path", "source_table_path", "artifact_path"):
            value = str(metadata.get(key) or "").strip()
            if value:
                candidates.append(value)

        for candidate in candidates:
            path = Path(str(candidate or "").strip())
            if path.exists() and path.is_file():
                return path
        raise RuntimeError(f"当前结果对象 `{target.label}` 缺少可导出的表文件。")

    def _latest_marker_artifact_path(self, workspace_root: Path, target: Any | None = None) -> Path | None:
        artifacts_dir = workspace_root / "artifacts"
        if not artifacts_dir.exists():
            return None

        candidates = sorted(
            [path for path in artifacts_dir.glob("markers_*.csv") if path.is_file()],
            key=lambda item: item.stat().st_mtime,
            reverse=True,
        )
        if not candidates:
            return None

        if target is not None:
            label_slug = self.slug(target.label)
            for candidate in candidates:
                if label_slug and label_slug in candidate.stem:
                    return candidate
        return candidates[0]

    def _plugin_object_context(self, target: Any | None) -> dict[str, Any] | None:
        if target is None:
            return None
        return {
            "backend_ref": target.backend_ref,
            "label": target.label,
            "kind": target.kind,
            "n_obs": target.n_obs,
            "n_vars": target.n_vars,
            "state": target.state,
            "materialized_path": target.materialized_path,
            "metadata": target.metadata,
        }

    def _execute_plugin_skill(self, ctx: SkillExecutionContext) -> dict[str, Any] | None:
        plugin = self.skill_registry.plugin_skill(self, ctx.skill)
        if plugin is None:
            return None
        return execute_plugin_skill(self, ctx, plugin)

    def execute(self, payload: dict[str, Any]) -> dict[str, Any]:
        target = self.objects.get(payload.get("target_backend_ref", ""))
        ctx = SkillExecutionContext.from_payload(payload, target)

        if not self.skill_enabled(ctx.skill):
            raise RuntimeError(f"技能 `{ctx.skill}` 当前已在 Skill Hub 中停用。")

        builtin_response = self.skill_registry.execute_builtin(self, ctx)
        if builtin_response is not None:
            return builtin_response

        plugin_response = self._execute_plugin_skill(ctx)
        if plugin_response is not None:
            return plugin_response

        raise RuntimeError(f"暂不支持的技能：{ctx.skill}")

    def _generate_methods_section(
        self,
        history: list[dict[str, Any]],
        target: Any | None,
        extra_context: str,
    ) -> str:
        sections: list[str] = ["# Methods", ""]

        if extra_context:
            sections.append(extra_context)
            sections.append("")

        if not history:
            sections.append("No analysis steps were recorded in this session.")
            return "\n".join(sections)

        category_order = [
            "session",
            "quality_control",
            "preprocessing",
            "embedding",
            "clustering",
            "subsetting",
            "differential_expression",
            "visualization",
            "export",
            "custom",
        ]
        skill_category = {
            "inspect_dataset": "session",
            "assess_dataset": "session",
            "summarize_qc": "quality_control",
            "plot_qc_metrics": "quality_control",
            "filter_cells": "quality_control",
            "filter_genes": "quality_control",
            "normalize_total": "preprocessing",
            "log1p_transform": "preprocessing",
            "select_hvg": "preprocessing",
            "scale_matrix": "preprocessing",
            "run_pca": "embedding",
            "compute_neighbors": "embedding",
            "run_umap": "embedding",
            "prepare_umap": "embedding",
            "subset_cells": "subsetting",
            "subcluster_from_global": "clustering",
            "recluster": "clustering",
            "reanalyze_subset": "clustering",
            "subcluster_group": "clustering",
            "rename_clusters": "clustering",
            "score_gene_set": "subsetting",
            "find_markers": "differential_expression",
            "plot_umap": "visualization",
            "plot_gene_umap": "visualization",
            "plot_dotplot": "visualization",
            "plot_violin": "visualization",
            "plot_heatmap": "visualization",
            "plot_celltype_composition": "visualization",
            "run_python_analysis": "custom",
            "export_h5ad": "export",
            "export_markers_csv": "export",
        }
        category_heading = {
            "session": "Data Loading and Inspection",
            "quality_control": "Quality Control",
            "preprocessing": "Preprocessing and Normalization",
            "embedding": "Dimensionality Reduction",
            "clustering": "Clustering",
            "subsetting": "Cell Subsetting and Scoring",
            "differential_expression": "Differential Expression Analysis",
            "visualization": "Visualization",
            "export": "Data Export",
            "custom": "Custom Analysis",
        }

        from collections import OrderedDict

        grouped: OrderedDict[str, list[dict[str, Any]]] = OrderedDict()
        for step in history:
            cat = skill_category.get(step.get("skill", ""), "custom")
            grouped.setdefault(cat, []).append(step)

        sorted_cats = sorted(grouped.keys(), key=lambda c: category_order.index(c) if c in category_order else 999)

        for cat in sorted_cats:
            steps = grouped[cat]
            heading = category_heading.get(cat, cat.replace("_", " ").title())
            sections.append(f"## {heading}")
            sections.append("")
            for step in steps:
                desc = self._describe_step(step)
                if desc:
                    sections.append(desc)
            sections.append("")

        sections.append("## Software")
        sections.append("")
        sections.append(
            "All analyses were performed using scAgent, an interactive single-cell "
            "analysis platform built on Scanpy (Wolf et al., 2018). Visualizations were "
            "generated with Matplotlib. Clustering was performed using the Leiden algorithm "
            "(Traag et al., 2019)."
        )
        sections.append("")

        return "\n".join(sections)

    @staticmethod
    def _describe_step(step: dict[str, Any]) -> str:
        skill_name = step.get("skill", "")
        p = step.get("params") or {}
        summary = step.get("summary", "")

        templates: dict[str, str | None] = {
            "inspect_dataset": "The dataset was inspected to assess its structure and available annotations.",
            "assess_dataset": "The dataset was assessed to determine its processing state.",
        }

        if skill_name == "filter_cells":
            parts = ["Cells were filtered"]
            criteria = []
            if p.get("min_genes"):
                criteria.append(f"minimum {p['min_genes']} detected genes")
            if p.get("max_genes"):
                criteria.append(f"maximum {p['max_genes']} detected genes")
            if p.get("max_mt_pct"):
                criteria.append(f"maximum {p['max_mt_pct']}% mitochondrial content")
            if criteria:
                parts.append("based on " + ", ".join(criteria))
            return " ".join(parts) + "."

        if skill_name == "filter_genes":
            parts = ["Genes were filtered"]
            criteria = []
            if p.get("min_cells"):
                criteria.append(f"expression in at least {p['min_cells']} cells")
            if p.get("min_counts"):
                criteria.append(f"minimum total count of {p['min_counts']}")
            if criteria:
                parts.append("requiring " + ", ".join(criteria))
            return " ".join(parts) + "."

        if skill_name == "summarize_qc":
            prefix = p.get("mt_prefix", "MT-")
            return (
                f"Standard quality control metrics were computed, including total counts, "
                f"number of detected genes, and mitochondrial gene fraction (prefix: {prefix})."
            )

        if skill_name == "normalize_total":
            target_sum = p.get("target_sum", 10000)
            return f"Per-cell counts were normalized to a target library size of {target_sum}."

        if skill_name == "log1p_transform":
            return "The expression matrix was log1p-transformed."

        if skill_name == "select_hvg":
            n = p.get("n_top_genes", 2000)
            flavor = p.get("flavor", "seurat_v3")
            return f"{n} highly variable genes were selected using the {flavor} method."

        if skill_name == "scale_matrix":
            mv = p.get("max_value")
            clip = f", clipping values at {mv}" if mv else ""
            return f"The expression matrix was scaled to unit variance and zero mean{clip}."

        if skill_name == "run_pca":
            n = p.get("n_comps", 50)
            return f"Principal component analysis was performed, retaining {n} components."

        if skill_name == "compute_neighbors":
            nn = p.get("n_neighbors", 15)
            return f"A nearest-neighbor graph was constructed using {nn} neighbors."

        if skill_name == "run_umap":
            md = p.get("min_dist", 0.5)
            return f"UMAP embedding was computed with min_dist={md}."

        if skill_name == "prepare_umap":
            return (
                "An automated preprocessing pipeline was applied to prepare the data for "
                "UMAP visualization, including normalization, log-transformation, HVG selection, "
                "PCA, neighbor graph construction, and UMAP embedding."
            )

        if skill_name == "recluster":
            res = p.get("resolution", 1.0)
            return f"The data were reclustered using the Leiden algorithm at resolution {res}."

        if skill_name == "subcluster_from_global":
            field = p.get("obs_field", "")
            value = p.get("value", "")
            res = p.get("resolution", 1.0)
            return (
                f"A subgroup defined by {field}={value} was isolated from the global object "
                f"and subclustered at resolution {res}."
            )

        if skill_name == "subcluster_group":
            groupby = p.get("groupby", "")
            groups = p.get("groups", [])
            res = p.get("resolution", 1.0)
            group_str = ", ".join(str(g) for g in groups) if isinstance(groups, list) else str(groups)
            return (
                f"Clusters {group_str} (from {groupby}) were isolated and reclustered "
                f"at resolution {res}."
            )

        if skill_name == "reanalyze_subset":
            return "The subset was reanalyzed using a dedicated subclustering workflow."

        if skill_name == "subset_cells":
            field = p.get("obs_field", "")
            op = p.get("op", "eq")
            value = p.get("value", "")
            return f"A cell subset was extracted where {field} {op} {value}."

        if skill_name == "rename_clusters":
            return "Cluster labels were manually renamed for biological annotation."

        if skill_name == "score_gene_set":
            genes = p.get("genes", [])
            name = p.get("score_name", "gene_set_score")
            gene_str = ", ".join(str(g) for g in genes[:5]) if isinstance(genes, list) else str(genes)
            if isinstance(genes, list) and len(genes) > 5:
                gene_str += f" and {len(genes) - 5} others"
            return f"A gene-set score ({name}) was computed using genes: {gene_str}."

        if skill_name == "find_markers":
            groupby = p.get("groupby", "leiden")
            return f"Marker genes were identified for each group defined by {groupby} using the Wilcoxon rank-sum test."

        if skill_name in {"plot_umap", "plot_gene_umap", "plot_dotplot", "plot_violin", "plot_heatmap", "plot_celltype_composition"}:
            return None

        if skill_name == "export_h5ad":
            return None

        if skill_name == "export_markers_csv":
            return None

        if skill_name == "run_python_analysis":
            if summary:
                return f"A custom analysis step was performed: {summary}"
            return "A custom Python analysis step was performed."

        if skill_name in templates:
            return templates[skill_name]

        if summary:
            return summary
        return None

    def _descriptor(self, obj: Any) -> dict[str, Any]:
        return {
            "backend_ref": obj.backend_ref,
            "label": obj.label,
            "kind": obj.kind,
            "n_obs": obj.n_obs,
            "n_vars": obj.n_vars,
            "state": obj.state,
            "in_memory": obj.in_memory,
            "materialized_path": obj.materialized_path,
            "metadata": obj.metadata,
        }
