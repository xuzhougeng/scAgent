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

If `pixi` is not in `PATH`, set `SCAGENT_PIXI_BIN=/absolute/path/to/pixi` before running `./start.sh`.

Run the environment doctor:

```bash
pixi run doctor
```

If `.env` exists in the project root, Pixi loads it before starting the doctor or runtime. This lets you run multiple local instances by changing `SCAGENT_RUNTIME_PORT` and related settings in `.env`.

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

- `MPLBACKEND=Agg`
- `MPLCONFIGDIR=/tmp/scagent-mpl`
- `NUMBA_CACHE_DIR=/tmp/scagent-numba`

Runtime host and port come from `.env` or the surrounding shell environment when set; otherwise the runtime falls back to `127.0.0.1:8081`.

These defaults keep the runtime predictable and avoid polluting the user home directory with matplotlib cache files.

## Locking Policy

`pixi.toml` is now the committed environment definition. After installing Pixi locally, generate and commit `pixi.lock` so the exact solver result is frozen for the team.
