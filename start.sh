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
: "${SCAGENT_PLUGIN_DIR:=data/skill-hub/plugins}"
: "${SCAGENT_PLUGIN_STATE_PATH:=data/skill-hub/state.json}"
: "${SCAGENT_DOCS_DIR:=docs}"
: "${SCAGENT_DATA_DIR:=data}"
: "${SCAGENT_WEB_DIR:=web}"
: "${SCAGENT_SAMPLE_H5AD:=${SCAGENT_DATA_DIR}/samples/pbmc3k.h5ad}"
: "${SCAGENT_SAMPLE_H5AD_URL:=https://exampledata.scverse.org/tutorials/scverse-getting-started-anndata-pbmc3k_processed.h5ad}"
: "${SCAGENT_SAMPLE_AUTO_DOWNLOAD:=1}"
: "${SCAGENT_OPENAI_BASE_URL:=https://api.openai.com/v1}"
: "${SCAGENT_OPENAI_MODEL:=gpt-5.4}"
: "${SCAGENT_OPENAI_REASONING_EFFORT:=low}"
: "${SCAGENT_OPENAI_API_KEY:=}"
: "${SCAGENT_USE_PIXI:=1}"
: "${SCAGENT_PIXI_BIN:=}"
: "${SCAGENT_NUMBA_CACHE_DIR:=/tmp/scagent-numba}"
: "${SCAGENT_MPLCONFIGDIR:=/tmp/scagent-mpl}"

export NUMBA_CACHE_DIR="${SCAGENT_NUMBA_CACHE_DIR}"
export MPLCONFIGDIR="${SCAGENT_MPLCONFIGDIR}"
export MPLBACKEND="${MPLBACKEND:-Agg}"
export SCAGENT_PLUGIN_DIR
export SCAGENT_PLUGIN_STATE_PATH
export SCAGENT_SAMPLE_H5AD

resolve_pixi_bin() {
  if [[ -n "${SCAGENT_PIXI_BIN}" ]]; then
    if [[ -x "${SCAGENT_PIXI_BIN}" ]]; then
      printf '%s\n' "${SCAGENT_PIXI_BIN}"
      return 0
    fi
    echo "SCAGENT_PIXI_BIN is set but not executable: ${SCAGENT_PIXI_BIN}" >&2
    return 1
  fi

  if command -v pixi >/dev/null 2>&1; then
    command -v pixi
    return 0
  fi

  local candidate
  for candidate in "${HOME}/.pixi/bin/pixi" "/home/xzg/.pixi/bin/pixi"; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  return 1
}

download_sample_h5ad() {
  local sample_path="${SCAGENT_SAMPLE_H5AD}"
  local sample_dir
  local tmp_path

  if [[ -f "${sample_path}" ]]; then
    return 0
  fi
  if [[ "${SCAGENT_SAMPLE_AUTO_DOWNLOAD}" != "1" ]]; then
    return 0
  fi
  if [[ -z "${SCAGENT_SAMPLE_H5AD_URL}" ]]; then
    echo "sample h5ad is missing and SCAGENT_SAMPLE_H5AD_URL is empty: ${sample_path}" >&2
    exit 1
  fi

  sample_dir="$(dirname "${sample_path}")"
  mkdir -p "${sample_dir}"
  tmp_path="${sample_path}.download"
  rm -f "${tmp_path}"

  echo "sample h5ad not found, downloading to ${sample_path}"
  echo "  source: ${SCAGENT_SAMPLE_H5AD_URL}"

  if command -v curl >/dev/null 2>&1; then
    if ! curl -fL --retry 3 --retry-delay 1 "${SCAGENT_SAMPLE_H5AD_URL}" -o "${tmp_path}"; then
      rm -f "${tmp_path}"
      echo "failed to download sample h5ad from ${SCAGENT_SAMPLE_H5AD_URL}" >&2
      exit 1
    fi
  elif command -v wget >/dev/null 2>&1; then
    if ! wget -O "${tmp_path}" "${SCAGENT_SAMPLE_H5AD_URL}"; then
      rm -f "${tmp_path}"
      echo "failed to download sample h5ad from ${SCAGENT_SAMPLE_H5AD_URL}" >&2
      exit 1
    fi
  else
    echo "curl or wget is required to download the sample h5ad." >&2
    exit 1
  fi

  mv "${tmp_path}" "${sample_path}"
}

