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
    "summarize_qc",
    "plot_qc_metrics",
    "filter_cells",
    "filter_genes",
    "normalize_total",
    "log1p_transform",
    "select_hvg",
    "scale_matrix",
    "run_pca",
    "compute_neighbors",
    "run_umap",
    "prepare_umap",
    "subset_cells",
    "subcluster_from_global",
    "score_gene_set",
    "recluster",
    "reanalyze_subset",
    "subcluster_group",
    "rename_clusters",
    "find_markers",
    "plot_umap",
    "plot_gene_umap",
    "plot_dotplot",
    "plot_violin",
    "plot_heatmap",
    "plot_celltype_composition",
    "run_python_analysis",
    "export_h5ad",
    "export_markers_csv",
    "write_method",
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
    "getattr": getattr,
    "hasattr": hasattr,
    "int": int,
    "isinstance": isinstance,
    "len": len,
    "list": list,
    "max": max,
    "min": min,
    "next": next,
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
BACKEND_REF_RE = re.compile(r"^py:[^:]+:adata_(\d+)$")


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

    def _sync_counter_with_backend_ref(self, backend_ref: str) -> None:
        match = BACKEND_REF_RE.match(str(backend_ref or "").strip())
        if match is None:
            return
        self.counter = max(self.counter, int(match.group(1)))

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

    def create_workspace_root(self, session_id: str, label: str, workspace_root: Path) -> dict[str, Any]:
        objects_dir = workspace_root / "objects"
        artifacts_dir = workspace_root / "artifacts"
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

    def ensure_object(self, session_id: str, descriptor: dict[str, Any]) -> dict[str, Any]:
        backend_ref = str(descriptor.get("backend_ref") or "").strip()
        if backend_ref and backend_ref in self.objects:
            obj = self.objects[backend_ref]
            return {
                "object": self._descriptor(obj),
                "summary": f"目标对象 {obj.label} 已存在于运行时。",
            }

        materialized_path_raw = str(descriptor.get("materialized_path") or "").strip()
        if materialized_path_raw == "":
            raise RuntimeError("无法恢复目标对象：缺少 materialized_path。")

        materialized_path = Path(materialized_path_raw)
        if not materialized_path.exists():
            raise RuntimeError(f"无法恢复目标对象，文件不存在：{materialized_path}")

        label = str(descriptor.get("label") or materialized_path.stem).strip() or materialized_path.stem
        kind = str(descriptor.get("kind") or "unknown").strip() or "unknown"
        state = str(descriptor.get("state") or "resident").strip() or "resident"
        metadata = descriptor.get("metadata")
        if not isinstance(metadata, dict):
            metadata = {}

        try:
            n_obs = int(descriptor.get("n_obs") or 0)
        except (TypeError, ValueError):
            n_obs = 0
        try:
            n_vars = int(descriptor.get("n_vars") or 0)
        except (TypeError, ValueError):
            n_vars = 0
        h5ad_backed_kinds = {"raw_dataset", "filtered_dataset", "subset", "reclustered_subset", "unknown"}
        if kind in h5ad_backed_kinds:
            if not metadata:
                metadata = inspect_h5ad_metadata(materialized_path)
            if n_obs <= 0 or n_vars <= 0:
                n_obs, n_vars = inspect_h5ad_shape(materialized_path)
        else:
            metadata = metadata or {}

        if backend_ref == "":
            backend_ref = self.next_ref(session_id)
        else:
            self._sync_counter_with_backend_ref(backend_ref)

        obj = RuntimeObject(
            backend_ref=backend_ref,
            session_id=session_id,
            label=label,
            kind=kind,
            n_obs=n_obs,
            n_vars=n_vars,
            state=state,
            in_memory=bool(descriptor.get("in_memory", True)),
            materialized_path=str(materialized_path),
            metadata=metadata,
        )
        self.objects[backend_ref] = obj
        return {
            "object": self._descriptor(obj),
            "summary": f"已恢复目标对象 {label} 到运行时。",
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

    def _load_qc_adata(self, target: RuntimeObject) -> Any:
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
        materialized_path = workspace_root / "objects" / f"{slug(label)}_{slug(suffix)}.h5ad"
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

    def _categorical_field(self, target: RuntimeObject, adata: Any, requested: str | None = None) -> str:
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

    def _artifact_path(self, workspace_root: Path, stem: str, extension: str, request_id: str | None = None) -> Path:
        artifacts_dir = workspace_root / "artifacts"
        artifacts_dir.mkdir(parents=True, exist_ok=True)

        normalized_extension = extension.lstrip(".").lower() or "bin"
        base_stem = slug(stem) or "artifact"
        request_suffix = slug(str(request_id or "").strip())
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
        _, sc, _, _, _ = analysis_modules()
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

        return dedupe_list([item for item in normalized if not available or item in available])

    def _metric_stats(self, adata: Any, metric: str) -> dict[str, float] | None:
        _, _, _, np, _ = analysis_modules()
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

        return dedupe_list([candidate for candidate in candidates if candidate])

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
                raise RuntimeError(f"当前对象中未找到请求基因：{format_list_zh(missing)}。")
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
        _, _, _, np, sp = analysis_modules()
        if sp.issparse(values):
            return np.asarray(values.toarray()).reshape(-1)
        return np.asarray(values).reshape(-1)

    def _dense_matrix(self, values: Any) -> Any:
        _, _, _, np, sp = analysis_modules()
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
        fig.savefig(path, format="png", dpi=180, bbox_inches="tight", facecolor="white")
        plt.close(fig)

    def _save_qc_metrics_plot(self, adata: Any, path: Path, metrics: list[str], *, title: str | None = None) -> None:
        _, _, plt, np, _ = analysis_modules()
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
        _, _, plt, np, _ = analysis_modules()
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
        _, _, plt, np, _ = analysis_modules()
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
        _, _, plt, np, _ = analysis_modules()
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
            axis.set_xticks(positions, group_labels if index == len(gene_labels) - 1 else [""] * len(group_labels), rotation=45, ha="right")
            axis.spines["top"].set_visible(False)
            axis.spines["right"].set_visible(False)

        fig.suptitle(figure_title)
        fig.tight_layout()
        fig.savefig(path, format="png", dpi=180, bbox_inches="tight", facecolor="white")
        plt.close(fig)

    def _save_stacked_bar_plot(self, path: Path, composition: Any, *, title: str | None = None) -> None:
        _, _, plt, np, _ = analysis_modules()
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
        _, _, plt, _, _ = analysis_modules()
        plt.close(figure)
        return path

    def _save_custom_table(self, table: Any, workspace_root: Path, stem: str, request_id: str | None = None) -> Path:
        path = self._artifact_path(workspace_root, stem, "csv", request_id)
        table.to_csv(path, index=False)
        return path

    def _table_source_path(self, target: RuntimeObject) -> Path:
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

    def _latest_marker_artifact_path(self, workspace_root: Path, target: RuntimeObject | None = None) -> Path | None:
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
            label_slug = slug(target.label)
            for candidate in candidates:
                if label_slug and label_slug in candidate.stem:
                    return candidate
        return candidates[0]

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
        workspace_root: Path,
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
                workspace_root=workspace_root,
                label=str(label or f"{skill_name}_{target.label}"),
                kind=kind or self._default_kind_after_processing(target),
                adata=output_adata,
                summary="",
            )
            return persisted["object"]

        def save_figure(figure: Any, stem: str, *, title: str = "", summary: str = "") -> dict[str, Any]:
            figure_path = self._save_custom_figure(
                figure,
                workspace_root,
                stem or f"{skill_name}_{slug(target.label if target else skill_name)}",
                request_id=str(payload.get("request_id") or "").strip() or None,
            )
            return {
                "kind": "plot",
                "title": title or f"{skill_name} 输出图",
                "path": str(figure_path),
                "content_type": "image/png",
                "summary": summary or "由 Skill Hub 插件生成的图。",
            }

        def save_table(table: Any, stem: str, *, title: str = "", summary: str = "") -> dict[str, Any]:
            table_path = self._save_custom_table(
                table,
                workspace_root,
                stem or f"{skill_name}_{slug(target.label if target else skill_name)}",
                request_id=str(payload.get("request_id") or "").strip() or None,
            )
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
            "workspace_root": workspace_root,
            "artifacts_dir": workspace_root / "artifacts",
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
        workspace_root = Path(payload["workspace_root"])
        request_id = str(payload.get("request_id") or "").strip() or None
        target = self.objects.get(payload.get("target_backend_ref", ""))
        params = payload.get("params", {})

        if not self.skill_enabled(skill):
            raise RuntimeError(f"技能 `{skill}` 当前已在 Skill Hub 中停用。")

        if skill in {"inspect_dataset", "assess_dataset"}:
            target = self._require_target(target, skill)
            metadata = target.metadata or {}
            return {
                "summary": f"{target.label}：{target.n_obs} 个细胞，{target.n_vars} 个基因，状态为 {format_object_state_zh(target.state)}。{describe_annotation_summary(metadata)}",
                "facts": build_inspect_dataset_facts(target),
                "metadata": {
                    "available_obs": metadata.get("obs_fields", []),
                    "available_embeddings": metadata.get("obsm_keys", []),
                    "cell_type_annotation": metadata.get("cell_type_annotation"),
                    "cluster_annotation": metadata.get("cluster_annotation"),
                    "categorical_obs_fields": metadata.get("categorical_obs_fields", []),
                    "assessment": metadata.get("assessment", {}),
                },
            }

        if skill == "summarize_qc":
            target = self._require_target(target, skill)
            adata = self._load_qc_adata(target)
            qc_info = self._ensure_qc_metrics(adata, params.get("mt_prefix"))
            stats = {metric: self._metric_stats(adata, metric) for metric in qc_info["metrics"]}
            facts = {
                "cell_count": int(adata.n_obs),
                "gene_count": int(adata.n_vars),
                "mt_prefix": qc_info["mt_prefix"],
                "qc_metrics": stats,
            }

            summary_bits = [f"已为 {target.label} 计算 QC 指标。"]
            total_counts_stats = stats.get("total_counts") or {}
            genes_stats = stats.get("n_genes_by_counts") or {}
            mt_stats = stats.get("pct_counts_mt") or {}
            if total_counts_stats:
                summary_bits.append(f"每细胞 total_counts 中位数为 {total_counts_stats['median']:g}。")
            if genes_stats:
                summary_bits.append(f"每细胞检测基因数中位数为 {genes_stats['median']:g}。")
            if mt_stats:
                summary_bits.append(f"线粒体占比中位数为 {mt_stats['median']:g}%。")
            if qc_info["mt_prefix"]:
                summary_bits.append(f"线粒体基因前缀识别为 `{qc_info['mt_prefix']}`。")
            else:
                summary_bits.append("未识别线粒体基因前缀，pct_counts_mt 按 0 处理。")

            return {
                "summary": "".join(summary_bits),
                "facts": facts,
                "metadata": {
                    "qc_metrics": stats,
                    "mt_prefix": qc_info["mt_prefix"],
                },
            }

        if skill == "plot_qc_metrics":
            target = self._require_target(target, skill)
            adata = self._load_qc_adata(target)
            qc_info = self._ensure_qc_metrics(adata, params.get("mt_prefix"))
            metrics = self._normalize_qc_metric_names(params.get("metrics"), qc_info["metrics"])
            if not metrics:
                raise RuntimeError("plot_qc_metrics 没有可绘制的 QC 指标。")

            title = str(params.get("title") or "").strip() or None
            path = self._plot_path(workspace_root, skill, target.label, request_id)
            self._save_qc_metrics_plot(adata, path, metrics, title=title)
            return {
                "summary": f"已为 {target.label} 生成 QC 分布图（指标：{format_list_zh(metrics)}）。",
                "artifacts": [
                    {
                        "kind": "plot",
                        "title": f"{target.label} 的 QC 指标图",
                        "path": str(path),
                        "content_type": "image/png",
                        "summary": f"{target.label} 的 QC 指标分布图。",
                    }
                ],
                "metadata": {
                    "metrics": metrics,
                    "mt_prefix": qc_info["mt_prefix"],
                },
            }

        if skill == "filter_cells":
            target = self._require_target(target, skill)
            _, _, _, np, _ = analysis_modules()
            adata = self._load_qc_adata(target)
            qc_info = self._ensure_qc_metrics(adata, params.get("mt_prefix"))

            thresholds: dict[str, float] = {}
            if params.get("min_genes") is not None:
                thresholds["min_genes"] = float(params["min_genes"])
            if params.get("max_genes") is not None:
                thresholds["max_genes"] = float(params["max_genes"])
            if params.get("max_mt_pct") is not None:
                thresholds["max_mt_pct"] = float(params["max_mt_pct"])
            if not thresholds:
                raise RuntimeError("filter_cells 至少需要一个阈值：min_genes、max_genes 或 max_mt_pct。")

            mask = np.ones(adata.n_obs, dtype=bool)
            if "min_genes" in thresholds:
                mask &= np.asarray(adata.obs["n_genes_by_counts"], dtype=float) >= thresholds["min_genes"]
            if "max_genes" in thresholds:
                mask &= np.asarray(adata.obs["n_genes_by_counts"], dtype=float) <= thresholds["max_genes"]
            if "max_mt_pct" in thresholds:
                mask &= np.asarray(adata.obs["pct_counts_mt"], dtype=float) <= thresholds["max_mt_pct"]

            filtered = adata[mask].copy()
            if filtered.n_obs == 0:
                raise RuntimeError("filter_cells 的筛选结果为空，请检查阈值。")

            removed = adata.n_obs - filtered.n_obs
            threshold_bits = [f"{name}={value:g}" for name, value in thresholds.items()]
            return self._persist_adata_object(
                session_id=session_id,
                workspace_root=workspace_root,
                label=f"filtered_cells_{target.label}",
                kind="filtered_dataset",
                adata=filtered,
                summary=(
                    f"已对 {target.label} 应用细胞过滤（{', '.join(threshold_bits)}），"
                    f"保留 {filtered.n_obs} 个细胞，移除 {removed} 个细胞。"
                ),
            )

        if skill == "filter_genes":
            target = self._require_target(target, skill)
            _, _, _, np, sp = analysis_modules()
            adata = self._load_qc_adata(target)

            thresholds: dict[str, float] = {}
            if params.get("min_cells") is not None:
                thresholds["min_cells"] = float(params["min_cells"])
            if params.get("min_counts") is not None:
                thresholds["min_counts"] = float(params["min_counts"])
            if not thresholds:
                raise RuntimeError("filter_genes 至少需要一个阈值：min_cells 或 min_counts。")

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
                raise RuntimeError("filter_genes 的筛选结果为空，请检查阈值。")

            removed = adata.n_vars - filtered.n_vars
            threshold_bits = [f"{name}={value:g}" for name, value in thresholds.items()]
            return self._persist_adata_object(
                session_id=session_id,
                workspace_root=workspace_root,
                label=f"filtered_genes_{target.label}",
                kind="filtered_dataset",
                adata=filtered,
                summary=(
                    f"已对 {target.label} 应用基因过滤（{', '.join(threshold_bits)}），"
                    f"保留 {filtered.n_vars} 个基因，移除 {removed} 个基因。"
                ),
            )

        if skill == "normalize_total":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_counts_adata(target)
            target_sum = float(params.get("target_sum") or 1e4)
            sc.pp.normalize_total(adata, target_sum=target_sum)
            return self._persist_adata_object(
                session_id=session_id,
                workspace_root=workspace_root,
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
                workspace_root=workspace_root,
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
                workspace_root=workspace_root,
                label=f"hvg_{target.label}",
                kind=self._default_kind_after_processing(target),
                adata=adata,
                summary=f"已为 {target.label} 选择高变基因（n_top_genes={n_top_genes}，实际标记 {n_hvg} 个）。",
            )

        if skill == "scale_matrix":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_adata(target)
            max_value = params.get("max_value")
            if max_value is not None:
                sc.pp.scale(adata, max_value=float(max_value))
            else:
                sc.pp.scale(adata)
            summary = f"已对 {target.label} 完成表达矩阵缩放。"
            if max_value is not None:
                summary = f"已对 {target.label} 完成表达矩阵缩放（max_value={float(max_value):g}）。"
            return self._persist_adata_object(
                session_id=session_id,
                workspace_root=workspace_root,
                label=f"scaled_{target.label}",
                kind=self._default_kind_after_processing(target),
                adata=adata,
                summary=summary,
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
                workspace_root=workspace_root,
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
                workspace_root=workspace_root,
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
                workspace_root=workspace_root,
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
                workspace_root=workspace_root,
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
                workspace_root=workspace_root,
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
                workspace_root=workspace_root,
                label=subset_label,
                kind="reclustered_subset",
                adata=analyzed_subset,
                summary=(
                    f"已保持 {target.label} 不变，并仅对 {obs_field}={value} 的 {analyzed_subset.n_obs} 个细胞完成亚群分析。"
                    f"流程包括归一化、log1p、高变基因、PCA、邻接图、UMAP 和 Leiden（resolution={workflow['resolution']}）。"
                ),
            )

        if skill == "score_gene_set":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_adata(target)
            requested_genes, resolved_genes, missing_genes = self._resolve_gene_keys(adata, params.get("genes"), require_at_least_one=True)
            score_name = str(params.get("score_name") or "").strip() or f"score_{slug('_'.join(item['requested'] for item in resolved_genes[:4])) or 'gene_set'}"
            sc.tl.score_genes(adata, gene_list=[item["feature_key"] for item in resolved_genes], score_name=score_name, use_raw=adata.raw is not None)
            summary_bits = [f"已为 {target.label} 计算基因集得分，并写入 obs 字段 `{score_name}`。"]
            if missing_genes:
                summary_bits.append(f"未命中的基因：{format_list_zh(missing_genes)}。")
            persisted = self._persist_adata_object(
                session_id=session_id,
                workspace_root=workspace_root,
                label=f"scored_{target.label}",
                kind=self._default_kind_after_processing(target),
                adata=adata,
                summary="".join(summary_bits),
            )
            persisted["metadata"] = {
                "score_name": score_name,
                "genes": requested_genes,
                "resolved_genes": resolved_genes,
                "missing_genes": missing_genes,
            }
            return persisted

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
                workspace_root=workspace_root,
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
                workspace_root=workspace_root,
                label=f"reanalyzed_{target.label}",
                kind="reclustered_subset",
                adata=analyzed_subset,
                summary=(
                    f"已对提取亚群 {target.label} 重新执行低计数友好的亚群分析。"
                    f"流程包括归一化、log1p、高变基因、PCA、邻接图、UMAP 和 Leiden（resolution={workflow['resolution']}）。"
                ),
            )

        if skill == "subcluster_group":
            target = self._require_target(target, skill)
            adata = self._load_counts_adata(target)
            groupby = str(params.get("groupby") or "").strip()
            groups = params.get("groups")
            if groupby == "":
                raise RuntimeError("subcluster_group 需要 groupby。")
            if not isinstance(groups, list) or not groups:
                raise RuntimeError("subcluster_group 需要非空的 groups。")

            mask = self._build_obs_mask(adata, groupby, "in", groups)
            subset = adata[mask].copy()
            if subset.n_obs == 0:
                raise RuntimeError("subcluster_group 的筛选结果为空，请检查 groups。")

            analyzed_subset, workflow = self._run_subcluster_workflow(subset, params)
            group_label = slug("_".join(str(item) for item in groups)) or "selected"
            return self._persist_adata_object(
                session_id=session_id,
                workspace_root=workspace_root,
                label=f"subcluster_{groupby}_{group_label}",
                kind="reclustered_subset",
                adata=analyzed_subset,
                summary=(
                    f"已从 {target.label} 中提取 {groupby}={format_list_zh([str(item) for item in groups])} 的 {analyzed_subset.n_obs} 个细胞，"
                    f"并完成亚群重分析（resolution={workflow['resolution']}）。"
                ),
            )

        if skill == "rename_clusters":
            target = self._require_target(target, skill)
            adata = self._load_adata(target)
            groupby = str(params.get("groupby") or "").strip()
            mapping = params.get("mapping")
            if groupby == "":
                raise RuntimeError("rename_clusters 需要 groupby。")
            if groupby not in adata.obs.columns:
                raise RuntimeError(f"当前对象缺少 obs 字段 `{groupby}`。")
            if not isinstance(mapping, dict) or not mapping:
                raise RuntimeError("rename_clusters 需要非空的 mapping。")

            renamed = adata.obs[groupby].astype(str).replace({str(key): str(value) for key, value in mapping.items()})
            adata.obs[groupby] = renamed.astype("category")
            return self._persist_adata_object(
                session_id=session_id,
                workspace_root=workspace_root,
                label=f"renamed_{target.label}",
                kind=target.kind,
                adata=adata,
                summary=f"已在 {target.label} 中重命名 `{groupby}` 的类别标签，共应用 {len(mapping)} 条映射。",
            )

        if skill == "find_markers":
            target = self._require_target(target, skill)
            _, sc, _, _, _ = analysis_modules()
            adata = self._load_adata(target)
            groupby = self._cluster_field(target, adata, str(params.get("groupby") or ""))
            adata.obs[groupby] = adata.obs[groupby].astype("category")
            path = self._artifact_path(workspace_root, f"markers_{target.label}", "csv", request_id)
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
            legend_loc_param = str(params.get("legend_loc") or "").strip()
            legend_loc = self._normalize_legend_loc(legend_loc_param)
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
            if color_by and legend_loc_param == "":
                series = adata.obs[color_by]
                if getattr(series.dtype, "kind", "") not in {"i", "u", "f"}:
                    categories = series.astype("category")
                    if len(categories.cat.categories) > 4:
                        legend_loc = "on data"
            path = self._plot_path(workspace_root, skill, target.label, request_id)
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
                        "content_type": "image/png",
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
                path = self._plot_path(workspace_root, skill, f"{target.label}_{display_gene}", request_id)
                self._save_gene_umap_plot(adata, path, display_gene, expression)
                artifacts.append(
                    {
                        "kind": "plot",
                        "title": f"{target.label} 的 {display_gene} 基因 UMAP",
                        "path": str(path),
                        "content_type": "image/png",
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

        if skill == "plot_dotplot":
            target = self._require_target(target, skill)
            _, _, _, np, _ = analysis_modules()
            adata = self._load_adata(target)
            requested_genes, resolved_genes, missing_genes = self._resolve_gene_keys(adata, params.get("genes"), require_at_least_one=True)
            requested_groupby = str(params.get("groupby") or "").strip()
            groupby = self._categorical_field(target, adata, requested_groupby if requested_groupby else None)
            categories = adata.obs[groupby].astype("category")
            codes = categories.cat.codes.to_numpy()
            group_labels = [str(item) for item in categories.cat.categories]
            gene_keys = [item["feature_key"] for item in resolved_genes]
            gene_labels = [item["requested"] for item in resolved_genes]
            expression = self._dense_matrix(adata[:, gene_keys].X)

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

            title = str(params.get("title") or "").strip() or None
            palette = str(params.get("palette") or "").strip() or None
            path = self._plot_path(workspace_root, skill, target.label, request_id)
            self._save_dotplot(
                path,
                group_labels,
                gene_labels,
                np.vstack(mean_values),
                np.vstack(pct_values),
                title=title,
                palette=palette,
            )
            summary_bits = [f"已为 {target.label} 生成 dotplot（groupby={groupby}，基因：{format_list_zh(gene_labels)}）。"]
            if missing_genes:
                summary_bits.append(f"未命中的基因：{format_list_zh(missing_genes)}。")
            return {
                "summary": "".join(summary_bits),
                "artifacts": [
                    {
                        "kind": "plot",
                        "title": f"{target.label} 的 dotplot",
                        "path": str(path),
                        "content_type": "image/png",
                        "summary": f"{target.label} 按 {groupby} 汇总的 dotplot。",
                    }
                ],
                "metadata": {
                    "groupby": groupby,
                    "genes": requested_genes,
                    "resolved_genes": resolved_genes,
                    "missing_genes": missing_genes,
                },
            }

        if skill == "plot_violin":
            target = self._require_target(target, skill)
            _, _, _, np, _ = analysis_modules()
            adata = self._load_adata(target)
            requested_genes, resolved_genes, missing_genes = self._resolve_gene_keys(adata, params.get("genes"), require_at_least_one=True)
            requested_groupby = str(params.get("groupby") or "").strip()
            if requested_groupby:
                if requested_groupby not in adata.obs.columns:
                    raise RuntimeError(f"当前对象缺少 obs 字段 `{requested_groupby}`。")
                groupby = requested_groupby
            else:
                try:
                    groupby = self._categorical_field(target, adata, None)
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
                expression = self._dense_vector(adata[:, [item["feature_key"]]].X)
                gene_groups = []
                for index in range(len(group_labels)):
                    gene_groups.append(expression[codes == index])
                grouped_values.append(gene_groups)

            title = str(params.get("title") or "").strip() or None
            path = self._plot_path(workspace_root, skill, target.label, request_id)
            self._save_violin_plot(path, group_labels, [item["requested"] for item in resolved_genes], grouped_values, title=title)
            summary_bits = [f"已为 {target.label} 生成小提琴图（基因：{format_list_zh([item['requested'] for item in resolved_genes])}）。"]
            if groupby:
                summary_bits.append(f"分组字段：{groupby}。")
            if missing_genes:
                summary_bits.append(f"未命中的基因：{format_list_zh(missing_genes)}。")
            return {
                "summary": "".join(summary_bits),
                "artifacts": [
                    {
                        "kind": "plot",
                        "title": f"{target.label} 的小提琴图",
                        "path": str(path),
                        "content_type": "image/png",
                        "summary": f"{target.label} 的基因表达小提琴图。",
                    }
                ],
                "metadata": {
                    "groupby": groupby or None,
                    "genes": requested_genes,
                    "resolved_genes": resolved_genes,
                    "missing_genes": missing_genes,
                },
            }

        if skill == "plot_heatmap":
            target = self._require_target(target, skill)
            _, _, _, np, _ = analysis_modules()
            adata = self._load_adata(target)
            requested_genes, resolved_genes, missing_genes = self._resolve_gene_keys(adata, params.get("genes"), require_at_least_one=True)
            requested_groupby = str(params.get("groupby") or "").strip()
            if requested_groupby:
                if requested_groupby not in adata.obs.columns:
                    raise RuntimeError(f"当前对象缺少 obs 字段 `{requested_groupby}`。")
                groupby = requested_groupby
            else:
                try:
                    groupby = self._categorical_field(target, adata, None)
                except RuntimeError:
                    groupby = ""

            gene_keys = [item["feature_key"] for item in resolved_genes]
            expression = self._dense_matrix(adata[:, gene_keys].X)
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

            title = str(params.get("title") or "").strip() or None
            palette = str(params.get("palette") or "").strip() or None
            path = self._plot_path(workspace_root, skill, target.label, request_id)
            self._save_group_heatmap(
                path,
                group_labels,
                [item["requested"] for item in resolved_genes],
                heatmap_values,
                title=title,
                palette=palette,
            )
            summary_bits = [f"已为 {target.label} 生成热图（基因：{format_list_zh([item['requested'] for item in resolved_genes])}）。"]
            if groupby:
                summary_bits.append(f"分组字段：{groupby}。")
            if missing_genes:
                summary_bits.append(f"未命中的基因：{format_list_zh(missing_genes)}。")
            return {
                "summary": "".join(summary_bits),
                "artifacts": [
                    {
                        "kind": "plot",
                        "title": f"{target.label} 的热图",
                        "path": str(path),
                        "content_type": "image/png",
                        "summary": f"{target.label} 的基因表达热图。",
                    }
                ],
                "metadata": {
                    "groupby": groupby or None,
                    "genes": requested_genes,
                    "resolved_genes": resolved_genes,
                    "missing_genes": missing_genes,
                },
            }

        if skill == "plot_celltype_composition":
            target = self._require_target(target, skill)
            import pandas as pd

            adata = self._load_adata(target)
            groupby = str(params.get("groupby") or "").strip()
            split_by = str(params.get("split_by") or "").strip()
            if groupby == "" or split_by == "":
                raise RuntimeError("plot_celltype_composition 需要 groupby 和 split_by。")
            if groupby not in adata.obs.columns:
                raise RuntimeError(f"当前对象缺少 obs 字段 `{groupby}`。")
            if split_by not in adata.obs.columns:
                raise RuntimeError(f"当前对象缺少 obs 字段 `{split_by}`。")

            composition = pd.crosstab(
                adata.obs[split_by].astype(str),
                adata.obs[groupby].astype(str),
                normalize="index",
            ) * 100.0
            if composition.empty:
                raise RuntimeError("plot_celltype_composition 没有可绘制的数据。")

            title = str(params.get("title") or "").strip() or None
            path = self._plot_path(workspace_root, skill, target.label, request_id)
            self._save_stacked_bar_plot(path, composition, title=title)
            return {
                "summary": f"已为 {target.label} 生成组成图（groupby={groupby}，split_by={split_by}）。",
                "artifacts": [
                    {
                        "kind": "plot",
                        "title": f"{target.label} 的组成图",
                        "path": str(path),
                        "content_type": "image/png",
                        "summary": f"{target.label} 按 {split_by} 分层的 {groupby} 组成图。",
                    }
                ],
                "metadata": {
                    "groupby": groupby,
                    "split_by": split_by,
                    "n_groups": int(len(composition.columns)),
                    "n_splits": int(len(composition.index)),
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
                "workspace_root": workspace_root,
                "artifacts_dir": workspace_root / "artifacts",
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
                figure_path = self._save_custom_figure(
                    figure,
                    workspace_root,
                    f"custom_plot_{slug(output_label)}",
                    request_id,
                )
                artifacts.append(
                    {
                        "kind": "plot",
                        "title": f"{output_label} 的自定义图",
                        "path": str(figure_path),
                        "content_type": "image/png",
                        "summary": "由自定义 Python 分析生成的图。",
                    }
                )

            result_table = exec_env.get("result_table")
            if result_table is not None and hasattr(result_table, "to_csv"):
                table_path = self._save_custom_table(
                    result_table,
                    workspace_root,
                    f"custom_table_{slug(output_label)}",
                    request_id,
                )
                artifacts.append(
                    {
                        "kind": "table",
                        "title": f"{output_label} 的自定义表",
                        "path": str(table_path),
                        "content_type": "text/csv",
                        "summary": "由自定义 Python 分析生成的表。",
                    }
                )

            facts = build_custom_analysis_facts(
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
                "summary": default_custom_analysis_summary(
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
                persisted = self._persist_adata_object(
                    session_id=session_id,
                    workspace_root=workspace_root,
                    label=output_label,
                    kind=self._default_kind_after_processing(target),
                    adata=output_adata,
                    summary="",
                )
                response["object"] = persisted["object"]
                if not result_summary and not result_text and facts.get("result_value") is None:
                    response["summary"] = f"已完成针对 {target.label} 的自定义 Python 分析，并生成对象 {output_label}。"

            if artifacts:
                response["artifacts"] = artifacts

            return response

        plugin_response = self._execute_plugin_skill(skill, payload, target, workspace_root)
        if plugin_response is not None:
            return plugin_response

        if skill == "export_h5ad":
            target = self._require_target(target, skill)
            export_path = workspace_root / "objects" / f"{slug(target.label)}.h5ad"
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

        if skill == "export_markers_csv":
            target = self._require_target(target, skill)
            import pandas as pd

            source_path = self._table_source_path(target)
            export_path = self._artifact_path(workspace_root, f"{target.label}_markers", "csv", request_id)
            suffix = source_path.suffix.lower()
            if suffix == ".csv":
                shutil.copy2(source_path, export_path)
            elif suffix in {".tsv", ".txt"}:
                table = pd.read_csv(source_path, sep="\t")
                table.to_csv(export_path, index=False)
            else:
                try:
                    table = pd.read_csv(source_path)
                except Exception:
                    table = pd.read_csv(source_path, sep="\t")
                table.to_csv(export_path, index=False)

            return {
                "summary": f"已将 {target.label} 导出为 CSV 文件。",
                "artifacts": [
                    {
                        "kind": "file",
                        "title": f"{target.label}.csv",
                        "path": str(export_path),
                        "content_type": "text/csv",
                        "summary": "marker / differential expression 结果导出文件。",
                    }
                ],
                "metadata": {
                    "source_path": str(source_path),
                },
            }

        if skill == "write_method":
            filename = str(params.get("filename") or "Methods.md").strip() or "Methods.md"
            extra_context = str(params.get("extra_context") or "").strip()
            history = params.get("_analysis_history") or []

            content = self._generate_methods_section(history, target, extra_context)

            stem = filename.rsplit(".", 1)[0] if "." in filename else filename
            ext = filename.rsplit(".", 1)[1] if "." in filename else "md"
            method_path = self._artifact_path(workspace_root, slug(stem) or "methods", ext, request_id)
            method_path.write_text(content, encoding="utf-8")

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

        raise RuntimeError(f"暂不支持的技能：{skill}")

    def _generate_methods_section(
        self,
        history: list[dict[str, Any]],
        target: "RuntimeObject | None",
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

        # Group steps by category, preserving first-occurrence order.
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

        # Software note
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
            return None  # Skip visualization steps — not typically included in Methods.

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

        materialized_path = objects_dir / "pbmc3k_demo.h5ad"
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
            "obs_fields": ["cell_type", "louvain"],
            "obsm_keys": ["X_pca", "X_umap"],
            "uns_keys": ["neighbors", "pca"],
            "layer_keys": [],
            "var_fields": ["gene_symbol"],
            "varm_keys": [],
            "raw_present": True,
            "categorical_obs_fields": [
                {
                    "field": "cell_type",
                    "n_categories": 7,
                    "sample_values": ["B cells", "CD4 T cells", "NK cells", "CD14+ Monocytes", "FCGR3A+ Monocytes"],
                    "role": "cell_type",
                },
                {
                    "field": "louvain",
                    "n_categories": 8,
                    "sample_values": ["0", "1", "2", "3"],
                    "role": "cluster",
                },
            ],
            "cluster_annotation": {
                "field": "louvain",
                "n_categories": 8,
                "sample_values": ["0", "1", "2", "3"],
                "role": "cluster",
                "confidence": "high",
            },
            "cell_type_annotation": {
                "field": "cell_type",
                "n_categories": 7,
                "sample_values": ["B cells", "CD4 T cells", "NK cells", "CD14+ Monocytes", "FCGR3A+ Monocytes"],
                "role": "cell_type",
                "confidence": "high",
            },
        }
        metadata["assessment"] = build_dataset_assessment(metadata)
        return {
            "label": "pbmc3k_demo",
            "n_obs": 2700,
            "n_vars": 32738,
            "materialized_path": materialized_path,
            "metadata": metadata,
            "summary": "未在磁盘上找到样例 .h5ad，已使用 PBMC3K 回退演示数据初始化会话。",
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
                "常规预处理链、QC、subset、recluster、marker 和主要图形技能已切到真实 AnnData/Scanpy 执行。",
                "当现成 tool 不够时，可通过 run_python_analysis 在内存中的 AnnData 上执行短代码。",
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
                    workspace_root=payload.get("workspace_root"),
                )
                workspace_root = Path(payload["workspace_root"])
                response = STATE.create_workspace_root(
                    session_id=payload["session_id"],
                    label=payload.get("label", "session"),
                    workspace_root=workspace_root,
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
            if self.path == "/objects/ensure":
                object_payload = payload.get("object") or {}
                log_runtime_event(
                    "ensure_object_started",
                    session_id=session_id,
                    backend_ref=object_payload.get("backend_ref"),
                    label=object_payload.get("label"),
                    materialized_path=object_payload.get("materialized_path"),
                )
                response = STATE.ensure_object(
                    session_id=payload["session_id"],
                    descriptor=object_payload,
                )
                log_runtime_event(
                    "ensure_object_finished",
                    session_id=session_id,
                    backend_ref=response.get("object", {}).get("backend_ref"),
                    duration_ms=round((time.perf_counter() - started_at) * 1000, 2),
                    object_label=response.get("object", {}).get("label"),
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
        matrix_shape = infer_h5ad_matrix_shape(matrix)
        if matrix_shape is not None:
            return matrix_shape

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
    if hasattr(node, "attrs"):
        index_name = node.attrs.get("_index")
        if isinstance(index_name, bytes):
            index_name = index_name.decode("utf-8", "ignore")
        if isinstance(index_name, str) and hasattr(node, "keys") and index_name in node:
            return len(node[index_name])
    if hasattr(node, "keys") and "_index" in node:
        return len(node["_index"])
    if hasattr(node, "keys"):
        keys = list(node.keys())
        if keys:
            first_key = keys[0]
            try:
                return len(node[first_key])
            except Exception:
                return 0
    return 0


def infer_h5ad_matrix_shape(node: Any) -> tuple[int, int] | None:
    if node is None:
        return None

    shape = getattr(node, "shape", None)
    if shape and len(shape) >= 2:
        return int(shape[0]), int(shape[1])

    if hasattr(node, "attrs"):
        attr_shape = node.attrs.get("shape")
        if attr_shape is not None:
            try:
                values = list(attr_shape)
                if len(values) >= 2:
                    return int(values[0]), int(values[1])
            except Exception:
                pass

    if hasattr(node, "keys") and "indptr" in node and "indices" in node:
        try:
            n_obs = len(node["indptr"]) - 1
            attr_shape = node.attrs.get("shape")
            if attr_shape is not None:
                values = list(attr_shape)
                if len(values) >= 2:
                    return int(values[0]), int(values[1])
            if len(node["indices"]) > 0:
                return int(n_obs), int(max(node["indices"]) + 1)
        except Exception:
            return None

    return None


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

    available_analyses = [
        "inspect_dataset",
        "summarize_qc",
        "plot_qc_metrics",
        "filter_cells",
        "filter_genes",
        "subset_cells",
        "run_python_analysis",
        "export_h5ad",
    ]
    if has_pca or has_neighbors:
        available_analyses.append("recluster")
    if has_pca:
        available_analyses.append("score_gene_set")
    if has_umap:
        available_analyses.extend(["plot_umap", "plot_gene_umap"])
    if has_clusters:
        available_analyses.extend(["find_markers", "subcluster_group", "rename_clusters", "plot_dotplot", "plot_violin", "plot_heatmap"])
    if metadata.get("categorical_obs_fields"):
        available_analyses.append("plot_celltype_composition")

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


def build_inspect_dataset_facts(target: RuntimeObject) -> dict[str, Any]:
    metadata = target.metadata or {}
    assessment = metadata.get("assessment") or {}
    cluster = metadata.get("cluster_annotation") or {}
    cell_type = metadata.get("cell_type_annotation") or {}
    return {
        "object_label": target.label,
        "cell_count": target.n_obs,
        "gene_count": target.n_vars,
        "object_state": target.state,
        "has_umap": bool(assessment.get("has_umap")),
        "has_pca": bool(assessment.get("has_pca")),
        "has_neighbors": bool(assessment.get("has_neighbors")),
        "preprocessing_state": assessment.get("preprocessing_state"),
        "cluster_field": cluster.get("field"),
        "cluster_count": cluster.get("n_categories"),
        "cell_type_field": cell_type.get("field"),
        "cell_type_count": cell_type.get("n_categories"),
    }


def build_custom_analysis_facts(
    *,
    output_label: str,
    result_value: Any,
    result_text: str,
    result_summary: str,
    stdout_text: str,
    result_table: Any,
    output_adata: Any,
    artifacts: list[dict[str, Any]],
) -> dict[str, Any]:
    scalar_value = normalize_custom_scalar(result_value)
    if scalar_value is None:
        scalar_value = infer_scalar_from_stdout(stdout_text)

    facts: dict[str, Any] = {
        "analysis_kind": "custom_python",
        "output_label": output_label,
        "generated_object": output_adata is not None,
        "artifact_count": len(artifacts),
        "table_generated": result_table is not None,
        "stdout_text": stdout_text or None,
    }
    if scalar_value is not None:
        facts["result_value"] = scalar_value
    if result_text:
        facts["result_text"] = result_text
    if result_summary:
        facts["result_summary"] = result_summary
    if result_table is not None:
        facts["table_rows"] = safe_table_length(result_table)
        facts["table_columns"] = safe_table_columns(result_table)
    if output_adata is not None:
        facts["generated_object_label"] = output_label
    return facts


def default_custom_analysis_summary(*, target_label: str, output_label: str, facts: dict[str, Any], generated_object: bool) -> str:
    if facts.get("result_summary"):
        return str(facts["result_summary"])
    if facts.get("result_text"):
        return str(facts["result_text"])
    if facts.get("result_value") is not None:
        return f"{output_label} = {format_scalar_value(facts['result_value'])}。"
    if facts.get("table_generated"):
        return f"已完成针对 {target_label} 的自定义 Python 分析，并生成结果表 {output_label}。"
    if generated_object:
        return f"已完成针对 {target_label} 的自定义 Python 分析，并生成对象 {output_label}。"
    return f"已完成针对 {target_label} 的自定义 Python 分析。"


def normalize_custom_scalar(value: Any) -> Any:
    if value is None:
        return None
    if isinstance(value, (str, bool, int, float)):
        return value
    if hasattr(value, "item"):
        try:
            item = value.item()
            if isinstance(item, (str, bool, int, float)):
                return item
        except Exception:
            return None
    return None


def infer_scalar_from_stdout(stdout_text: str) -> Any:
    text = stdout_text.strip()
    if text == "" or "\n" in text:
        return None
    if re.fullmatch(r"-?\d+", text):
        try:
            return int(text)
        except ValueError:
            return None
    if re.fullmatch(r"-?\d+\.\d+", text):
        try:
            return float(text)
        except ValueError:
            return None
    return None


def safe_table_length(table: Any) -> int | None:
    try:
        return int(len(table))
    except Exception:
        return None


def safe_table_columns(table: Any) -> list[str] | None:
    columns = getattr(table, "columns", None)
    if columns is None:
        return None
    try:
        return [str(value) for value in list(columns)[:12]]
    except Exception:
        return None


def format_scalar_value(value: Any) -> str:
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, float):
        if value.is_integer():
            return str(int(value))
        return f"{value:g}"
    return str(value)


STATE = RuntimeState()


def main() -> None:
    host = os.environ.get("SCAGENT_RUNTIME_HOST", "127.0.0.1")
    port = int(os.environ.get("SCAGENT_RUNTIME_PORT", "8081"))
    server = ThreadingHTTPServer((host, port), RequestHandler)
    print(f"runtime listening on http://{host}:{port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
