<p align="center">
  <img src="web/logo.svg" alt="scAgent" width="320" />
</p>

<p align="center">Go control plane + Python analysis runtime + static frontend for interactive single-cell workflows.</p>

`scAgent` supports workspace-based object sharing, conversation-scoped job/message history, a three-phase LLM execution pipeline (decide → investigate → respond), background job execution, markdown-driven help docs, dynamic Skill Hub plugin bundles, and optional WeChat bridge integration.

## What Works Today

- Upload a real `.h5ad` file and assess its readiness, annotations, embeddings, and analysis state.
- Reuse one shared workspace across multiple conversations while keeping each conversation's jobs and messages isolated.
- Create, inspect, and delete workspaces and conversations through the REST API.
- Execute 18 `wired` skills covering the full single-cell analysis pipeline:
  - **Inspection**: `inspect_dataset`, `assess_dataset`
  - **Preprocessing**: `normalize_total`, `log1p_transform`, `select_hvg`
  - **Dimensionality reduction**: `run_pca`, `compute_neighbors`, `run_umap`, `prepare_umap`
  - **Visualization**: `plot_umap`, `plot_gene_umap`
  - **Subsetting & clustering**: `subset_cells`, `subcluster_from_global`, `recluster`, `reanalyze_subset`
  - **Downstream**: `find_markers`, `run_python_analysis`, `export_h5ad`
- Run long tasks as background jobs. The web client streams plan updates, execution checkpoints, step results, and artifacts over SSE.
- Preview the planning context before execution through `/api/sessions/{id}/planner-preview`.
- Manage built-in bundles and uploaded plugin bundles from `/plugins.html` without restarting the server.
- Optionally bridge conversations to WeChat for voice and text message interaction.

Only `wired` skills are executable. `planned` skills remain registry placeholders until runtime support is added.

## Layout

- `cmd/scagent`: Go entrypoint and CLI flags.
- `internal/api`: HTTP handlers for workspaces, sessions, messages, docs, skills, and plugins.
- `internal/app`: server wiring.
- `internal/models`: workspace, session, object, job, artifact, checkpoint, and plan structs.
- `internal/orchestrator`: three-phase execution (decide/investigate/respond), planning, evaluation, and event publishing.
- `internal/runtime`: Go client for the Python runtime.
- `internal/session`: SQLite-backed store plus snapshot/event helpers.
- `internal/skill`: built-in registry plus Skill Hub plugin loading.
- `internal/weixin`: WeChat bridge client and protocol types.
- `runtime/server.py`: long-lived Python runtime service.
- `runtime/doctor.py`: environment health check utility.
- `skills/registry.json`: shared skill catalog and parameter schema.
- `web`: main SPA, help site, and plugin management UI.
- `docs/agent-architecture.md`: current execution flow and extension points.
- `docs/help-guide.md`: user-facing workflow guide.
- `docs/protocol.md`: control-plane, runtime, and web API contract.
- `docs/skill-hub.md`: plugin bundle format and Skill Hub behavior.
- `docs/skill-catalog.md`: skill descriptions and parameter reference.
- `docs/custom-tools.md`: custom tool integration guide.
- `docs/weixin-bridge.md`: WeChat bridge setup and protocol.
- `docs/pixi-environment.md`: Python environment pinning.
- `docs/roadmap.md`: project roadmap.
- `docs/commit-convention.md`: git commit style guide.

## Run

Install Pixi first, then create the pinned Python environment:

```bash
curl -fsSL https://pixi.sh/install.sh | sh
pixi install
pixi run doctor
```

If `pixi` is installed but not in `PATH`, `start.sh` also accepts `SCAGENT_PIXI_BIN=/absolute/path/to/pixi`.

For local development, the easiest path is:

```bash
cp .env.example .env
./start.sh
```

`start.sh` will:

- load `.env` if it exists
- start the Python runtime through `pixi run runtime` by default
- fail fast when Pixi is unavailable unless `SCAGENT_USE_PIXI=0`
- wait for the runtime health check
- start the Go control plane
- optionally start the WeChat bridge when `WEIXIN_BRIDGE_ENABLED=1`

Available Makefile targets:

```bash
make dev             # run via start.sh
make restore         # reset store.db and workspace files
make weixin          # run with WeChat bridge enabled
make weixin-login    # WeChat login flow
make weixin-logout   # WeChat logout flow
```

Or run both processes manually:

```bash
pixi run runtime
go run ./cmd/scagent
```

Open:

- `http://127.0.0.1:8080/` for the main analysis console
- `http://127.0.0.1:8080/help.html` for the markdown-driven help site
- `http://127.0.0.1:8080/plugins.html` for Skill Hub / plugin management

## Environment Variables

All variables can be set in `.env` or passed as CLI flags. See `.env.example` for a template.

**Server & runtime:**

| Variable | Default | Description |
|----------|---------|-------------|
| `SCAGENT_ADDR` | `:8080` | HTTP listen address |
| `SCAGENT_RUNTIME_HOST` | `127.0.0.1` | Python runtime host |
| `SCAGENT_RUNTIME_PORT` | `8081` | Python runtime port |
| `SCAGENT_RUNTIME_URL` | `http://127.0.0.1:8081` | Full runtime URL |
| `SCAGENT_USE_PIXI` | `1` | Use Pixi to manage the Python runtime |
| `SCAGENT_PIXI_BIN` | (auto) | Absolute path to Pixi binary |

**LLM planner:**