if [[ "${SCAGENT_PLANNER_MODE}" == "llm" && -z "${SCAGENT_OPENAI_API_KEY}" ]]; then
  echo "SCAGENT_OPENAI_API_KEY is required when SCAGENT_PLANNER_MODE=llm" >&2
  exit 1
fi

runtime_cmd=(python3 runtime/server.py)
if [[ "${SCAGENT_USE_PIXI}" != "0" ]]; then
  if ! pixi_bin="$(resolve_pixi_bin)"; then
    echo "SCAGENT_USE_PIXI=${SCAGENT_USE_PIXI}, but pixi was not found in PATH." >&2
    echo "Add pixi to PATH, set SCAGENT_PIXI_BIN=/path/to/pixi, or set SCAGENT_USE_PIXI=0 to run the runtime with python3." >&2
    exit 1
  fi
  runtime_cmd=("${pixi_bin}" run runtime)
fi

download_sample_h5ad

cleanup() {
  if [[ -n "${RUNTIME_PID:-}" ]]; then
    local runtime_target="${RUNTIME_PID}"
    local watchdog_pid=""
    if [[ "${RUNTIME_OWN_PG:-0}" == "1" ]]; then
      runtime_target="-${RUNTIME_PID}"
    fi

    kill "${runtime_target}" >/dev/null 2>&1 || true

    # Give the runtime 2s to shut down before forcing it down.
    (
      sleep 2
      kill -9 "${runtime_target}" >/dev/null 2>&1 || true
    ) &
    watchdog_pid=$!

    wait "${RUNTIME_PID}" >/dev/null 2>&1 || true
    if [[ -n "${watchdog_pid}" ]]; then
      kill "${watchdog_pid}" >/dev/null 2>&1 || true
      wait "${watchdog_pid}" >/dev/null 2>&1 || true
    fi

    RUNTIME_PID=""
    RUNTIME_OWN_PG=0
  fi
}

handle_interrupt() {
  cleanup
  exit 0
}

trap cleanup EXIT TERM
trap handle_interrupt INT

echo "scAgent startup configuration"
echo "  planner mode:    ${SCAGENT_PLANNER_MODE}"
echo "  runtime command: ${runtime_cmd[*]}"
echo "  runtime url:     ${SCAGENT_RUNTIME_URL}"
echo "  server addr:     ${SCAGENT_ADDR}"

# Run the runtime in its own process group so Ctrl+C only hits the
# foreground Go process. The shell trap will clean the runtime up.
set -m
"${runtime_cmd[@]}" &
RUNTIME_PID=$!
RUNTIME_OWN_PG=1
set +m

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

: "${WEIXIN_BRIDGE_ENABLED:=0}"

WEIXIN_FLAGS=()
if [[ "${WEIXIN_BRIDGE_ENABLED}" == "1" ]]; then
  WEIXIN_FLAGS+=(-weixin)
fi

go run ./cmd/scagent \
  -addr "${SCAGENT_ADDR}" \
  -runtime-url "${SCAGENT_RUNTIME_URL}" \
  -skills-path "${SCAGENT_SKILLS_PATH}" \
  -plugin-dir "${SCAGENT_PLUGIN_DIR}" \
  -plugin-state-path "${SCAGENT_PLUGIN_STATE_PATH}" \
  -docs-dir "${SCAGENT_DOCS_DIR}" \
  -data-dir "${SCAGENT_DATA_DIR}" \
  -web-dir "${SCAGENT_WEB_DIR}" \
  -planner-mode "${SCAGENT_PLANNER_MODE}" \
  -openai-api-key "${SCAGENT_OPENAI_API_KEY}" \
  -openai-base-url "${SCAGENT_OPENAI_BASE_URL}" \
  -openai-model "${SCAGENT_OPENAI_MODEL}" \
  -openai-reasoning "${SCAGENT_OPENAI_REASONING_EFFORT}" \
  "${WEIXIN_FLAGS[@]}"
