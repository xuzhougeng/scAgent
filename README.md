# scAgent

Go control plane + Python analysis runtime + static frontend for interactive single-cell workflows.

`scAgent` currently supports workspace-based object sharing, conversation-scoped job/message history, background job execution, planner preview, markdown-driven help docs, and dynamic Skill Hub plugin bundles.

## What Works Today

- Upload a real `.h5ad` file and assess its readiness, annotations, embeddings, and analysis state.
- Reuse one shared workspace across multiple conversations while keeping each conversation's jobs and messages isolated.
- Execute a growing set of `wired` skills, including `assess_dataset`, the core preprocessing chain, `subset_cells`, `recluster`, `find_markers`, plotting, `export_h5ad`, and `run_python_analysis`.
- Run long tasks as background jobs. The web client streams plan updates, execution checkpoints, step results, and artifacts over SSE.
- Preview the planning context before execution through `/api/sessions/{id}/planner-preview`.
- Manage built-in bundles and uploaded plugin bundles from `/plugins.html` without restarting the server.

Only `wired` skills are executable. `planned` skills remain registry placeholders until runtime support is added.

## Layout

- `cmd/scagent`: Go entrypoint and CLI flags.
- `internal/api`: HTTP handlers for sessions, messages, docs, skills, and plugins.
- `internal/app`: server wiring.
- `internal/models`: session, object, job, artifact, checkpoint, and plan structs.
- `internal/orchestrator`: planning, completion evaluation, checkpoint replanning, runtime execution, and event publishing.
- `internal/runtime`: Go client for the Python runtime.
- `internal/session`: file-backed store plus snapshot/event helpers.
- `internal/skill`: built-in registry plus Skill Hub plugin loading.
- `runtime/server.py`: long-lived Python runtime service.
- `skills/registry.json`: shared skill catalog and parameter schema.
- `web`: main SPA, help site, and plugin management UI.
- `docs/agent-architecture.md`: current execution flow and extension points.
- `docs/help-guide.md`: user-facing workflow guide.
- `docs/protocol.md`: control-plane, runtime, and web API contract.
- `docs/skill-hub.md`: plugin bundle format and Skill Hub behavior.

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

You can also use:

```bash
make dev
```

To reset persisted workspace metadata and materialized workspace files under the current `SCAGENT_DATA_DIR` (default `data`), use:

```bash
make restore
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

## Important Environment Variables

- `SCAGENT_PLANNER_MODE=fake|llm`
- `SCAGENT_OPENAI_API_KEY`
- `SCAGENT_OPENAI_BASE_URL`
- `SCAGENT_OPENAI_MODEL`
- `SCAGENT_OPENAI_REASONING_EFFORT`
- `SCAGENT_PIXI_BIN`
- `SCAGENT_USE_PIXI`

## Planner And Execution Modes

The orchestrator can run with either deterministic fake planning or LLM planning.

Fake mode:

```bash
go run ./cmd/scagent -planner-mode=fake
```

LLM mode:

```bash
SCAGENT_OPENAI_API_KEY=... go run ./cmd/scagent \
  -planner-mode=llm \
  -openai-model=gpt-5.4 \
  -openai-reasoning=low
```

In `llm` mode, the same model configuration is used for both:

- plan generation
- completion evaluation after each executed step

After each successful step, the orchestrator can:

1. evaluate whether the user request is already complete
2. stop early if the request is satisfied
3. otherwise rebuild planning context and replan the remaining steps

This keeps long-running workflows asynchronous while still allowing mid-run correction.

## Planner Preview And Execution

Preview the deterministic fake planner:

```bash
curl -X POST http://127.0.0.1:8080/api/fake/plan \
  -H 'Content-Type: application/json' \
  -d '{"message":"把 cortex 细胞拿出来重新聚类，然后画一下 marker"}'
```

Preview the real planning context for a session:

```bash
curl -X POST http://127.0.0.1:8080/api/sessions/sess_000001/planner-preview \
  -H 'Content-Type: application/json' \
  -d '{"message":"根据这个 h5ad 的细胞类型字段做 subset"}'
```

Run a full request:

```bash
curl -X POST http://127.0.0.1:8080/api/messages \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"sess_000001","message":"把 cortex 细胞拿出来重新聚类，然后画一下 marker"}'
```

`POST /api/messages` returns immediately. The job continues in the background, and the web client follows progress through `/api/sessions/{id}/events`.

## Status Model

The runtime can expose different capability combinations:

- `live`: LLM planner enabled and real analysis execution available
- `hybrid_demo` / `demo`: some parts are real, but not every analysis path is production-ready

Check `/api/status` or the main UI status panel to see the current effective mode, loaded skills, runtime health, and environment notes.

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

This repo is no longer just a one-shot planner demo. It now focuses on:

- session and object lineage management
- structured job execution with plan, steps, and checkpoints
- real `.h5ad` inspection and an expanding set of `wired` single-cell skills
- dynamic plugin registration through Skill Hub

Still in progress:

- many registry entries remain `planned`
- persistence is SQLite-backed at `data/state/store.db`; migration and operational hardening are still minimal
- richer evaluator policies and more biological workflows are still being added
