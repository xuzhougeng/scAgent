from __future__ import annotations

import json
import os
import signal
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import Any


def _log_worker_event(logger: Any, event: str, **fields: Any) -> None:
    payload = {"event": event}
    for key, value in fields.items():
        if value in (None, "", [], {}):
            continue
        payload[key] = value
    logger.info(json.dumps(payload, ensure_ascii=False, default=str))


class SessionWorkerError(RuntimeError):
    pass


class SessionWorkerHandle:
    def __init__(self, session_id: str, worker_script: Path, repo_root: Path, logger: Any) -> None:
        self.session_id = session_id
        self._worker_script = worker_script
        self._repo_root = repo_root
        self._logger = logger
        self._request_lock = threading.Lock()
        self._state_lock = threading.Lock()
        self._proc: subprocess.Popen[str] | None = None
        self._active_request_id = ""
        self._active_operation = ""

    def invoke(self, op: str, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = str(payload.get("request_id") or op).strip() or op
        with self._request_lock:
            proc = self._ensure_process()
            with self._state_lock:
                self._active_request_id = request_id
                self._active_operation = op

            try:
                self._send_request(proc, op, payload)
                response = self._read_response(proc, request_id)
            finally:
                with self._state_lock:
                    if self._active_request_id == request_id:
                        self._active_request_id = ""
                        self._active_operation = ""

        if not response.get("ok"):
            raise SessionWorkerError(str(response.get("error") or "session worker request failed"))
        result = response.get("result")
        if not isinstance(result, dict):
            raise SessionWorkerError("session worker returned an invalid response payload")
        return result

    def interrupt(self, request_id: str | None = None, *, grace_period_sec: float = 1.5) -> dict[str, Any]:
        with self._state_lock:
            proc = self._proc
            active_request_id = self._active_request_id
            active_operation = self._active_operation
            if proc is None:
                return {
                    "summary": "当前 session 没有活跃的运行时 worker。",
                    "stopped": False,
                    "isolated": False,
                    "active_request_id": active_request_id,
                    "active_operation": active_operation,
                }

            self._proc = None
            self._active_request_id = ""
            self._active_operation = ""

        self._close_pipes(proc)
        stopped, exit_signal = self._terminate_process_group(proc, grace_period_sec)
        summary = "已终止会话执行 worker，后续请求会按需重建运行环境。"
        if not stopped:
            summary = "已隔离当前会话 worker，但进程仍未退出；后续请求会重建运行环境。"
        _log_worker_event(
            self._logger,
            "session_worker_stopped",
            session_id=self.session_id,
            pid=proc.pid,
            stopped=stopped,
            isolated=not stopped,
            signal=exit_signal,
            active_request_id=active_request_id,
            active_operation=active_operation,
        )
        return {
            "summary": summary,
            "stopped": stopped,
            "isolated": not stopped,
            "active_request_id": active_request_id,
            "active_operation": active_operation,
        }

    def _ensure_process(self) -> subprocess.Popen[str]:
        with self._state_lock:
            proc = self._proc
            if proc is not None and proc.poll() is None:
                return proc
            if proc is not None:
                self._close_pipes(proc)

            proc = subprocess.Popen(
                [sys.executable, "-u", str(self._worker_script), "--session-id", self.session_id],
                cwd=self._repo_root,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                encoding="utf-8",
                bufsize=1,
                start_new_session=True,
            )
            self._proc = proc
            threading.Thread(target=self._drain_stderr, args=(proc,), daemon=True).start()
            _log_worker_event(
                self._logger,
                "session_worker_started",
                session_id=self.session_id,
                pid=proc.pid,
            )
            return proc

    def _send_request(self, proc: subprocess.Popen[str], op: str, payload: dict[str, Any]) -> None:
        if proc.stdin is None:
            raise SessionWorkerError("session worker stdin is unavailable")
        envelope = {"op": op, "payload": payload}
        try:
            proc.stdin.write(json.dumps(envelope, ensure_ascii=False) + "\n")
            proc.stdin.flush()
        except (BrokenPipeError, OSError, ValueError) as exc:
            raise SessionWorkerError("session worker connection closed before the request was sent") from exc

    def _read_response(self, proc: subprocess.Popen[str], request_id: str) -> dict[str, Any]:
        if proc.stdout is None:
            raise SessionWorkerError("session worker stdout is unavailable")
        try:
            raw = proc.stdout.readline()
        except (OSError, ValueError) as exc:
            raise SessionWorkerError(f"session worker connection closed while handling `{request_id}`") from exc
        if raw == "":
            return_code = proc.poll()
            if return_code is None:
                raise SessionWorkerError(f"session worker stopped responding while handling `{request_id}`")
            raise SessionWorkerError(f"session worker exited while handling `{request_id}` (code={return_code})")
        try:
            payload = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise SessionWorkerError("session worker returned malformed JSON") from exc
        if not isinstance(payload, dict):
            raise SessionWorkerError("session worker returned an invalid response envelope")
        return payload

    def _drain_stderr(self, proc: subprocess.Popen[str]) -> None:
        if proc.stderr is None:
            return
        try:
            for raw in proc.stderr:
                line = raw.strip()
                if not line:
                    continue
                _log_worker_event(
                    self._logger,
                    "session_worker_stderr",
                    session_id=self.session_id,
                    pid=proc.pid,
                    message=line,
                )
        except Exception:  # noqa: BLE001
            return

    @staticmethod
    def _close_pipes(proc: subprocess.Popen[str] | None) -> None:
        if proc is None:
            return
        for stream_name in ("stdin", "stdout", "stderr"):
            stream = getattr(proc, stream_name, None)
            if stream is None:
                continue
            try:
                stream.close()
            except Exception:  # noqa: BLE001
                continue

    @staticmethod
    def _terminate_process_group(proc: subprocess.Popen[str], grace_period_sec: float) -> tuple[bool, str]:
        if proc.poll() is not None:
            return True, "none"

        try:
            pgid = os.getpgid(proc.pid)
        except ProcessLookupError:
            return True, "none"

        for sig in (signal.SIGTERM, signal.SIGKILL):
            try:
                os.killpg(pgid, sig)
            except ProcessLookupError:
                return True, sig.name

            deadline = time.monotonic() + grace_period_sec
            while time.monotonic() < deadline:
                if proc.poll() is not None:
                    return True, sig.name
                time.sleep(0.05)

        return proc.poll() is not None, signal.SIGKILL.name


class SessionWorkerManager:
    def __init__(self, logger: Any) -> None:
        runtime_dir = Path(__file__).resolve().parents[1]
        self._repo_root = runtime_dir.parent
        self._worker_script = runtime_dir / "session_worker.py"
        self._logger = logger
        self._lock = threading.Lock()
        self._workers: dict[str, SessionWorkerHandle] = {}

    def create_workspace_root(self, *, session_id: str, label: str, workspace_root: Path) -> dict[str, Any]:
        return self._worker(session_id).invoke(
            "session_init",
            {
                "session_id": session_id,
                "label": label,
                "workspace_root": str(workspace_root),
            },
        )

    def load_file(self, *, session_id: str, file_path: Path, label: str) -> dict[str, Any]:
        return self._worker(session_id).invoke(
            "load_file",
            {
                "session_id": session_id,
                "file_path": str(file_path),
                "label": label,
            },
        )

    def ensure_object(self, *, session_id: str, descriptor: dict[str, Any]) -> dict[str, Any]:
        return self._worker(session_id).invoke(
            "ensure_object",
            {
                "session_id": session_id,
                "object": descriptor,
            },
        )

    def execute(self, payload: dict[str, Any]) -> dict[str, Any]:
        session_id = str(payload.get("session_id") or "").strip()
        if session_id == "":
            raise SessionWorkerError("missing session_id")
        return self._worker(session_id).invoke("execute", payload)

    def cancel_execution(self, *, session_id: str, request_id: str | None = None) -> dict[str, Any]:
        with self._lock:
            handle = self._workers.pop(session_id, None)
        if handle is None:
            return {
                "summary": "当前 session 没有可停止的运行时 worker。",
                "stopped": False,
                "isolated": False,
            }
        return handle.interrupt(request_id)

    def _worker(self, session_id: str) -> SessionWorkerHandle:
        with self._lock:
            handle = self._workers.get(session_id)
            if handle is None:
                handle = SessionWorkerHandle(session_id, self._worker_script, self._repo_root, self._logger)
                self._workers[session_id] = handle
            return handle
