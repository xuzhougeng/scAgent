#!/usr/bin/env python3

from __future__ import annotations

import csv
import importlib
import io
import json
import logging
import os
import random
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
        self.environment_report = build_environment_report(self.sample_path)

    def next_ref(self, session_id: str) -> str:
        self.counter += 1
        return f"py:{session_id}:adata_{self.counter}"

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

    def execute(self, payload: dict[str, Any]) -> dict[str, Any]:
        skill = payload["skill"]
        session_id = payload["session_id"]
        session_root = Path(payload["session_root"])
        target = self.objects.get(payload.get("target_backend_ref", ""))
        params = payload.get("params", {})

        if skill in {"inspect_dataset", "assess_dataset"}:
            if not target:
                raise RuntimeError(f"{skill} 需要一个目标对象")
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

        if skill == "subset_cells":
            if not target:
                raise RuntimeError("subset_cells 需要一个目标对象")
            value = params.get("value", "subset")
            new_n_obs = max(100, int(target.n_obs * 0.44))
            return self._new_object_response(
                session_id=session_id,
                session_root=session_root,
                label=f"subset_{value}",
                kind="subset",
                n_obs=new_n_obs,
                n_vars=target.n_vars,
                summary=f"已从 {target.label} 生成子集 subset_{value}，包含 {new_n_obs} 个细胞。",
            )

        if skill == "recluster":
            if not target:
                raise RuntimeError("recluster 需要一个目标对象")
            resolution = params.get("resolution", 0.6)
            return self._new_object_response(
                session_id=session_id,
                session_root=session_root,
                label=f"reclustered_{target.label}",
                kind="reclustered_subset",
                n_obs=target.n_obs,
                n_vars=target.n_vars,
                summary=f"已对 {target.label} 完成重新聚类，分辨率为 {resolution}。",
            )

        if skill == "find_markers":
            if not target:
                raise RuntimeError("find_markers 需要一个目标对象")
            path = session_root / "artifacts" / f"markers_{slug(target.label)}.csv"
            with path.open("w", newline="", encoding="utf-8") as handle:
                writer = csv.writer(handle)
                writer.writerow(["cluster", "gene", "score", "logfc"])
                rows = [
                    ("0", "WOX5", "18.2", "2.8"),
                    ("1", "SCR", "15.6", "2.4"),
                    ("2", "PLT1", "14.8", "2.2"),
                    ("3", "CYCD6", "13.1", "1.9"),
                ]
                writer.writerows(rows)
            return {
                "summary": f"已为 {target.label} 生成 marker 表。",
                "artifacts": [
                    {
                        "kind": "table",
                        "title": f"{target.label} 的 marker 表",
                        "path": str(path),
                        "content_type": "text/csv",
                        "summary": "按 leiden 聚类汇总的高分 marker 基因。",
                    }
                ],
                "metadata": {"groupby": params.get("groupby", "leiden")},
            }

        if skill in {"plot_umap", "plot_dotplot", "plot_violin"}:
            if not target:
                raise RuntimeError(f"{skill} 需要一个目标对象")
            title, note = {
                "plot_umap": ("UMAP 占位示意图", "当前不是基于真实 UMAP 坐标绘制。"),
                "plot_dotplot": ("Marker 点图占位示意图", "当前不是基于真实表达矩阵绘制。"),
                "plot_violin": ("基因小提琴图占位示意图", "当前不是基于真实表达矩阵绘制。"),
            }[skill]
            path = session_root / "artifacts" / f"{skill}_{slug(target.label)}.svg"
            path.write_text(self._build_svg(title, target.label, note), encoding="utf-8")
            return {
                "summary": f"已生成 {target.label} 的 {title}。{note}",
                "artifacts": [
                    {
                        "kind": "plot",
                        "title": title,
                        "path": str(path),
                        "content_type": "image/svg+xml",
                        "summary": f"{target.label} 的 {title}。{note}",
                    }
                ],
                "metadata": {"placeholder_plot": True, "note": note},
            }

        if skill == "export_h5ad":
            if not target:
                raise RuntimeError("export_h5ad 需要一个目标对象")
            export_path = session_root / "objects" / f"{slug(target.label)}.h5ad"
            export_path.write_text(
                json.dumps(
                    {
                        "exported_from": target.backend_ref,
                        "label": target.label,
                        "kind": target.kind,
                        "n_obs": target.n_obs,
                        "n_vars": target.n_vars,
                    },
                    indent=2,
                ),
                encoding="utf-8",
            )
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

    def _new_object_response(
        self,
        session_id: str,
        session_root: Path,
        label: str,
        kind: str,
        n_obs: int,
        n_vars: int,
        summary: str,
    ) -> dict[str, Any]:
        backend_ref = self.next_ref(session_id)
        materialized_path = session_root / "objects" / f"{slug(label)}.h5ad"
        materialized_path.write_text(
            json.dumps(
                {
                    "label": label,
                    "kind": kind,
                    "n_obs": n_obs,
                    "n_vars": n_vars,
                    "note": "占位派生对象",
                },
                indent=2,
            ),
            encoding="utf-8",
        )
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
            metadata={},
        )
        self.objects[backend_ref] = obj
        return {
            "summary": summary,
            "object": self._descriptor(obj),
        }

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

    def _build_svg(self, title: str, label: str, note: str = "") -> str:
        random.seed(f"{title}:{label}")
        dots = []
        for _ in range(26):
            x = 60 + random.randint(0, 420)
            y = 70 + random.randint(0, 220)
            radius = 5 + random.randint(0, 8)
            shade = 45 + random.randint(0, 40)
            dots.append(
                f'<circle cx="{x}" cy="{y}" r="{radius}" fill="hsl(135 30% {shade}%)" opacity="0.82" />'
            )
        rings = []
        for idx in range(3):
            rings.append(
                f'<path d="M 60 {120 + idx * 60} C 180 {40 + idx * 50}, 300 {260 - idx * 30}, 480 {100 + idx * 50}" stroke="rgba(197,136,49,0.35)" fill="none" stroke-width="{2 + idx}" />'
            )
        return f"""
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 540 320">
  <rect width="540" height="320" rx="24" fill="#f7faf2" />
  <text x="36" y="48" font-size="24" font-family="IBM Plex Sans, sans-serif" fill="#234631">{title}</text>
  <text x="36" y="76" font-size="14" font-family="IBM Plex Sans, sans-serif" fill="#68746c">{label}</text>
  <text x="36" y="102" font-size="12" font-family="IBM Plex Sans, sans-serif" fill="#9b5b1d">{note}</text>
  {''.join(rings)}
  {''.join(dots)}
</svg>
""".strip()


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
            payload = {
                "status": "ok",
                "runtime_mode": "hybrid_demo",
                "real_h5ad_inspection": True,
                "real_analysis_execution": False,
                "executable_skills": [
                    "inspect_dataset",
                    "assess_dataset",
                    "subset_cells",
                    "recluster",
                    "find_markers",
                    "plot_umap",
                    "plot_dotplot",
                    "plot_violin",
                    "export_h5ad",
                ],
                "notes": [
                    "运行时会读取真实的 h5ad 结构和注释信息。",
                    "subset、recluster、marker 和绘图等分析步骤目前仍是占位实现。",
                    "plot_umap 当前返回的是占位 SVG，而不是真实 UMAP 坐标绘图。",
                ],
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

    available_analyses = ["inspect_dataset", "subset_cells", "export_h5ad"]
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
