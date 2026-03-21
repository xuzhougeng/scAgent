# Pixi Analysis Environment

`scAgent` keeps the Go control plane separate from the Python analysis stack. Use Pixi to pin the Python side so `scanpy`, `anndata`, `numpy`, and plotting libraries stay ABI-compatible.

## Why Pixi

- Single source of truth for the analysis runtime in `pixi.toml`
- Reproducible Python versions and binary dependencies across machines
- Avoids the mixed-system-environment failures already seen with `numpy` / `pandas` / `scanpy`

## Standard Workflow

Install Pixi itself with the official installer first, then create the environment:

```bash
pixi install
```

Run the environment doctor:

```bash
pixi run doctor
```

This checks:

- pinned package imports
- resolved package versions
- whether the sample `.h5ad` can be opened through `anndata`

Start the Python runtime inside Pixi:

```bash
pixi run runtime
```

Start the Go server separately:

```bash
go run ./cmd/scagent
```

Open `http://127.0.0.1:8080`.

## Environment Conventions

The Pixi environment exports:

- `SCAGENT_RUNTIME_HOST=127.0.0.1`
- `SCAGENT_RUNTIME_PORT=8081`
- `MPLBACKEND=Agg`
- `MPLCONFIGDIR=.pixi/matplotlib`

These defaults keep the runtime predictable and avoid polluting the user home directory with matplotlib cache files.

## Locking Policy

`pixi.toml` is now the committed environment definition. After installing Pixi locally, generate and commit `pixi.lock` so the exact solver result is frozen for the team.
