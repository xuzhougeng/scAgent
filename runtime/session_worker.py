#!/usr/bin/env python3

from __future__ import annotations

import argparse
import json
import logging
import os
import sys
from pathlib import Path
from typing import Any

RUNTIME_DIR = Path(__file__).resolve().parent
if str(RUNTIME_DIR) not in sys.path:
    sys.path.insert(0, str(RUNTIME_DIR))

from runtime_core import RuntimeState

logging.basicConfig(
    level=os.environ.get("SCAGENT_LOG_LEVEL", "INFO").upper(),
    format="%(asctime)s %(levelname)s %(message)s",
)
LOGGER = logging.getLogger("scagent.runtime.worker")


def handle_request(state: RuntimeState, session_id: str, envelope: dict[str, Any]) -> dict[str, Any]:
    op = str(envelope.get("op") or "").strip()
    payload = envelope.get("payload") or {}
    if not isinstance(payload, dict):
        raise RuntimeError("worker payload must be a JSON object")

    payload_session_id = str(payload.get("session_id") or session_id).strip()
    if payload_session_id != session_id:
        raise RuntimeError(f"worker session mismatch: expected {session_id}, got {payload_session_id}")

    if op == "session_init":
        return state.create_workspace_root(
            session_id=session_id,
            label=str(payload.get("label") or "session"),
            workspace_root=Path(str(payload["workspace_root"])),
        )
    if op == "load_file":
        return state.load_file(
            session_id=session_id,
            file_path=Path(str(payload["file_path"])),
            label=str(payload.get("label") or ""),
        )
    if op == "ensure_object":
        return state.ensure_object(
            session_id=session_id,
            descriptor=payload.get("object") or {},
        )
    if op == "execute":
        return state.execute(payload)
    raise RuntimeError(f"unsupported worker operation: {op}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--session-id", required=True)
    args = parser.parse_args()

    state = RuntimeState()
    session_id = str(args.session_id).strip()
    if session_id == "":
        raise RuntimeError("missing session id")

    for raw in sys.stdin:
        line = raw.strip()
        if line == "":
            continue

        try:
            envelope = json.loads(line)
            if not isinstance(envelope, dict):
                raise RuntimeError("worker request must be a JSON object")
            response = {
                "ok": True,
                "result": handle_request(state, session_id, envelope),
            }
        except Exception as exc:  # noqa: BLE001
            LOGGER.exception("session worker request failed")
            response = {
                "ok": False,
                "error": str(exc),
            }

        try:
            sys.stdout.write(json.dumps(response, ensure_ascii=False) + "\n")
            sys.stdout.flush()
        except (BrokenPipeError, OSError):
            return


if __name__ == "__main__":
    main()
