#!/usr/bin/env python3

from __future__ import annotations

import importlib
import io
import json
import logging
import os
import math
import re
import shutil
import sys
import time
from contextlib import redirect_stderr, redirect_stdout
from dataclasses import dataclass
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from importlib import metadata
from pathlib import Path
from typing import Any

import h5py

MAX_CATEGORY_SCAN = 50000

ENVIRONMENT_PACKAGES: list[tuple[str, str, str]] = [
    ("numpy", "numpy", "numpy"),
    ("scipy", "scipy", "scipy"),
    ("pandas", "pandas", "pandas"),
    ("anndata", "anndata", "anndata"),
    ("scanpy", "scanpy", "scanpy"),
    ("h5py", "h5py", "h5py"),
    ("matplotlib", "matplotlib", "matplotlib"),
    ("scikit-learn", "sklearn", "scikit-learn"),
    ("umap-learn", "umap", "umap-learn"),
    ("python-igraph", "igraph", "igraph"),
    ("leidenalg", "leidenalg", "leidenalg"),
]

logging.basicConfig(
    level=os.environ.get("SCAGENT_LOG_LEVEL", "INFO").upper(),
    format="%(asctime)s %(levelname)s %(message)s",
)
LOGGER = logging.getLogger("scagent.runtime")

os.environ.setdefault("NUMBA_CACHE_DIR", "/tmp/scagent-numba")
os.environ.setdefault("MPLCONFIGDIR", "/tmp/scagent-mpl")
os.environ.setdefault("MPLBACKEND", "Agg")

_ANALYSIS_MODULES: tuple[Any, Any, Any, Any, Any] | None = None
BUILTIN_BUNDLE_ID = "builtin-core"
BUILTIN_EXECUTABLE_SKILLS = [
    "inspect_dataset",
    "assess_dataset",
    "normalize_total",
    "log1p_transform",
    "select_hvg",
    "run_pca",
    "compute_neighbors",
    "run_umap",
    "prepare_umap",
    "subset_cells",
    "subcluster_from_global",
    "recluster",
    "reanalyze_subset",
    "find_markers",
    "plot_umap",
    "plot_gene_umap",
    "run_python_analysis",
    "export_h5ad",
]
SAFE_IMPORT_MODULES = {
    "anndata",
    "json",
    "math",
    "matplotlib",
    "matplotlib.pyplot",
    "numpy",
    "os",
    "pandas",
    "pathlib",
    "re",
    "scanpy",
    "scipy",
    "scipy.sparse",
}
SAFE_EXEC_BUILTINS = {
    "abs": abs,
    "all": all,
    "any": any,
    "bool": bool,
    "dict": dict,
    "enumerate": enumerate,
    "Exception": Exception,
    "float": float,
    "int": int,
    "isinstance": isinstance,
    "len": len,
    "list": list,
    "max": max,
    "min": min,
    "print": print,
    "range": range,
    "round": round,
    "set": set,
    "sorted": sorted,
    "str": str,
    "sum": sum,
    "tuple": tuple,
    "zip": zip,
}


def safe_exec_import(name: str, globals_dict: Any = None, locals_dict: Any = None, fromlist: tuple[str, ...] = (), level: int = 0) -> Any:
    if level != 0:
        raise ImportError("relative imports are not allowed")

    target = str(name or "").strip()
    if target == "":
        raise ImportError("empty import is not allowed")

    allowed = False
    for candidate in SAFE_IMPORT_MODULES:
        if target == candidate or target.startswith(candidate + "."):
            allowed = True
            break
    if not allowed:
        raise ImportError(f"import '{target}' is not allowed")

    return __import__(target, globals_dict, locals_dict, fromlist, level)


SAFE_EXEC_BUILTINS["__import__"] = safe_exec_import


def analysis_modules() -> tuple[Any, Any, Any, Any, Any]:
    global _ANALYSIS_MODULES
    if _ANALYSIS_MODULES is None:
        import anndata as ad
        import matplotlib
        import numpy as np
        import scanpy as sc
        import scipy.sparse as sp

        matplotlib.use("Agg")
        import matplotlib.pyplot as plt

        _ANALYSIS_MODULES = (ad, sc, plt, np, sp)
    return _ANALYSIS_MODULES


def matrix_has_negative_values(matrix: Any) -> bool:
    _, _, _, np, sp = analysis_modules()
    if sp.issparse(matrix):
        minimum = matrix.min()
        return float(minimum) < 0
    return float(np.nanmin(np.asarray(matrix))) < 0


@dataclass
class RuntimeObject:
    backend_ref: str
    session_id: str
    label: str
    kind: str
    n_obs: int
    n_vars: int
    state: str
    in_memory: bool
    materialized_path: str
    metadata: dict[str, Any]


