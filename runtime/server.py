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


def main() -> None:
    host = os.environ.get("SCAGENT_RUNTIME_HOST", "127.0.0.1")
    port = int(os.environ.get("SCAGENT_RUNTIME_PORT", "8081"))
    server = ThreadingHTTPServer((host, port), RequestHandler)
    print(f"runtime listening on http://{host}:{port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