| Variable | Default | Description |
|----------|---------|-------------|
| `SCAGENT_PLANNER_MODE` | `llm` | Planner backend (`llm`) |
| `SCAGENT_OPENAI_API_KEY` | — | API key for the LLM provider |
| `SCAGENT_OPENAI_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible base URL |
| `SCAGENT_OPENAI_MODEL` | `gpt-5.4` | Model identifier |
| `SCAGENT_OPENAI_REASONING_EFFORT` | `low` | Reasoning effort level |

**Paths:**

| Variable | Default | Description |
|----------|---------|-------------|
| `SCAGENT_DATA_DIR` | `data` | Workspace and state storage root |
| `SCAGENT_WEB_DIR` | `web` | Static frontend directory |
| `SCAGENT_SKILLS_PATH` | `skills/registry.json` | Skill catalog file |
| `SCAGENT_DOCS_DIR` | `docs` | Markdown help content |
| `SCAGENT_PLUGIN_DIR` | `data/skill-hub/plugins` | Uploaded plugin bundles |
| `SCAGENT_PLUGIN_STATE_PATH` | `data/skill-hub/state.json` | Plugin enable/disable state |

**WeChat bridge:**

| Variable | Default | Description |
|----------|---------|-------------|
| `WEIXIN_BRIDGE_ENABLED` | `0` | Enable WeChat message bridge |
| `WEIXIN_BRIDGE_SESSION_LABEL` | — | Target session label for bridged messages |
| `WEIXIN_BRIDGE_TIMEOUT_MS` | — | Bridge request timeout in milliseconds |

## Three-Phase Execution

Each user request flows through three phases:

1. **Decide** — determine whether the request can be answered directly (simple QA) or requires analysis execution.
2. **Investigate** — generate a plan, execute steps against the Python runtime, evaluate completion after each step, and replan if necessary.
3. **Respond** — synthesize a final assistant message from the accumulated facts and artifacts.

The LLM is used for plan generation, mid-run completion evaluation, and final response synthesis:

```bash
SCAGENT_OPENAI_API_KEY=... go run ./cmd/scagent \
  -planner-mode=llm \
  -openai-model=gpt-5.4 \
  -openai-reasoning=low
```

After each successful step in the investigate phase, the orchestrator can:

1. evaluate whether the user request is already complete
2. stop early if the request is satisfied
3. otherwise rebuild planning context and replan the remaining steps

This keeps long-running workflows asynchronous while still allowing mid-run correction.

## API

### Workspaces and conversations

```bash
# list workspaces
curl http://127.0.0.1:8080/api/workspaces

# create a workspace
curl -X POST http://127.0.0.1:8080/api/workspaces

# get workspace snapshot
curl http://127.0.0.1:8080/api/workspaces/{id}

# delete a workspace
curl -X DELETE http://127.0.0.1:8080/api/workspaces/{id}

# create a conversation within a workspace
curl -X POST http://127.0.0.1:8080/api/workspaces/{id}/conversations
```

### Sessions and messages

```bash
# create a standalone session
curl -X POST http://127.0.0.1:8080/api/sessions

# get session snapshot
curl http://127.0.0.1:8080/api/sessions/{id}

# delete a session
curl -X DELETE http://127.0.0.1:8080/api/sessions/{id}

# upload an h5ad file
curl -X POST http://127.0.0.1:8080/api/sessions/{id}/upload \
  -F 'file=@dataset.h5ad'

# submit a message (returns immediately; job runs in background)
curl -X POST http://127.0.0.1:8080/api/messages \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"sess_000001","message":"把 cortex 细胞拿出来重新聚类，然后画一下 marker"}'

# stream events (SSE)
curl http://127.0.0.1:8080/api/sessions/{id}/events
```

### Planner preview

```bash
# preview real planning context for a session
curl -X POST http://127.0.0.1:8080/api/sessions/sess_000001/planner-preview \
  -H 'Content-Type: application/json' \
  -d '{"message":"根据这个 h5ad 的细胞类型字段做 subset"}'

# debug-only deterministic fake plan
curl -X POST http://127.0.0.1:8080/api/fake/plan \
  -H 'Content-Type: application/json' \
  -d '{"message":"把 cortex 细胞拿出来重新聚类，然后画一下 marker"}'
```

### Other endpoints

```bash
# health check
curl http://127.0.0.1:8080/healthz

# system status, loaded skills, runtime health
curl http://127.0.0.1:8080/api/status

# skill catalog
curl http://127.0.0.1:8080/api/skills

# docs index and content
curl http://127.0.0.1:8080/api/docs
curl http://127.0.0.1:8080/api/docs/{slug}

# plugin management
curl http://127.0.0.1:8080/api/plugins
curl -X POST http://127.0.0.1:8080/api/plugins -F 'bundle=@plugin.zip'
curl -X PATCH http://127.0.0.1:8080/api/plugins/{bundleID} \
  -H 'Content-Type: application/json' -d '{"enabled":true}'
```

## Git Setup

This repository uses a local commit convention:

- subject format: `type(scope): short summary`
- template file: `.gitmessage.txt`
- guide: `docs/commit-convention.md`

Examples:

- `feat(api): add docs index endpoint`
- `fix(runtime): improve h5ad metadata assessment`

The Go control plane uses your local Go toolchain. The Python analysis stack is pinned through [docs/pixi-environment.md](docs/pixi-environment.md).

## Current Scope

- Workspace and conversation lifecycle management with full CRUD
- Structured job execution with three-phase pipeline, plans, steps, and checkpoints
- Real `.h5ad` inspection and 18 `wired` single-cell skills covering the standard analysis pipeline
- Dynamic plugin registration through Skill Hub
- Optional WeChat bridge for voice and text message interaction
- Persistence is SQLite-backed at `data/state/store.db`

Still in progress:

- Many registry entries remain `planned`
- Migration and operational hardening are still minimal
- Richer evaluator policies and more biological workflows are still being added
