#!/usr/bin/env python3

from __future__ import annotations

import json
import logging
import os
import re
import sys
from http.server import ThreadingHTTPServer
from pathlib import Path
from typing import Any

RUNTIME_DIR = Path(__file__).resolve().parent
if str(RUNTIME_DIR) not in sys.path:
    sys.path.insert(0, str(RUNTIME_DIR))

from runtime_core import (
    SAFE_EXEC_BUILTINS,
    RuntimeObject,
    RuntimeObjectStore,
    analysis_modules,
    build_environment_report,
    build_request_handler,
    build_inspect_dataset_facts,
    build_custom_analysis_facts,
    dedupe_list,
    default_custom_analysis_summary,
    describe_annotation_summary,
    format_list_zh,
    format_object_state_zh,
    inspect_h5ad_metadata,
    inspect_h5ad_shape,
    matrix_has_negative_values,
    slug,
)
from skill_runtime import SkillRegistry, SkillRuntimeSupport, register_builtin_skills

logging.basicConfig(
    level=os.environ.get("SCAGENT_LOG_LEVEL", "INFO").upper(),
    format="%(asctime)s %(levelname)s %(message)s",
)
LOGGER = logging.getLogger("scagent.runtime")

os.environ.setdefault("NUMBA_CACHE_DIR", "/tmp/scagent-numba")
os.environ.setdefault("MPLCONFIGDIR", "/tmp/scagent-mpl")
os.environ.setdefault("MPLBACKEND", "Agg")

BUILTIN_BUNDLE_ID = "builtin-core"
BACKEND_REF_RE = re.compile(r"^py:[^:]+:adata_(\d+)$")


class RuntimeState(RuntimeObjectStore, SkillRuntimeSupport):
    object_type = RuntimeObject

    def __init__(self) -> None:
        self.counter = 0
        self.objects: dict[str, RuntimeObject] = {}
        self.sample_path = Path(os.environ.get("SCAGENT_SAMPLE_H5AD", "data/samples/pbmc3k.h5ad"))
        self.plugin_root = Path(os.environ.get("SCAGENT_PLUGIN_DIR", "data/skill-hub/plugins"))
        self.plugin_state_path = Path(os.environ.get("SCAGENT_PLUGIN_STATE_PATH", str(self.plugin_root.parent / "state.json")))
        self.skill_registry = SkillRegistry(BUILTIN_BUNDLE_ID)
        register_builtin_skills(self.skill_registry)
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
        return self.skill_registry.builtin_skills(self)

    def next_ref(self, session_id: str) -> str:
        self.counter += 1
        return f"py:{session_id}:adata_{self.counter}"

    def _sync_counter_with_backend_ref(self, backend_ref: str) -> None:
        match = BACKEND_REF_RE.match(str(backend_ref or "").strip())
        if match is None:
            return
        self.counter = max(self.counter, int(match.group(1)))

    def load_plugin_skills(self) -> dict[str, Any]:
        return self.skill_registry.load_plugin_skills(self)

    def skill_enabled(self, skill_name: str) -> bool:
        return self.skill_registry.skill_enabled(self, skill_name)

    @staticmethod
    def analysis_modules() -> tuple[Any, Any, Any, Any, Any]:
        return analysis_modules()

    @staticmethod
    def matrix_has_negative_values(matrix: Any) -> bool:
        return matrix_has_negative_values(matrix)

    @staticmethod
    def safe_exec_builtins() -> dict[str, Any]:
        return SAFE_EXEC_BUILTINS

    @staticmethod
    def format_list_zh(values: list[str]) -> str:
        return format_list_zh(values)

    @staticmethod
    def format_object_state_zh(state: str) -> str:
        return format_object_state_zh(state)

    @staticmethod
    def describe_annotation_summary(metadata: dict[str, Any]) -> str:
        return describe_annotation_summary(metadata)

    @staticmethod
    def build_inspect_dataset_facts(target: RuntimeObject) -> dict[str, Any]:
        return build_inspect_dataset_facts(target)

    @staticmethod
    def build_custom_analysis_facts(**kwargs: Any) -> dict[str, Any]:
        return build_custom_analysis_facts(**kwargs)

    @staticmethod
    def default_custom_analysis_summary(*, target_label: str, output_label: str, facts: dict[str, Any], generated_object: bool) -> str:
        return default_custom_analysis_summary(
            target_label=target_label,
            output_label=output_label,
            facts=facts,
            generated_object=generated_object,
        )

    @staticmethod
    def slug(value: str) -> str:
        return slug(value)

    @staticmethod
    def dedupe_list(values: list[str]) -> list[str]:
        return dedupe_list(values)

    @staticmethod
    def inspect_h5ad_shape(path: Path) -> tuple[int, int]:
        return inspect_h5ad_shape(path)

    @staticmethod
    def inspect_h5ad_metadata(path: Path) -> dict[str, Any]:
        return inspect_h5ad_metadata(path)


STATE = RuntimeState()
RequestHandler = build_request_handler(STATE, LOGGER)


def main() -> None:
    host = os.environ.get("SCAGENT_RUNTIME_HOST", "127.0.0.1")
    port = int(os.environ.get("SCAGENT_RUNTIME_PORT", "8081"))
    server = ThreadingHTTPServer((host, port), RequestHandler)
    print(f"runtime listening on http://{host}:{port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
