from __future__ import annotations

import json
import time
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler
from pathlib import Path
from typing import Any

from i18n import t, set_request_locale

from .diagnostics import dedupe_list


def log_runtime_event(logger: Any, event: str, **fields: Any) -> None:
    payload = {"event": event}
    for key, value in fields.items():
        if value in (None, "", [], {}):
            continue
        payload[key] = value
    logger.info(json.dumps(payload, ensure_ascii=False, default=str))


def build_request_handler(state: Any, logger: Any) -> type[BaseHTTPRequestHandler]:
    class RequestHandler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            locale = self.headers.get("Accept-Language", "zh").split(",")[0].strip()[:5]
            set_request_locale(locale if locale in ("zh", "en") else "zh")
            if self.path == "/healthz":
                plugin_skills = state.load_plugin_skills()
                executable_skills = dedupe_list(state.builtin_skills() + sorted(plugin_skills.keys()))
                notes = [
                    t("runtime.healthNote1"),
                    t("runtime.healthNote2"),
                    t("runtime.healthNote3"),
                ]
                disabled_bundles = state.load_disabled_bundles()
                if disabled_bundles:
                    notes.append(t("runtime.healthDisabledBundles", count=len(disabled_bundles)))
                if plugin_skills:
                    notes.append(t("runtime.healthPluginSkills", count=len(plugin_skills)))
                payload = {
                    "status": "ok",
                    "runtime_mode": "live",
                    "real_h5ad_inspection": True,
                    "real_analysis_execution": True,
                    "executable_skills": executable_skills,
                    "notes": notes,
                }
                payload.update(state.environment_report)
                self._write_json(HTTPStatus.OK, payload)
                return
            self._write_json(HTTPStatus.NOT_FOUND, {"error": t("error.endpointNotFound")})

        def do_POST(self) -> None:
            locale = self.headers.get("Accept-Language", "zh").split(",")[0].strip()[:5]
            set_request_locale(locale if locale in ("zh", "en") else "zh")
            payload: dict[str, Any] = {}
            started_at = time.perf_counter()
            try:
                payload = self._read_json()
                session_id = payload.get("session_id")
                request_id = payload.get("request_id")
                if self.path == "/sessions/init":
                    log_runtime_event(
                        logger,
                        "session_init_started",
                        session_id=session_id,
                        label=payload.get("label"),
                        workspace_root=payload.get("workspace_root"),
                    )
                    workspace_root = Path(payload["workspace_root"])
                    response = state.create_workspace_root(
                        session_id=payload["session_id"],
                        label=payload.get("label", "session"),
                        workspace_root=workspace_root,
                    )
                    log_runtime_event(
                        logger,
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
                        logger,
                        "load_file_started",
                        session_id=session_id,
                        label=payload.get("label"),
                        file_path=payload.get("file_path"),
                    )
                    response = state.load_file(
                        session_id=payload["session_id"],
                        file_path=Path(payload["file_path"]),
                        label=payload.get("label", ""),
                    )
                    log_runtime_event(
                        logger,
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
                        logger,
                        "ensure_object_started",
                        session_id=session_id,
                        backend_ref=object_payload.get("backend_ref"),
                        label=object_payload.get("label"),
                        materialized_path=object_payload.get("materialized_path"),
                    )
                    response = state.ensure_object(
                        session_id=payload["session_id"],
                        descriptor=object_payload,
                    )
                    log_runtime_event(
                        logger,
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
                        logger,
                        "job_started",
                        session_id=session_id,
                        request_id=request_id,
                        skill=payload.get("skill"),
                        target_backend_ref=payload.get("target_backend_ref"),
                        params=payload.get("params"),
                    )
                    response = state.execute(payload)
                    log_runtime_event(
                        logger,
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
                if self.path == "/sessions/cancel_execution":
                    log_runtime_event(
                        logger,
                        "job_cancel_started",
                        session_id=session_id,
                        request_id=request_id,
                    )
                    response = state.cancel_execution(
                        session_id=payload["session_id"],
                        request_id=str(payload.get("request_id") or "").strip() or None,
                    )
                    log_runtime_event(
                        logger,
                        "job_cancel_finished",
                        session_id=session_id,
                        request_id=request_id,
                        duration_ms=round((time.perf_counter() - started_at) * 1000, 2),
                        stopped=response.get("stopped"),
                        isolated=response.get("isolated"),
                        active_request_id=response.get("active_request_id"),
                        active_operation=response.get("active_operation"),
                    )
                    self._write_json(HTTPStatus.OK, response)
                    return
                self._write_json(HTTPStatus.NOT_FOUND, {"error": t("error.endpointNotFound")})
            except Exception as exc:  # noqa: BLE001
                log_runtime_event(
                    logger,
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
            try:
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            except (BrokenPipeError, ConnectionResetError):
                return

    return RequestHandler
