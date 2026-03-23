#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

if [[ -f ".env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source ".env"
  set +a
fi

: "${SCAGENT_RUNTIME_HOST:=127.0.0.1}"
: "${SCAGENT_RUNTIME_PORT:=8081}"

export SCAGENT_RUNTIME_HOST
export SCAGENT_RUNTIME_PORT

exec "$@"
