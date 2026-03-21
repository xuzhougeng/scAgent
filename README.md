# scAgent

Go control plane + Python analysis runtime + vanilla WebView-friendly frontend for interactive single-cell workflows.

## Layout

- `cmd/scagent`: Go entrypoint.
- `internal/models`: session, object, job, artifact, and plan structs.
- `internal/orchestrator`: session bootstrap, planning, validation, runtime execution, and event publishing.
- `internal/runtime`: Go client for the Python runtime.
- `internal/skill`: skill registry loader and plan validation.
- `runtime/server.py`: long-lived Python runtime service.
- `skills/registry.json`: shared skill catalog and parameter schema.
- `web`: static SPA for object explorer, chat, and result inspector.
- `docs/protocol.md`: control-plane / compute-plane contract.
- `docs/skill-catalog.md`: organized single-cell skill catalog with `wired` vs `planned` status.

## Run

Install Pixi itself first with the official installer, then create the pinned analysis environment:

```bash
pixi install
pixi run doctor
```

Start the Python runtime inside Pixi:

```bash
pixi run runtime
```

Start the Go server:

```bash
go run ./cmd/scagent
```

Open `http://127.0.0.1:8080`.

Open `http://127.0.0.1:8080/help.html` for the markdown-driven help site rendered from `docs/*.md`.

## Simple Startup

For local testing, the easiest path is:

```bash
cp .env.example .env
./start.sh
```

`start.sh` will:

- load `.env` if it exists
- start the Python runtime through `pixi run runtime` when Pixi is available
- wait for the runtime health check
- start the Go control plane with the configured planner mode

You can also use:

```bash
make dev
```

Important environment variables:

- `SCAGENT_PLANNER_MODE=fake|llm`
- `SCAGENT_OPENAI_API_KEY`
- `SCAGENT_OPENAI_BASE_URL`
- `SCAGENT_OPENAI_MODEL`
- `SCAGENT_OPENAI_REASONING_EFFORT`

## Git Setup

This repository uses a local commit convention:

- subject format: `type(scope): short summary`
- template file: `.gitmessage.txt`
- guide: `docs/commit-convention.md`

Examples:

- `feat(api): add docs index endpoint`
- `fix(runtime): improve h5ad metadata assessment`

The Go control plane still uses your local Go toolchain. The Python analysis stack is fixed by [docs/pixi-environment.md](docs/pixi-environment.md).

## Planner Modes

The orchestrator can run with either a deterministic fake planner or a real LLM planner.

Fake mode is the default:

```bash
go run ./cmd/scagent -planner-mode=fake
```

LLM mode uses the OpenAI Responses API:

```bash
SCAGENT_OPENAI_API_KEY=... go run ./cmd/scagent \
  -planner-mode=llm \
  -openai-model=gpt-5.4 \
  -openai-reasoning=low
```

Environment variables are also supported:

- `SCAGENT_PLANNER_MODE`
- `SCAGENT_OPENAI_BASE_URL`
- `SCAGENT_OPENAI_MODEL`
- `SCAGENT_OPENAI_REASONING_EFFORT`
- `SCAGENT_OPENAI_API_KEY`

## Planner Preview

Preview the fake planner output without executing any runtime step:

```bash
curl -X POST http://127.0.0.1:8080/api/fake/plan \
  -H 'Content-Type: application/json' \
  -d '{"message":"把 cortex 细胞拿出来重新聚类，然后画一下 marker"}'
```

Preview the planner context for a concrete session and message:

```bash
curl -X POST http://127.0.0.1:8080/api/sessions/sess_000001/planner-preview \
  -H 'Content-Type: application/json' \
  -d '{"message":"根据这个 h5ad 的细胞类型字段做 subset"}'
```

This returns the active object summary, extracted `.h5ad` metadata, and, in `llm` mode, the developer instructions plus request body that would be sent to the planner API.

Run the full execution path:

```bash
curl -X POST http://127.0.0.1:8080/api/messages \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"sess_000001","message":"把 cortex 细胞拿出来重新聚类，然后画一下 marker"}'
```

## Current Scope

This repo is an MVP skeleton. The Python runtime currently returns mock single-cell objects and placeholder artifacts so the control-plane contract, object lifecycle, and UI behavior are testable before wiring in `scanpy` and real `.h5ad` handling.
