# Repository Guidelines

## Project Structure & Module Organization

`scAgent` is split across three layers:

- `cmd/scagent/`: Go entrypoint and CLI flags.
- `internal/`: Go control plane code.
  - `api/`: HTTP handlers and API tests.
  - `app/`: server wiring.
  - `models/`, `session/`, `orchestrator/`, `runtime/`, `skill/`: core domain, state, planner, runtime client, and skill registry.
- `runtime/`: Python runtime service and diagnostics.
  - `server.py`: thin runtime entrypoint that wires state, HTTP handler, and startup.
  - `session_worker.py`: per-session worker process for isolated skill execution.
  - `runtime_core/`: shared runtime infrastructure such as object store, HTTP layer, h5ad inspection, and analysis helpers.
  - `skill_runtime/`: builtin skill registry, plugin loading, and skill execution/support modules.
  - `doctor.py`: environment diagnostics entrypoint.
- `skills/registry.json`: shared skill catalog.
- `web/`: static frontend — `index.html`, `app.js`, modular CSS (`css/chat.css`, `css/layout.css`, `css/modal.css`, `css/sidebar.css`), JS modules (`js/api.mjs`, `js/format.mjs`, `js/layout.mjs`, `js/modals.mjs`, `js/render.mjs`, `js/state.mjs`), help pages, and plugin management UI.
- `docs/`: Markdown docs rendered by the in-app help site.

## Build, Test, and Development Commands

- `./start.sh`: starts the Python runtime, waits for health, then starts the Go server.
- `make dev`: thin wrapper around `./start.sh`.
- `pixi install && pixi run doctor`: installs and verifies the pinned Python analysis environment.
- `pixi run runtime`: runs the Python runtime inside Pixi.
- `go run ./cmd/scagent`: runs only the Go control plane.
- `GOCACHE=/tmp/go-build go test ./...`: runs all Go tests.
- `python3 -m py_compile runtime/server.py runtime/session_worker.py runtime/doctor.py runtime/runtime_core/*.py runtime/skill_runtime/*.py`: checks Python syntax.
- `node --check web/app.js && node --check web/help.js`: checks frontend script syntax.

## Coding Style & Naming Conventions

- Go: rely on `gofmt`; exported names use `CamelCase`, package-local helpers use `camelCase`.
- Python: 4-space indentation, `snake_case` functions, keep runtime responses JSON-friendly.
- Keep `runtime/server.py` thin; add runtime infrastructure under `runtime/runtime_core/` and skill implementations under `runtime/skill_runtime/` instead of growing the entrypoint.
- JavaScript/CSS/HTML: follow existing simple static-file style; use 2-space indentation in `web/`.
- Keep new skill names lower_snake_case, e.g. `plot_gene_umap`, `subcluster_group`.

## AI Responsibilities & Context Boundaries

- Trust the LLM to interpret user intent, especially for follow-up requests that depend on prior turns, prior artifacts, or previously chosen parameters.
- Do not push natural-language understanding down into orchestrator/runtime/UI with ad hoc keyword rules when the issue is actually missing context.
- Outside explicit mock/fake LLM implementations and test doubles, do not use keyword matching, keyword-triggered branching, or keyword-based canned replies to infer user intent, choose strategies, or compose answers.
- Production orchestrator, resolver, answerer, evaluator, runtime, and UI paths must remain language-agnostic at the deterministic layer; semantic interpretation must come from LLM output plus preserved structured context, not from hard-coded tokens in any single human language.
- The orchestrator should preserve and pass forward rich structured state such as prior step params, metadata, artifact references, active object state, and recent decisions.
- Deterministic code should enforce schemas, safety checks, state persistence, and conservative defaults; it should not silently reinterpret ambiguous user language.
- When the LLM is unavailable or returns unusable output, deterministic fallbacks may validate, defer, ask for clarification, or take the most conservative safe action, but they must not emulate understanding by scanning for task keywords.
- If a follow-up request fails because prior intent was forgotten, fix the context representation or prompt contract first; do not patch over the gap with task-specific heuristics unless there is a hard safety or correctness requirement.
- Prefer general context-management solutions over plot-specific or feature-specific patches. The same design should work for analysis steps, exports, tables, plots, and UI follow-ups.

## Testing Guidelines

- Go tests live next to implementation as `*_test.go`, currently under `internal/api/` and `internal/orchestrator/`.
- Prefer end-to-end handler tests for API changes and unit tests for planner/runtime helpers.
- When changing runtime metadata or UI state, verify both backend tests and syntax checks before shipping.

## Commit & Pull Request Guidelines

- This repository uses `type(scope): short summary`, e.g. `feat(runtime): assess uploaded h5ad readiness`.
- Preferred commit types: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`.
- Use the local template in `.gitmessage.txt` and see `docs/commit-convention.md` for examples.
- Keep PRs scoped to one feature area.
- Include: purpose, key files changed, test commands run, and screenshots for UI changes.

## Security & Configuration Tips

- Use `.env` for local settings; start from `.env.example`.
- Prefer `SCAGENT_OPENAI_API_KEY` over global OpenAI variables to avoid conflicts with other projects.
- Do not commit secrets, generated data under `data/`, or `.pixi/`.
