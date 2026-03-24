from __future__ import annotations

import json
import shutil
from pathlib import Path
from typing import Any

from i18n import t

from .diagnostics import build_dataset_assessment, describe_annotation_summary, inspect_h5ad_metadata, inspect_h5ad_shape, slug


class RuntimeObjectStore:
    object_type: type[Any]
    objects: dict[str, Any]
    sample_path: Path

    def create_workspace_root(self, session_id: str, label: str, workspace_root: Path) -> dict[str, Any]:
        objects_dir = workspace_root / "objects"
        artifacts_dir = workspace_root / "artifacts"
        objects_dir.mkdir(parents=True, exist_ok=True)
        artifacts_dir.mkdir(parents=True, exist_ok=True)

        backend_ref = self.next_ref(session_id)
        sample_info = self._load_sample(session_id, label, objects_dir)
        materialized_path = sample_info["materialized_path"]

        obj = self.object_type(
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
            raise RuntimeError(t("error.uploadFileNotFound", path=file_path))

        backend_ref = self.next_ref(session_id)
        n_obs, n_vars = inspect_h5ad_shape(file_path)
        object_label = label or file_path.stem
        obj = self.object_type(
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
            "summary": t("runtime.loadFileSummary", filename=file_path.name, label=object_label, n_obs=n_obs, n_vars=n_vars, annotation_note=annotation_note),
        }

    def ensure_object(self, session_id: str, descriptor: dict[str, Any]) -> dict[str, Any]:
        backend_ref = str(descriptor.get("backend_ref") or "").strip()
        if backend_ref and backend_ref in self.objects:
            obj = self.objects[backend_ref]
            return {
                "object": self._descriptor(obj),
                "summary": t("runtime.objectAlreadyExists", label=obj.label),
            }

        materialized_path_raw = str(descriptor.get("materialized_path") or "").strip()
        if materialized_path_raw == "":
            raise RuntimeError(t("error.restoreObjectMissingPath"))

        materialized_path = Path(materialized_path_raw)
        if not materialized_path.exists():
            raise RuntimeError(t("error.restoreObjectFileNotFound", path=materialized_path))

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

        obj = self.object_type(
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
            "summary": t("runtime.objectRestored", label=label),
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
                "summary": t("runtime.sessionInitFromSample", filename=self.sample_path.name, annotation_note=describe_annotation_summary(metadata)),
            }

        materialized_path = objects_dir / "pbmc3k_demo.h5ad"
        materialized_path.write_text(
            json.dumps(
                {
                    "session_id": session_id,
                    "label": label,
                    "kind": "raw_dataset",
                    "note": "placeholder h5ad generated by MVP runtime",
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
            "summary": t("runtime.sessionInitFallback"),
        }
