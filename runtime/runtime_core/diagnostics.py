from __future__ import annotations

import importlib
import io
import re
import sys
from contextlib import redirect_stderr, redirect_stdout
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


def failing_package_checks(report: dict[str, Any]) -> list[dict[str, Any]]:
    checks = report.get("environment_checks") or []
    if not isinstance(checks, list):
        return []

    package_names = {label for label, _, _ in ENVIRONMENT_PACKAGES}
    failures: list[dict[str, Any]] = []
    for item in checks:
        if not isinstance(item, dict):
            continue
        name = str(item.get("name") or "").strip()
        if name not in package_names:
            continue
        if bool(item.get("ok")):
            continue
        failures.append(item)
    return failures


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


def build_inspect_dataset_facts(target: Any) -> dict[str, Any]:
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
