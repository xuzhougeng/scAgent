#!/usr/bin/env python3

from __future__ import annotations

import logging
import os
import sys
from http.server import ThreadingHTTPServer
from pathlib import Path

RUNTIME_DIR = Path(__file__).resolve().parent
if str(RUNTIME_DIR) not in sys.path:
    sys.path.insert(0, str(RUNTIME_DIR))

from runtime_core import ManagedRuntimeState, build_request_handler

logging.basicConfig(
    level=os.environ.get("SCAGENT_LOG_LEVEL", "INFO").upper(),
    format="%(asctime)s %(levelname)s %(message)s",
)
LOGGER = logging.getLogger("scagent.runtime")

os.environ.setdefault("NUMBA_CACHE_DIR", "/tmp/scagent-numba")
os.environ.setdefault("MPLCONFIGDIR", "/tmp/scagent-mpl")
os.environ.setdefault("MPLBACKEND", "Agg")

STATE = ManagedRuntimeState(LOGGER)
RequestHandler = build_request_handler(STATE, LOGGER)


def ensure_runtime_environment() -> None:
    if os.environ.get("SCAGENT_ALLOW_UNHEALTHY_ENV", "") == "1":
        return

    failures = STATE.failing_package_checks()
    if not failures:
        return

    details = "; ".join(f"{item['name']}: {item.get('detail', '')}" for item in failures)
    raise RuntimeError(
        "运行时依赖环境不完整，无法安全启动。"
        f" 当前失败项：{details}。"
        " 请优先使用 `pixi install && pixi run doctor` 修复环境，"
        "并通过 `./start.sh` 或 `pixi run runtime` 启动。"
        " 若确需跳过该检查，可设置 SCAGENT_ALLOW_UNHEALTHY_ENV=1。"
    )


def main() -> None:
    ensure_runtime_environment()
    host = os.environ.get("SCAGENT_RUNTIME_HOST", "127.0.0.1")
    port = int(os.environ.get("SCAGENT_RUNTIME_PORT", "8081"))
    server = ThreadingHTTPServer((host, port), RequestHandler)
    print(f"runtime listening on http://{host}:{port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
