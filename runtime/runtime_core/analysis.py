from __future__ import annotations

from typing import Any

_ANALYSIS_MODULES: tuple[Any, Any, Any, Any, Any] | None = None
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
