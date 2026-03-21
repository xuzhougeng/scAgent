# Repository Guidelines

## Project Structure & Module Organization

`scAgent` is split across three layers:

- `cmd/scagent/`: Go entrypoint and CLI flags.
- `internal/`: Go control plane code.
  - `api/`: HTTP handlers and API tests.
  - `app/`: server wiring.
  - `models/`, `session/`, `orchestrator/`, `runtime/`, `skill/`: core domain, state, planner, runtime client, and skill registry.
- `runtime/`: Python runtime service (`server.py`) and environment diagnostics (`doctor.py`).
- `skills/registry.json`: shared skill catalog.
- `web/`: static frontend (`index.html`, `app.js`, `styles.css`, help pages).
- `docs/`: Markdown docs rendered by the in-app help site.

## Build, Test, and Development Commands

- `./start.sh`: starts the Python runtime, waits for health, then starts the Go server.
- `make dev`: thin wrapper around `./start.sh`.
- `pixi install && pixi run doctor`: installs and verifies the pinned Python analysis environment.
- `pixi run runtime`: runs the Python runtime inside Pixi.
- `go run ./cmd/scagent`: runs only the Go control plane.
- `GOCACHE=/tmp/go-build go test ./...`: runs all Go tests.
- `python3 -m py_compile runtime/server.py runtime/doctor.py`: checks Python syntax.
- `node --check web/app.js && node --check web/help.js`: checks frontend script syntax.

## Coding Style & Naming Conventions

- Go: rely on `gofmt`; exported names use `CamelCase`, package-local helpers use `camelCase`.
- Python: 4-space indentation, `snake_case` functions, keep runtime responses JSON-friendly.
- JavaScript/CSS/HTML: follow existing simple static-file style; use 2-space indentation in `web/`.
- Keep new skill names lower_snake_case, e.g. `plot_gene_umap`, `subcluster_group`.

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
