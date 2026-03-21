#!/usr/bin/env python3

from __future__ import annotations

import importlib
from importlib import metadata
import io
import json
import os
import sys
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path


PACKAGES: list[tuple[str, str, str]] = [
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


def check_packages() -> tuple[dict[str, str], list[str]]:
    versions: dict[str, str] = {}
    failures: list[str] = []
    for label, module_name, dist_name in PACKAGES:
        try:
            with io.StringIO() as sink, redirect_stdout(sink), redirect_stderr(sink):
                importlib.import_module(module_name)
            versions[label] = metadata.version(dist_name)
        except Exception as exc:  # pragma: no cover - diagnostic path
            failures.append(f"{label}: {exc}")
    return versions, failures


def inspect_sample(path: Path) -> dict[str, object] | None:
    if not path.exists():
        return None

    with io.StringIO() as sink, redirect_stdout(sink), redirect_stderr(sink):
        anndata = importlib.import_module("anndata")
        adata = anndata.read_h5ad(path, backed="r")
    try:
        summary = {
            "path": str(path),
            "n_obs": int(adata.n_obs),
            "n_vars": int(adata.n_vars),
            "obs_fields": list(adata.obs.columns[:12]),
            "var_fields": list(adata.var.columns[:12]),
            "obsm_keys": list(adata.obsm.keys()),
        }
    finally:
        file_manager = getattr(adata, "file", None)
        if file_manager is not None:
            file_manager.close()
    return summary


def main() -> int:
    versions, failures = check_packages()
    payload: dict[str, object] = {
        "python": sys.version.split()[0],
        "runtime_host": os.environ.get("SCAGENT_RUNTIME_HOST", "127.0.0.1"),
        "runtime_port": os.environ.get("SCAGENT_RUNTIME_PORT", "8081"),
        "packages": versions,
    }

    sample_path = Path(os.environ.get("SCAGENT_SAMPLE_H5AD", "data/samples/pbmc3k.h5ad"))
    try:
        sample_summary = inspect_sample(sample_path)
        if sample_summary is not None:
            payload["sample_h5ad"] = sample_summary
    except Exception as exc:  # pragma: no cover - diagnostic path
        failures.append(f"sample_h5ad: {exc}")

    if failures:
        payload["failures"] = failures

    print(json.dumps(payload, indent=2))
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
