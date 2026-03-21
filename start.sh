#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

if [[ -f ".env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source ".env"
  set +a
fi

: "${SCAGENT_ADDR:=:8080}"
: "${SCAGENT_RUNTIME_HOST:=127.0.0.1}"
: "${SCAGENT_RUNTIME_PORT:=8081}"
: "${SCAGENT_RUNTIME_URL:=http://${SCAGENT_RUNTIME_HOST}:${SCAGENT_RUNTIME_PORT}}"
: "${SCAGENT_PLANNER_MODE:=fake}"
: "${SCAGENT_SKILLS_PATH:=skills/registry.json}"
: "${SCAGENT_DOCS_DIR:=docs}"
: "${SCAGENT_DATA_DIR:=data}"
: "${SCAGENT_WEB_DIR:=web}"
: "${SCAGENT_OPENAI_BASE_URL:=https://api.openai.com/v1}"
: "${SCAGENT_OPENAI_MODEL:=gpt-5.4}"
: "${SCAGENT_OPENAI_REASONING_EFFORT:=low}"
: "${SCAGENT_OPENAI_API_KEY:=}"
: "${SCAGENT_USE_PIXI:=1}"

if [[ "${SCAGENT_PLANNER_MODE}" == "llm" && -z "${SCAGENT_OPENAI_API_KEY}" ]]; then
  echo "SCAGENT_OPENAI_API_KEY is required when SCAGENT_PLANNER_MODE=llm" >&2
  exit 1
fi

runtime_cmd=(python3 runtime/server.py)
if [[ "${SCAGENT_USE_PIXI}" != "0" ]] && command -v pixi >/dev/null 2>&1; then
  runtime_cmd=(pixi run runtime)
fi

cleanup() {
  if [[ -n "${RUNTIME_PID:-}" ]]; then
    kill "${RUNTIME_PID}" >/dev/null 2>&1 || true
    wait "${RUNTIME_PID}" >/dev/null 2>&1 || true
  fi
}

trap cleanup EXIT INT TERM

echo "scAgent startup configuration"
echo "  planner mode:    ${SCAGENT_PLANNER_MODE}"
echo "  runtime command: ${runtime_cmd[*]}"
echo "  runtime url:     ${SCAGENT_RUNTIME_URL}"
echo "  server addr:     ${SCAGENT_ADDR}"

"${runtime_cmd[@]}" &
RUNTIME_PID=$!

for _ in $(seq 1 30); do
  if curl -s "${SCAGENT_RUNTIME_URL}/healthz" >/dev/null; then
    break
  fi
  sleep 1
done

if ! curl -s "${SCAGENT_RUNTIME_URL}/healthz" >/dev/null; then
  echo "runtime did not become healthy at ${SCAGENT_RUNTIME_URL}" >&2
  exit 1
fi

echo "runtime is healthy, starting Go control plane"

go run ./cmd/scagent \
  -addr "${SCAGENT_ADDR}" \
  -runtime-url "${SCAGENT_RUNTIME_URL}" \
  -skills-path "${SCAGENT_SKILLS_PATH}" \
  -docs-dir "${SCAGENT_DOCS_DIR}" \
  -data-dir "${SCAGENT_DATA_DIR}" \
  -web-dir "${SCAGENT_WEB_DIR}" \
  -planner-mode "${SCAGENT_PLANNER_MODE}" \
  -openai-api-key "${SCAGENT_OPENAI_API_KEY}" \
  -openai-base-url "${SCAGENT_OPENAI_BASE_URL}" \
  -openai-model "${SCAGENT_OPENAI_MODEL}" \
  -openai-reasoning "${SCAGENT_OPENAI_REASONING_EFFORT}"