class RuntimeState:
    def __init__(self) -> None:
        self.counter = 0
        self.objects: dict[str, RuntimeObject] = {}
        self.sample_path = Path(os.environ.get("SCAGENT_SAMPLE_H5AD", "data/samples/pbmc3k.h5ad"))
        self.plugin_root = Path(os.environ.get("SCAGENT_PLUGIN_DIR", "data/skill-hub/plugins"))
        self.plugin_state_path = Path(os.environ.get("SCAGENT_PLUGIN_STATE_PATH", str(self.plugin_root.parent / "state.json")))
        self.environment_report = build_environment_report(self.sample_path)

    def load_disabled_bundles(self) -> set[str]:
        if not self.plugin_state_path.exists():
            return set()
        try:
            payload = json.loads(self.plugin_state_path.read_text(encoding="utf-8"))
        except Exception:  # noqa: BLE001
            return set()

        disabled = set()
        for item in payload.get("disabled_bundles", []):
            bundle_id = str(item or "").strip()
            if bundle_id:
                disabled.add(bundle_id)
        return disabled

    def builtin_skills(self) -> list[str]:
        if BUILTIN_BUNDLE_ID in self.load_disabled_bundles():
            return []
        return BUILTIN_EXECUTABLE_SKILLS.copy()

    def next_ref(self, session_id: str) -> str:
        self.counter += 1
        return f"py:{session_id}:adata_{self.counter}"

    def load_plugin_skills(self) -> dict[str, dict[str, Any]]:
        skills: dict[str, dict[str, Any]] = {}
        if not self.plugin_root.exists():
            return skills
        disabled_bundles = self.load_disabled_bundles()

        for manifest_path in sorted(self.plugin_root.rglob("plugin.json")):
            try:
                payload = json.loads(manifest_path.read_text(encoding="utf-8"))
            except Exception:  # noqa: BLE001
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
                skills[skill_name] = {
                    "bundle_id": bundle_id,
                    "manifest_path": manifest_path,
                    "entrypoint": entrypoint_path,
                    "callable": callable_name,
                    "definition": skill_payload,
                }

        return skills

    def skill_enabled(self, skill_name: str) -> bool:
        if skill_name in BUILTIN_EXECUTABLE_SKILLS:
            return BUILTIN_BUNDLE_ID not in self.load_disabled_bundles()
        return skill_name in self.load_plugin_skills()

    def create_session_root(self, session_id: str, label: str, session_root: Path) -> dict[str, Any]:
        objects_dir = session_root / "objects"
        artifacts_dir = session_root / "artifacts"
        objects_dir.mkdir(parents=True, exist_ok=True)
        artifacts_dir.mkdir(parents=True, exist_ok=True)

        backend_ref = self.next_ref(session_id)
        sample_info = self._load_sample(session_id, label, objects_dir)
        materialized_path = sample_info["materialized_path"]

        obj = RuntimeObject(
            backend_ref=backend_ref,
            session_id=session_id,
            label=sample_info["label"],
            kind="raw_dataset",
            n_obs=sample_info["n_obs"],
            n_vars=sample_info["n_vars"],
            state="resident",
            in_memory=True,
            materialized_path=str(materialized_path),
            metadata=sample_info["metadata"],
        )
        self.objects[backend_ref] = obj
        return {
            "object": self._descriptor(obj),
            "summary": sample_info["summary"],
        }

    def load_file(self, session_id: str, file_path: Path, label: str) -> dict[str, Any]:
        if not file_path.exists():
            raise RuntimeError(f"上传文件不存在：{file_path}")

        backend_ref = self.next_ref(session_id)
        n_obs, n_vars = inspect_h5ad_shape(file_path)
        object_label = label or file_path.stem
        obj = RuntimeObject(
            backend_ref=backend_ref,
            session_id=session_id,
            label=object_label,
            kind="raw_dataset",
            n_obs=n_obs,
            n_vars=n_vars,
            state="resident",
            in_memory=True,
            materialized_path=str(file_path),
            metadata=inspect_h5ad_metadata(file_path),
        )
        self.objects[backend_ref] = obj
        annotation_note = describe_annotation_summary(obj.metadata)
        return {
            "object": self._descriptor(obj),
            "summary": f"已上传 {file_path.name}，并注册为 {object_label}（{n_obs} 个细胞，{n_vars} 个基因）。{annotation_note}",
        }

    def _require_target(self, target: RuntimeObject | None, skill: str) -> RuntimeObject:
        if target is None:
            raise RuntimeError(f"{skill} 需要一个目标对象")
        return target

    def _load_adata(self, target: RuntimeObject) -> Any:
        ad, _, _, _, _ = analysis_modules()
        adata = ad.read_h5ad(target.materialized_path)
        adata.var_names_make_unique()
        return adata

    def _load_counts_adata(self, target: RuntimeObject) -> Any:
        adata = self._load_adata(target)
        if matrix_has_negative_values(adata.X):
            if adata.raw is None:
                raise RuntimeError("当前对象缺少可用于预处理的原始 counts，请先提供原始矩阵或带 raw 的 h5ad。")
            adata = adata.raw.to_adata()
            adata.var_names_make_unique()
        return adata

    def _persist_adata_object(
        self,
        session_id: str,
        session_root: Path,
        label: str,
        kind: str,
        adata: Any,
        summary: str,
    ) -> dict[str, Any]:
        backend_ref = self.next_ref(session_id)
        suffix = backend_ref.split(":")[-1]
        materialized_path = session_root / "objects" / f"{slug(label)}_{slug(suffix)}.h5ad"
        materialized_path.parent.mkdir(parents=True, exist_ok=True)
        adata.write_h5ad(materialized_path)

        n_obs, n_vars = inspect_h5ad_shape(materialized_path)
        metadata = inspect_h5ad_metadata(materialized_path)
        obj = RuntimeObject(
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

    def _cluster_field(self, target: RuntimeObject, adata: Any, requested: str | None = None) -> str:
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

    def _default_kind_after_processing(self, target: RuntimeObject) -> str:
        if target.kind == "raw_dataset":
            return "filtered_dataset"
        return target.kind

    def _build_obs_mask(self, adata: Any, obs_field: str, op: str, value: Any) -> Any:
        _, _, _, np, _ = analysis_modules()
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
        _, sc, _, _, _ = analysis_modules()
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

    def _plot_path(self, session_root: Path, skill: str, label: str) -> Path:
        path = session_root / "artifacts" / f"{skill}_{slug(label)}.svg"
        path.parent.mkdir(parents=True, exist_ok=True)
        return path

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

        return dedupe_list([candidate for candidate in candidates if candidate])

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
        _, _, _, np, sp = analysis_modules()
        if sp.issparse(values):
            return np.asarray(values.toarray()).reshape(-1)
        return np.asarray(values).reshape(-1)

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
        _, _, plt, np, _ = analysis_modules()
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
                point_colors = [colors[max(code, 0)] if code >= 0 and code < len(colors) else (0.7, 0.7, 0.7, 0.8) for code in codes]
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
        fig.savefig(path, format="svg")
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
        _, _, plt, np, _ = analysis_modules()
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
        fig.savefig(path, format="svg")
        plt.close(fig)

    def _save_custom_figure(self, figure: Any, session_root: Path, stem: str) -> Path:
        path = session_root / "artifacts" / f"{stem}.svg"
        path.parent.mkdir(parents=True, exist_ok=True)
        figure.savefig(path, format="svg", bbox_inches="tight")
        _, _, plt, _, _ = analysis_modules()
        plt.close(figure)
        return path

    def _save_custom_table(self, table: Any, session_root: Path, stem: str) -> Path:
        path = session_root / "artifacts" / f"{stem}.csv"
        path.parent.mkdir(parents=True, exist_ok=True)
        table.to_csv(path, index=False)
        return path

    def _plugin_object_context(self, target: RuntimeObject | None) -> dict[str, Any] | None:
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

    def _execute_plugin_skill(
        self,
        skill_name: str,
        payload: dict[str, Any],
        target: RuntimeObject | None,
        session_root: Path,
    ) -> dict[str, Any] | None:
        plugin = self.load_plugin_skills().get(skill_name)
        if plugin is None:
            return None

        entrypoint = Path(plugin["entrypoint"])
        if not entrypoint.exists():
            raise RuntimeError(f"插件技能 `{skill_name}` 缺少入口脚本：{entrypoint.name}")

        session_id = str(payload.get("session_id") or "")
        params = payload.get("params") or {}
        adata = self._load_adata(target) if target is not None else None
        counts_adata = self._load_counts_adata(target) if target is not None else None
        _, sc, plt, np, _ = analysis_modules()

        def persist_adata(label: str, output_adata: Any, *, kind: str | None = None) -> dict[str, Any]:
            if target is None:
                raise RuntimeError("当前插件技能没有可持久化的目标对象。")
            persisted = self._persist_adata_object(
                session_id=session_id,
                session_root=session_root,
                label=str(label or f"{skill_name}_{target.label}"),
                kind=kind or self._default_kind_after_processing(target),
                adata=output_adata,
                summary="",
            )
            return persisted["object"]

        def save_figure(figure: Any, stem: str, *, title: str = "", summary: str = "") -> dict[str, Any]:
            figure_path = self._save_custom_figure(figure, session_root, stem or f"{skill_name}_{slug(target.label if target else skill_name)}")
            return {
                "kind": "plot",
                "title": title or f"{skill_name} 输出图",
                "path": str(figure_path),
                "content_type": "image/svg+xml",
                "summary": summary or "由 Skill Hub 插件生成的图。",
            }

        def save_table(table: Any, stem: str, *, title: str = "", summary: str = "") -> dict[str, Any]:
            table_path = self._save_custom_table(table, session_root, stem or f"{skill_name}_{slug(target.label if target else skill_name)}")
            return {
                "kind": "table",
                "title": title or f"{skill_name} 输出表",
                "path": str(table_path),
                "content_type": "text/csv",
                "summary": summary or "由 Skill Hub 插件生成的表。",
            }

        context = {
            "skill_name": skill_name,
            "bundle_id": plugin["bundle_id"],
            "params": params,
            "session_id": session_id,
            "request_id": payload.get("request_id"),
            "target": self._plugin_object_context(target),
            "adata": adata,
            "counts_adata": counts_adata,
            "sc": sc,
            "np": np,
            "plt": plt,
            "json": json,
            "Path": Path,
            "session_root": session_root,
            "artifacts_dir": session_root / "artifacts",
            "plugin_dir": entrypoint.parent,
            "persist_adata": persist_adata,
            "save_figure": save_figure,
            "save_table": save_table,
        }

        exec_env: dict[str, Any] = {
            "__builtins__": SAFE_EXEC_BUILTINS,
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
            handler = exec_env.get(str(plugin["callable"]))
            if not callable(handler):
                raise RuntimeError(f"插件技能 `{skill_name}` 未定义可调用入口 `{plugin['callable']}`。")
            response = handler(context)

        if response is None:
            response = {}
        if not isinstance(response, dict):
            raise RuntimeError(f"插件技能 `{skill_name}` 返回值必须是 dict。")

        metadata = response.get("metadata")
        if not isinstance(metadata, dict):
            metadata = {}
        metadata["plugin_bundle_id"] = plugin["bundle_id"]
        metadata["plugin_skill"] = skill_name
        stdout_text = stdout_buffer.getvalue().strip()
        stderr_text = stderr_buffer.getvalue().strip()
        if stdout_text:
            metadata["stdout"] = stdout_text
        if stderr_text:
            metadata["stderr"] = stderr_text
        response["metadata"] = metadata
        return response

    def execute(self, payload: dict[str, Any]) -> dict[str, Any]:
        skill = payload["skill"]
        session_id = payload["session_id"]
        session_root = Path(payload["session_root"])
        target = self.objects.get(payload.get("target_backend_ref", ""))
        params = payload.get("params", {})

        if not self.skill_enabled(skill):
            raise RuntimeError(f"技能 `{skill}` 当前已在 Skill Hub 中停用。")

        if skill in {"inspect_dataset", "assess_dataset"}:
            target = self._require_target(target, skill)
            metadata = target.metadata or {}
            return {
                "summary": f"{target.label}：{target.n_obs} 个细胞，{target.n_vars} 个基因，状态为 {format_object_state_zh(target.state)}。{describe_annotation_summary(metadata)}",
                "metadata": {
                    "available_obs": metadata.get("obs_fields", []),
                    "available_embeddings": metadata.get("obsm_keys", []),
                    "cell_type_annotation": metadata.get("cell_type_annotation"),
                    "cluster_annotation": metadata.get("cluster_annotation"),
                    "categorical_obs_fields": metadata.get("categorical_obs_fields", []),
                    "assessment": metadata.get("assessment", {}),
                },
            }

        if skill == "normalize_total":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_counts_adata(target)
            target_sum = float(params.get("target_sum") or 1e4)
            sc.pp.normalize_total(adata, target_sum=target_sum)
            return self._persist_adata_object(
                session_id=session_id,
                session_root=session_root,
                label=f"normalized_{target.label}",
                kind=self._default_kind_after_processing(target),
                adata=adata,
                summary=f"已对 {target.label} 完成总表达归一化（target_sum={target_sum:g}）。",
            )

        if skill == "log1p_transform":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_counts_adata(target)
            sc.pp.log1p(adata)
            return self._persist_adata_object(
                session_id=session_id,
                session_root=session_root,
                label=f"log1p_{target.label}",
                kind=self._default_kind_after_processing(target),
                adata=adata,
                summary=f"已对 {target.label} 完成 log1p 变换。",
            )

        if skill == "select_hvg":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_adata(target)
            assessment = (target.metadata or {}).get("assessment") or {}
            needs_recipe = (
                target.kind == "raw_dataset"
                or assessment.get("preprocessing_state") == "raw_like"
                or matrix_has_negative_values(adata.X)
            )
            if needs_recipe:
                adata = self._load_counts_adata(target)
                sc.pp.normalize_total(adata, target_sum=1e4)
                sc.pp.log1p(adata)
            n_top_genes = int(params.get("n_top_genes") or 2000)
            flavor = str(params.get("flavor") or "seurat")
            sc.pp.highly_variable_genes(adata, n_top_genes=n_top_genes, flavor=flavor, subset=False)
            n_hvg = int(adata.var.get("highly_variable", []).sum()) if "highly_variable" in adata.var else 0
            return self._persist_adata_object(
                session_id=session_id,
                session_root=session_root,
                label=f"hvg_{target.label}",
                kind=self._default_kind_after_processing(target),
                adata=adata,
                summary=f"已为 {target.label} 选择高变基因（n_top_genes={n_top_genes}，实际标记 {n_hvg} 个）。",
            )

        if skill == "run_pca":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_adata(target)
            n_comps = int(params.get("n_comps") or 30)
            if "highly_variable" in adata.var and bool(adata.var["highly_variable"].sum()):
                adata = adata[:, adata.var["highly_variable"]].copy()
            max_comps = max(2, min(n_comps, adata.n_obs - 1, adata.n_vars - 1))
            sc.pp.pca(adata, n_comps=max_comps)
            return self._persist_adata_object(
                session_id=session_id,
                session_root=session_root,
                label=f"pca_{target.label}",
                kind=self._default_kind_after_processing(target),
                adata=adata,
                summary=f"已为 {target.label} 计算 PCA（n_comps={max_comps}）。",
            )

        if skill == "compute_neighbors":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_adata(target)
            if "X_pca" not in adata.obsm:
                raise RuntimeError("当前对象缺少 `X_pca`，请先执行 run_pca。")
            n_neighbors = int(params.get("n_neighbors") or 15)
            use_rep = params.get("use_rep")
            if use_rep:
                sc.pp.neighbors(adata, n_neighbors=n_neighbors, use_rep=str(use_rep))
            else:
                sc.pp.neighbors(adata, n_neighbors=n_neighbors, n_pcs=min(30, adata.obsm["X_pca"].shape[1]))
            return self._persist_adata_object(
                session_id=session_id,
                session_root=session_root,
                label=f"neighbors_{target.label}",
                kind=self._default_kind_after_processing(target),
                adata=adata,
                summary=f"已为 {target.label} 计算邻接图（n_neighbors={n_neighbors}）。",
            )

        if skill == "run_umap":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_adata(target)
            if "neighbors" not in adata.uns and "connectivities" not in getattr(adata, "obsp", {}):
                raise RuntimeError("当前对象缺少邻接图，请先执行 compute_neighbors。")
            min_dist = float(params.get("min_dist") or 0.5)
            sc.tl.umap(adata, min_dist=min_dist)
            return self._persist_adata_object(
                session_id=session_id,
                session_root=session_root,
                label=f"umap_{target.label}",
                kind=self._default_kind_after_processing(target),
                adata=adata,
                summary=f"已为 {target.label} 计算 UMAP（min_dist={min_dist:g}）。",
            )

        if skill == "prepare_umap":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_counts_adata(target)
            sc.pp.normalize_total(adata, target_sum=1e4)
            sc.pp.log1p(adata)
            sc.pp.highly_variable_genes(adata, n_top_genes=2000, flavor="seurat", subset=True)
            sc.pp.pca(adata, n_comps=min(30, adata.n_obs - 1, adata.n_vars - 1))
            sc.pp.neighbors(adata, n_neighbors=15, n_pcs=min(30, adata.obsm["X_pca"].shape[1]))
            sc.tl.umap(adata)
            return self._persist_adata_object(
                session_id=session_id,
                session_root=session_root,
                label=f"prepared_{target.label}",
                kind=self._default_kind_after_processing(target),
                adata=adata,
                summary=f"已为 {target.label} 完成常规预处理链，并生成 PCA、邻接图和 UMAP。",
            )

        if skill == "subset_cells":
            target = self._require_target(target, skill)
            adata = self._load_adata(target)
            obs_field = str(params.get("obs_field") or "").strip()
            op = str(params.get("op") or "eq").strip()
            value = params.get("value")
            mask = self._build_obs_mask(adata, obs_field, op, value)

            subset = adata[mask].copy()
            if subset.n_obs == 0:
                raise RuntimeError("筛选结果为空，请检查筛选条件。")
            subset_label = f"subset_{obs_field}_{slug(str(value)) or 'selected'}"
            return self._persist_adata_object(
                session_id=session_id,
                session_root=session_root,
                label=subset_label,
                kind="subset",
                adata=subset,
                summary=f"已从 {target.label} 中筛选出 {subset.n_obs} 个细胞，生成子集 {subset_label}。",
            )

        if skill == "subcluster_from_global":
            target = self._require_target(target, skill)
            adata = self._load_counts_adata(target)
            obs_field = str(params.get("obs_field") or "").strip()
            op = str(params.get("op") or "eq").strip()
            value = params.get("value")
            mask = self._build_obs_mask(adata, obs_field, op, value)
            subset = adata[mask].copy()
            if subset.n_obs == 0:
                raise RuntimeError("亚群分析筛选结果为空，请检查筛选条件。")

            analyzed_subset, workflow = self._run_subcluster_workflow(subset, params)
            subset_label = f"subcluster_{obs_field}_{slug(str(value)) or 'selected'}"
            return self._persist_adata_object(
                session_id=session_id,
                session_root=session_root,
                label=subset_label,
                kind="reclustered_subset",
                adata=analyzed_subset,
                summary=(
                    f"已保持 {target.label} 不变，并仅对 {obs_field}={value} 的 {analyzed_subset.n_obs} 个细胞完成亚群分析。"
                    f"流程包括归一化、log1p、高变基因、PCA、邻接图、UMAP 和 Leiden（resolution={workflow['resolution']}）。"
                ),
            )

        if skill == "recluster":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_adata(target)
            if "X_pca" not in adata.obsm:
                raise RuntimeError("当前对象缺少 `X_pca`，请先执行 run_pca。")
            if "neighbors" not in adata.uns and "connectivities" not in getattr(adata, "obsp", {}):
                sc.pp.neighbors(adata, n_neighbors=15, n_pcs=min(30, adata.obsm["X_pca"].shape[1]))
            resolution = params.get("resolution", 0.6)
            sc.tl.leiden(adata, resolution=float(resolution), key_added="leiden")
            return self._persist_adata_object(
                session_id=session_id,
                session_root=session_root,
                label=f"reclustered_{target.label}",
                kind="reclustered_subset",
                adata=adata,
                summary=f"已对 {target.label} 完成重新聚类，分辨率为 {resolution}。",
            )

        if skill == "reanalyze_subset":
            target = self._require_target(target, skill)
            adata = self._load_counts_adata(target)
            analyzed_subset, workflow = self._run_subcluster_workflow(adata, params)
            return self._persist_adata_object(
                session_id=session_id,
                session_root=session_root,
                label=f"reanalyzed_{target.label}",
                kind="reclustered_subset",
                adata=analyzed_subset,
                summary=(
                    f"已对提取亚群 {target.label} 重新执行低计数友好的亚群分析。"
                    f"流程包括归一化、log1p、高变基因、PCA、邻接图、UMAP 和 Leiden（resolution={workflow['resolution']}）。"
                ),
            )

        if skill == "find_markers":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_adata(target)
            groupby = self._cluster_field(target, adata, str(params.get("groupby") or ""))
            adata.obs[groupby] = adata.obs[groupby].astype("category")
            path = session_root / "artifacts" / f"markers_{slug(target.label)}.csv"
            sc.tl.rank_genes_groups(adata, groupby=groupby, method="wilcoxon", use_raw=adata.raw is not None)
            markers = sc.get.rank_genes_groups_df(adata, group=None)
            markers.to_csv(path, index=False)
            return {
                "summary": f"已为 {target.label} 生成 marker 表（groupby={groupby}）。",
                "artifacts": [
                    {
                        "kind": "table",
                        "title": f"{target.label} 的 marker 表",
                        "path": str(path),
                        "content_type": "text/csv",
                        "summary": f"按 {groupby} 汇总的 marker 基因结果。",
                    }
                ],
                "metadata": {"groupby": groupby},
            }

        if skill == "plot_umap":
            target = self._require_target(target, skill)
            adata = self._load_adata(target)
            color_by = str(params.get("color_by") or "").strip()
            legend_loc = self._normalize_legend_loc(params.get("legend_loc"))
            palette = str(params.get("palette") or "").strip() or None
            title = str(params.get("title") or "").strip() or None
            point_size = self._coerce_positive_float(params.get("point_size"), 8.0)
            figure_width = self._coerce_positive_float(params.get("figure_width"), 6.2)
            figure_height = self._coerce_positive_float(params.get("figure_height"), 4.8)
            if not color_by:
                try:
                    color_by = self._cluster_field(target, adata, None)
                except RuntimeError:
                    color_by = ""
            if color_by and color_by not in adata.obs.columns:
                raise RuntimeError(f"`{color_by}` 不是 obs 字段；如果要按基因表达着色，请使用 plot_gene_umap。")
            path = self._plot_path(session_root, skill, target.label)
            self._save_umap_plot(
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
            summary_bits = [f"已为 {target.label} 生成真实 UMAP 图。"]
            if color_by:
                summary_bits.append(f"着色字段：{color_by}。")
            if legend_loc != "best":
                summary_bits.append(f"图例位置：{legend_loc}。")
            if title:
                summary_bits.append(f"标题：{title}。")
            return {
                "summary": "".join(summary_bits),
                "artifacts": [
                    {
                        "kind": "plot",
                        "title": f"{target.label} 的 UMAP 图",
                        "path": str(path),
                        "content_type": "image/svg+xml",
                        "summary": f"{target.label} 的真实 UMAP 散点图。",
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

        if skill == "plot_gene_umap":
            target = self._require_target(target, skill)
            adata = self._load_adata(target)
            requested_genes = self._normalize_gene_list(params.get("genes"))
            if not requested_genes:
                raise RuntimeError("plot_gene_umap 需要至少一个基因。")

            layer_name = str(params.get("layer") or "").strip() or None
            artifacts: list[dict[str, Any]] = []
            resolved_genes: list[dict[str, str]] = []
            for requested_gene in requested_genes:
                display_gene, gene_key, expression, source = self._resolve_gene_expression(adata, requested_gene, layer_name)
                path = self._plot_path(session_root, skill, f"{target.label}_{display_gene}")
                self._save_gene_umap_plot(adata, path, display_gene, expression)
                artifacts.append(
                    {
                        "kind": "plot",
                        "title": f"{target.label} 的 {display_gene} 基因 UMAP",
                        "path": str(path),
                        "content_type": "image/svg+xml",
                        "summary": f"{target.label} 中 {display_gene} 的真实基因表达 UMAP 图。",
                    }
                )
                resolved_genes.append(
                    {
                        "requested": display_gene,
                        "feature_key": gene_key,
                        "source": source,
                    }
                )

            summary_bits = [f"已为 {target.label} 生成 {len(artifacts)} 个基因 UMAP 图：{format_list_zh(requested_genes)}。"]
            if layer_name:
                summary_bits.append(f"使用 layer：{layer_name}。")
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

        if skill == "run_python_analysis":
            target = self._require_target(target, skill)
            _, sc, plt, np, _ = analysis_modules()
            import pandas as pd

            adata = self._load_adata(target)
            counts_adata = self._load_counts_adata(target)
            code = str(params.get("code") or "").strip()
            if code == "":
                raise RuntimeError("自定义分析缺少 code。")

            output_label = str(params.get("output_label") or f"custom_{target.label}").strip() or f"custom_{target.label}"
            persist_output = bool(params.get("persist_output"))
            stdout_buffer = io.StringIO()
            stderr_buffer = io.StringIO()
            exec_env: dict[str, Any] = {
                "__builtins__": SAFE_EXEC_BUILTINS,
                "adata": adata,
                "counts_adata": counts_adata,
                "sc": sc,
                "np": np,
                "pd": pd,
                "plt": plt,
                "Path": Path,
                "json": json,
                "session_root": session_root,
                "artifacts_dir": session_root / "artifacts",
                "result_summary": "",
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
            output_adata = exec_env.get("output_adata")
            if output_adata is None and bool(exec_env.get("persist_output")):
                output_adata = exec_env.get("adata")

            artifacts: list[dict[str, Any]] = []
            figure = exec_env.get("figure")
            if figure is None and plt.get_fignums():
                figure = plt.gcf()
            if figure is not None and hasattr(figure, "savefig"):
                figure_path = self._save_custom_figure(figure, session_root, f"custom_plot_{slug(output_label)}")
                artifacts.append(
                    {
                        "kind": "plot",
                        "title": f"{output_label} 的自定义图",
                        "path": str(figure_path),
                        "content_type": "image/svg+xml",
                        "summary": "由自定义 Python 分析生成的图。",
                    }
                )

            result_table = exec_env.get("result_table")
            if result_table is not None and hasattr(result_table, "to_csv"):
                table_path = self._save_custom_table(result_table, session_root, f"custom_table_{slug(output_label)}")
                artifacts.append(
                    {
                        "kind": "table",
                        "title": f"{output_label} 的自定义表",
                        "path": str(table_path),
                        "content_type": "text/csv",
                        "summary": "由自定义 Python 分析生成的表。",
                    }
                )

            response: dict[str, Any] = {
                "summary": result_summary or f"已完成针对 {target.label} 的自定义 Python 分析。",
                "metadata": {
                    "code_executed": True,
                    "stdout": stdout_text or None,
                    "stderr": stderr_text or None,
                },
            }

            if output_adata is not None:
                persisted = self._persist_adata_object(
                    session_id=session_id,
                    session_root=session_root,
                    label=output_label,
                    kind=self._default_kind_after_processing(target),
                    adata=output_adata,
                    summary="",
                )
                response["object"] = persisted["object"]
                if not result_summary:
                    response["summary"] = f"已完成针对 {target.label} 的自定义 Python 分析，并生成对象 {output_label}。"

            if artifacts:
                response["artifacts"] = artifacts

            return response

        plugin_response = self._execute_plugin_skill(skill, payload, target, session_root)
        if plugin_response is not None:
            return plugin_response

        if skill == "export_h5ad":
            target = self._require_target(target, skill)
            export_path = session_root / "objects" / f"{slug(target.label)}.h5ad"
            shutil.copy2(target.materialized_path, export_path)
            target.materialized_path = str(export_path)
            target.state = "materialized"
            return {
                "summary": f"已将 {target.label} 导出到磁盘。",
                "artifacts": [
                    {
                        "kind": "file",
                        "title": f"{target.label}.h5ad",
                        "path": str(export_path),
                        "content_type": "application/octet-stream",
                        "summary": "对象落盘快照文件。",
                    }
                ],
            }

        raise RuntimeError(f"暂不支持的技能：{skill}")

    def _descriptor(self, obj: RuntimeObject) -> dict[str, Any]:
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

    def _load_sample(self, session_id: str, label: str, objects_dir: Path) -> dict[str, Any]:
        if self.sample_path.exists():
            sample_name = slug(self.sample_path.stem) or "sample"
            materialized_path = objects_dir / f"{sample_name}.h5ad"
            shutil.copy2(self.sample_path, materialized_path)
            n_obs, n_vars = inspect_h5ad_shape(materialized_path)
            metadata = inspect_h5ad_metadata(materialized_path)
            return {
                "label": self.sample_path.stem,
                "n_obs": n_obs,
                "n_vars": n_vars,
                "materialized_path": materialized_path,
                "metadata": metadata,
                "summary": f"会话已从样例文件 {self.sample_path.name} 初始化。{describe_annotation_summary(metadata)}",
            }

        materialized_path = objects_dir / "raw_demo.h5ad"
        materialized_path.write_text(
            json.dumps(
                {
                    "session_id": session_id,
                    "label": label,
                    "kind": "raw_dataset",
                    "note": "由 MVP runtime 生成的占位 h5ad",
                },
                indent=2,
            ),
            encoding="utf-8",
        )
        metadata = {
            "top_level_keys": ["X", "obs", "var", "obsm", "uns"],
            "obs_fields": ["cell_type", "sample", "leiden"],
            "obsm_keys": ["X_umap"],
            "uns_keys": ["neighbors", "pca"],
            "layer_keys": [],
            "var_fields": ["gene_symbol"],
            "varm_keys": [],
            "raw_present": True,
            "categorical_obs_fields": [
                {
                    "field": "cell_type",
                    "n_categories": 6,
                    "sample_values": ["cortex", "endodermis", "epidermis", "phloem", "xylem"],
                    "role": "cell_type",
                },
                {
                    "field": "sample",
                    "n_categories": 2,
                    "sample_values": ["rep1", "rep2"],
                    "role": "covariate",
                },
            ],
            "cluster_annotation": {
                "field": "leiden",
                "n_categories": 8,
                "sample_values": ["0", "1", "2", "3"],
                "role": "cluster",
                "confidence": "high",
            },
            "cell_type_annotation": {
                "field": "cell_type",
                "n_categories": 6,
                "sample_values": ["cortex", "endodermis", "epidermis", "phloem", "xylem"],
                "role": "cell_type",
                "confidence": "high",
            },
        }
        metadata["assessment"] = build_dataset_assessment(metadata)
        return {
            "label": "root_atlas_demo",
            "n_obs": 4821,
            "n_vars": 28671,
            "materialized_path": materialized_path,
            "metadata": metadata,
            "summary": "未在磁盘上找到样例 .h5ad，已使用回退演示数据初始化会话。",
        }

def log_runtime_event(event: str, **fields: Any) -> None:
    payload = {"event": event}
    for key, value in fields.items():
        if value in (None, "", [], {}):
            continue
        payload[key] = value
    LOGGER.info(json.dumps(payload, ensure_ascii=False, default=str))


class RequestHandler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        if self.path == "/healthz":
            plugin_skills = STATE.load_plugin_skills()
            executable_skills = dedupe_list(STATE.builtin_skills() + sorted(plugin_skills.keys()))
            notes = [
                "运行时会读取真实的 h5ad 结构和注释信息。",
                "常规预处理链、subset、recluster、marker 和 UMAP 已切到真实 AnnData/Scanpy 执行。",
                "当现成 tool 不够时，可通过 run_python_analysis 在内存中的 AnnData 上执行短代码。",
                "dotplot 和 violin 仍未开放给 planner，等真实实现完成后再升为 wired。",
            ]
            disabled_bundles = STATE.load_disabled_bundles()
            if disabled_bundles:
                notes.append(f"Skill Hub 当前停用了 {len(disabled_bundles)} 个技能包，规划器与运行时都会跳过这些技能。")
            if plugin_skills:
                notes.append(f"Skill Hub 已加载 {len(plugin_skills)} 个插件技能，可在当前会话中直接调用。")
            payload = {
                "status": "ok",
                "runtime_mode": "live",
                "real_h5ad_inspection": True,
                "real_analysis_execution": True,
                "executable_skills": executable_skills,
                "notes": notes,
            }
            payload.update(STATE.environment_report)
            self._write_json(HTTPStatus.OK, payload)
            return
        self._write_json(HTTPStatus.NOT_FOUND, {"error": "未找到接口"})

    def do_POST(self) -> None:
        payload: dict[str, Any] = {}
        started_at = time.perf_counter()
        try:
            payload = self._read_json()
            session_id = payload.get("session_id")
            request_id = payload.get("request_id")
            if self.path == "/sessions/init":
                log_runtime_event(
                    "session_init_started",
                    session_id=session_id,
                    label=payload.get("label"),
                    session_root=payload.get("session_root"),
                )
                session_root = Path(payload["session_root"])
                response = STATE.create_session_root(
                    session_id=payload["session_id"],
                    label=payload.get("label", "session"),
                    session_root=session_root,
                )
                log_runtime_event(
                    "session_init_finished",
                    session_id=session_id,
                    duration_ms=round((time.perf_counter() - started_at) * 1000, 2),
                    object_label=response.get("object", {}).get("label"),
                    n_obs=response.get("object", {}).get("n_obs"),
                    n_vars=response.get("object", {}).get("n_vars"),
                )
                self._write_json(HTTPStatus.OK, response)
                return
            if self.path == "/sessions/load_file":
                log_runtime_event(
                    "load_file_started",
                    session_id=session_id,
                    label=payload.get("label"),
                    file_path=payload.get("file_path"),
                )
                response = STATE.load_file(
                    session_id=payload["session_id"],
                    file_path=Path(payload["file_path"]),
                    label=payload.get("label", ""),
                )
                log_runtime_event(
                    "load_file_finished",
                    session_id=session_id,
                    duration_ms=round((time.perf_counter() - started_at) * 1000, 2),
                    object_label=response.get("object", {}).get("label"),
                    n_obs=response.get("object", {}).get("n_obs"),
                    n_vars=response.get("object", {}).get("n_vars"),
                )
                self._write_json(HTTPStatus.OK, response)
                return
            if self.path == "/execute":
                log_runtime_event(
                    "job_started",
                    session_id=session_id,
                    request_id=request_id,
                    skill=payload.get("skill"),
                    target_backend_ref=payload.get("target_backend_ref"),
                    params=payload.get("params"),
                )
                response = STATE.execute(payload)
                log_runtime_event(
                    "job_finished",
                    session_id=session_id,
                    request_id=request_id,
                    skill=payload.get("skill"),
                    target_backend_ref=payload.get("target_backend_ref"),
                    duration_ms=round((time.perf_counter() - started_at) * 1000, 2),
                    artifact_count=len(response.get("artifacts", [])),
                    output_label=response.get("object", {}).get("label"),
                    summary=response.get("summary"),
                )
                self._write_json(HTTPStatus.OK, response)
                return
            self._write_json(HTTPStatus.NOT_FOUND, {"error": "未找到接口"})
        except Exception as exc:  # noqa: BLE001
            log_runtime_event(
                "request_failed",
                path=self.path,
                session_id=payload.get("session_id"),
                request_id=payload.get("request_id"),
                skill=payload.get("skill"),
                duration_ms=round((time.perf_counter() - started_at) * 1000, 2),
                error=str(exc),
            )
            self._write_json(HTTPStatus.BAD_REQUEST, {"error": str(exc)})

    def log_message(self, format: str, *args: Any) -> None:
        return

    def _read_json(self) -> dict[str, Any]:
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length) if length else b"{}"
        return json.loads(raw.decode("utf-8"))

    def _write_json(self, status: HTTPStatus, payload: dict[str, Any]) -> None:
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def slug(value: str) -> str:
    return "".join(ch.lower() if ch.isalnum() else "_" for ch in value).strip("_")


def inspect_h5ad_shape(path: Path) -> tuple[int, int]:
    with h5py.File(path, "r") as handle:
        matrix = handle.get("X")
        if matrix is not None and getattr(matrix, "shape", None) and len(matrix.shape) >= 2:
            return int(matrix.shape[0]), int(matrix.shape[1])

        obs = handle.get("obs")
        var = handle.get("var")
        n_obs = infer_axis_length(obs)
        n_vars = infer_axis_length(var)
    return n_obs, n_vars


def build_environment_report(sample_path: Path) -> dict[str, Any]:
    checks: list[dict[str, Any]] = []

    for label, module_name, dist_name in ENVIRONMENT_PACKAGES:
        try:
            with io.StringIO() as sink, redirect_stdout(sink), redirect_stderr(sink):
                importlib.import_module(module_name)
            checks.append(
                {
                    "name": label,
                    "ok": True,
                    "detail": metadata.version(dist_name),
                }
            )
        except Exception as exc:  # pragma: no cover - diagnostic path
            checks.append(
                {
                    "name": label,
                    "ok": False,
                    "detail": str(exc),
                }
            )

    sample_summary: dict[str, Any] | None = None
    if sample_path.exists():
        try:
            sample_summary = inspect_sample_h5ad(sample_path)
            checks.append(
                {
                    "name": "sample_h5ad",
                    "ok": True,
                    "detail": f"{sample_path}（{sample_summary['n_obs']} 个细胞，{sample_summary['n_vars']} 个基因）",
                }
            )
        except Exception as exc:  # pragma: no cover - diagnostic path
            checks.append(
                {
                    "name": "sample_h5ad",
                    "ok": False,
                    "detail": str(exc),
                }
            )
    else:
        checks.append(
            {
                "name": "sample_h5ad",
                "ok": False,
                "detail": f"缺少样例文件：{sample_path}",
            }
        )

    return {
        "python_version": sys.version.split()[0],
        "environment_checks": checks,
        "sample_h5ad": sample_summary,
    }


def inspect_sample_h5ad(path: Path) -> dict[str, Any]:
    metadata = inspect_h5ad_metadata(path)
    n_obs, n_vars = inspect_h5ad_shape(path)
    return {
        "path": str(path),
        "n_obs": n_obs,
        "n_vars": n_vars,
        "obs_fields": metadata.get("obs_fields", [])[:12],
        "obsm_keys": metadata.get("obsm_keys", [])[:12],
    }


def infer_axis_length(node: Any) -> int:
    if node is None:
        return 0
    shape = getattr(node, "shape", None)
    if shape:
        return int(shape[0])
    if hasattr(node, "keys") and "_index" in node:
        return len(node["_index"])
    return 0


def inspect_h5ad_metadata(path: Path) -> dict[str, Any]:
    with h5py.File(path, "r") as handle:
        top_level_keys = sorted(list(handle.keys()))[:20]
        obs = handle.get("obs")
        var = handle.get("var")
        obsm = handle.get("obsm")
        uns = handle.get("uns")
        layers = handle.get("layers")
        varm = handle.get("varm")
        metadata = {
            "top_level_keys": top_level_keys,
            "obs_fields": structured_fields(obs),
            "var_fields": structured_fields(var),
            "obsm_keys": structured_fields(obsm),
            "uns_keys": structured_fields(uns),
            "layer_keys": structured_fields(layers),
            "varm_keys": structured_fields(varm),
            "raw_present": "raw.X" in top_level_keys or "raw" in top_level_keys,
        }
        metadata.update(inspect_obs_annotations(obs))
        metadata["assessment"] = build_dataset_assessment(metadata)
    return metadata


def structured_fields(node: Any) -> list[str]:
    if node is None:
        return []
    dtype = getattr(node, "dtype", None)
    if dtype is not None and getattr(dtype, "names", None):
        return list(dtype.names)[:30]
    if hasattr(node, "keys"):
        fields = [key for key in node.keys() if key != "_index"]
        return sorted(fields)[:30]
    return []


def inspect_obs_annotations(node: Any) -> dict[str, Any]:
    categorical_fields: list[dict[str, Any]] = []
    for field in structured_fields(node):
        if field == "index":
            continue
        summary = summarize_obs_field(node, field)
        if summary is None or summary.get("kind") != "categorical":
            continue
        summary["role"] = infer_obs_role(field)
        categorical_fields.append(summary)

    cell_type_annotation = pick_best_annotation(categorical_fields, "cell_type")
    cluster_annotation = pick_best_annotation(categorical_fields, "cluster")
    return {
        "categorical_obs_fields": categorical_fields[:12],
        "cell_type_annotation": cell_type_annotation,
        "cluster_annotation": cluster_annotation,
    }


def summarize_obs_field(node: Any, field: str) -> dict[str, Any] | None:
    if node is None:
        return None

    dtype = getattr(node, "dtype", None)
    if dtype is not None and getattr(dtype, "names", None):
        return summarize_values(field, node[field][:], dtype[field].kind)

    if hasattr(node, "keys"):
        child = node.get(field)
        if child is None:
            return None
        encoding_type = child.attrs.get("encoding-type") if hasattr(child, "attrs") else None
        if encoding_type == "categorical" and hasattr(child, "keys") and "categories" in child:
            categories = decode_values(child["categories"][:])
            return {
                "field": field,
                "kind": "categorical",
                "n_categories": len(categories),
                "sample_values": categories[:10],
            }

        child_dtype = getattr(child, "dtype", None)
        if child_dtype is not None and getattr(child_dtype, "names", None):
            return None
        if child_dtype is not None:
            return summarize_values(field, child[:], child_dtype.kind)
    return None


def summarize_values(field: str, values: Any, dtype_kind: str | None) -> dict[str, Any] | None:
    size = len(values)
    if size == 0:
        return None

    limited = values[: min(size, MAX_CATEGORY_SCAN)]
    decoded = decode_values(limited)
    unique_values = sorted({value for value in decoded if value != ""})
    if not looks_categorical(unique_values, len(decoded), dtype_kind):
        return None

    return {
        "field": field,
        "kind": "categorical",
        "n_categories": len(unique_values),
        "sample_values": unique_values[:10],
    }


def decode_values(values: Any) -> list[str]:
    out: list[str] = []
    for value in values:
        if isinstance(value, bytes):
            out.append(value.decode("utf-8", "ignore"))
        else:
            out.append(str(value))
    return out


def looks_categorical(unique_values: list[str], sample_size: int, dtype_kind: str | None) -> bool:
    unique_count = len(unique_values)
    if unique_count == 0:
        return False
    if dtype_kind in {"S", "U", "O", "b"}:
        return unique_count <= 200
    if dtype_kind in {"i", "u"}:
        return unique_count <= 50 and unique_count / max(sample_size, 1) <= 0.25
    return False


def infer_obs_role(field: str) -> str:
    lower = field.lower()
    if any(token in lower for token in ["cell_type", "celltype", "annotation", "cell_label", "cell_ontology", "subtype", "broad_type", "fine_type"]):
        return "cell_type"
    if any(token in lower for token in ["cluster", "clusters", "leiden", "louvain", "seurat"]):
        return "cluster"
    if any(token in lower for token in ["sample", "batch", "condition", "donor", "replicate", "time", "group"]):
        return "covariate"
    return "annotation"


def pick_best_annotation(categorical_fields: list[dict[str, Any]], role: str) -> dict[str, Any] | None:
    exact = [item for item in categorical_fields if item.get("role") == role]
    if not exact:
        return None
    picked = min(exact, key=lambda item: item.get("n_categories", 999999))
    confidence = "high"
    if role == "cell_type" and picked.get("n_categories", 0) > 100:
        confidence = "medium"
    enriched = dict(picked)
    enriched["confidence"] = confidence
    return enriched


def build_dataset_assessment(metadata: dict[str, Any]) -> dict[str, Any]:
    obsm_keys = set(metadata.get("obsm_keys", []))
    uns_keys = set(metadata.get("uns_keys", []))
    has_umap = "X_umap" in obsm_keys
    has_pca = "X_pca" in obsm_keys or "pca" in uns_keys
    has_neighbors = "neighbors" in uns_keys
    has_clusters = metadata.get("cluster_annotation") is not None

    if has_umap and has_pca and has_neighbors:
        preprocessing_state = "analysis_ready"
    elif has_pca or has_neighbors or has_clusters or metadata.get("layer_keys"):
        preprocessing_state = "partially_processed"
    else:
        preprocessing_state = "raw_like"

    available_analyses = ["inspect_dataset", "subset_cells", "run_python_analysis", "export_h5ad"]
    if has_pca or has_neighbors:
        available_analyses.append("recluster")
    if has_umap:
        available_analyses.extend(["plot_umap", "plot_gene_umap"])
    if has_clusters:
        available_analyses.append("find_markers")

    missing_requirements = []
    if not has_pca:
        missing_requirements.append("未发现 PCA 嵌入（`obsm['X_pca']`）。")
    if not has_neighbors:
        missing_requirements.append("未发现邻接图（`uns['neighbors']`）。")
    if not has_umap:
        missing_requirements.append("未发现 UMAP 嵌入（`obsm['X_umap']`）。")

    suggested_next_steps = []
    if not has_pca:
        suggested_next_steps.extend(["过滤低质量细胞", "总表达归一化", "log1p 转换", "选择高变基因", "运行 PCA"])
    if has_pca and not has_neighbors:
        suggested_next_steps.append("计算邻接图")
    if (has_pca or has_neighbors) and not has_umap:
        suggested_next_steps.append("运行 UMAP")
    if has_umap:
        suggested_next_steps.append("绘制基因 UMAP")

    return {
        "preprocessing_state": preprocessing_state,
        "has_umap": has_umap,
        "has_pca": has_pca,
        "has_neighbors": has_neighbors,
        "has_clusters": has_clusters,
        "available_analyses": dedupe_list(available_analyses),
        "missing_requirements": missing_requirements,
        "suggested_next_steps": dedupe_list(suggested_next_steps),
    }


def dedupe_list(values: list[str]) -> list[str]:
    seen: set[str] = set()
    out: list[str] = []
    for value in values:
        if value in seen:
            continue
        seen.add(value)
        out.append(value)
    return out


def format_list_zh(values: list[str]) -> str:
    if not values:
        return "无"
    return "、".join(values)


def format_object_state_zh(state: str) -> str:
    return {
        "resident": "常驻",
        "materialized": "已落盘",
    }.get(state, state)


def describe_annotation_summary(metadata: dict[str, Any]) -> str:
    assessment = metadata.get("assessment", {})
    parts: list[str] = []
    cell_type = metadata.get("cell_type_annotation")
    cluster = metadata.get("cluster_annotation")

    if cell_type:
        parts.append(
            f"检测到疑似细胞类型字段 `{cell_type['field']}`，共 {cell_type['n_categories']} 个类别。"
        )
    elif cluster:
        parts.append(
            f"未检测到高置信度细胞类型字段；发现聚类字段 `{cluster['field']}`，共 {cluster['n_categories']} 组。"
        )
    else:
        parts.append("未检测到高置信度的细胞类型或聚类注释。")

    if assessment.get("preprocessing_state"):
        parts.append(f"数据集状态为 `{assessment['preprocessing_state']}`。")

    missing = assessment.get("missing_requirements", [])
    if missing:
        parts.append(f"缺失条件：{missing[0]}")

    return " ".join(parts)


STATE = RuntimeState()


def main() -> None:
    host = os.environ.get("SCAGENT_RUNTIME_HOST", "127.0.0.1")
    port = int(os.environ.get("SCAGENT_RUNTIME_PORT", "8081"))
    server = ThreadingHTTPServer((host, port), RequestHandler)
    print(f"runtime listening on http://{host}:{port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
