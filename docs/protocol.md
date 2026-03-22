# Protocol

## Core Entities

`Workspace`

- Owns one shared dataset/workspace root.
- Tracks `dataset_id`, `active_object_id`, timestamps, and shared object/artifact lineage.

`Session`

- Represents one conversation thread inside a workspace.
- Tracks `workspace_id`, its own conversation-local history, `active_object_id`, timestamps, and lifecycle status.

`ObjectMeta`

- Lives in Go as metadata only.
- References the actual Python runtime object through `backend_ref`.
- Carries lineage (`parent_id`), residency (`resident`, `materialized`, `evicted`, `deleted`), and materialization info.

`Job`

- Represents one user request submitted through `POST /api/messages`.
- Is created immediately, then continues running asynchronously in the background.
- Contains:
  - `plan`
  - executed `steps`
  - `checkpoints`
  - `summary`
  - `error`

`JobCheckpoint`

- Records orchestration milestones that are not well represented by a single step summary.
- Common examples:
  - `初始规划`
  - `完成判定`
  - `检查点重规划`
  - `执行失败`

Example:

```json
{
  "kind": "completion",
  "tone": "warn",
  "title": "完成判定",
  "label": "继续执行",
  "summary": "当前请求尚未完成，需要继续执行或重规划。",
  "created_at": "2026-03-22T12:34:56Z"
}
```

`Artifact`

- Stored on disk and cataloged by Go.
- Exposed to the web client through `/data/...` URLs.

## Persistence

Control-plane state is persisted in SQLite at `data/state/store.db`.

Current persistence scope:

- `Workspace`
- `Session`
- `ObjectMeta`
- `Job`
- `Artifact`
- `Message`
- ID counter state

Current boundary:

- This persists metadata and history, not Python runtime memory.
- `backend_ref` still points to an in-memory runtime object.
- If the Python runtime restarts, automatic object rehydration / reload is not complete yet.

## Go -> Python

The runtime request field is still named `session_root` for compatibility, but it now points to the workspace root directory under `data/workspaces/{workspace_id}`.

### `POST /sessions/init`

```json
{
  "session_id": "sess_000001",
  "dataset_id": "ds_000002",
  "label": "PBMC3K test session",
  "session_root": "/abs/path/data/workspaces/ws_000001"
}
```

Response:

```json
{
  "object": {
    "backend_ref": "py:sess_000001:adata_1",
    "kind": "raw_dataset",
    "label": "pbmc3k_demo",
    "n_obs": 2700,
    "n_vars": 32738,
    "state": "resident",
    "in_memory": true,
    "materialized_path": "/abs/path/data/workspaces/ws_000001/objects/pbmc3k_demo.h5ad"
  },
  "summary": "Session bootstrapped. Demo raw dataset is resident in the Python runtime."
}
```

### `POST /execute`

```json
{
  "session_id": "sess_000001",
  "request_id": "job_000005:step_2",
  "skill": "recluster",
  "target_backend_ref": "py:sess_000001:adata_2",
  "params": {
    "resolution": 0.6
  },
  "session_root": "/abs/path/data/workspaces/ws_000001"
}
```

Response:

```json
{
  "summary": "Reclustered subset_cortex at resolution 0.6.",
  "object": {
    "backend_ref": "py:sess_000001:adata_3",
    "kind": "reclustered_subset",
    "label": "reclustered_subset_cortex",
    "n_obs": 2134,
    "n_vars": 28671,
    "state": "resident",
    "in_memory": true,
    "materialized_path": "/abs/path/data/workspaces/ws_000001/objects/reclustered_subset_cortex.h5ad"
  },
  "artifacts": [],
  "metadata": {}
}
```

## Web API

### `GET /api/status`

Returns the current effective system status shown in the UI.

Example live response:

```json
{
  "system_mode": "live",
  "summary": "当前处于正式模式：LLM 规划器已启用，分析执行为真实运行。",
  "planner_mode": "llm",
  "planner_ready": true,
  "llm_loaded": true,
  "runtime_connected": true,
  "runtime_mode": "live",
  "real_h5ad_inspection": true,
  "real_analysis_execution": true,
  "executable_skills": ["assess_dataset", "normalize_total", "run_umap"]
}
```

### `GET /api/docs`

Returns the markdown help document index rendered by the in-app help site.

### `GET /api/docs/{slug}`

Returns one markdown document payload:

```json
{
  "slug": "help-guide",
  "title": "scAgent 中文使用指南",
  "path": "help-guide.md",
  "content": "# ..."
}
```

### `POST /api/fake/plan`

Preview the deterministic fake planner without touching the runtime:

```json
{
  "message": "把 cortex 细胞拿出来重新聚类，然后画一下 marker"
}
```

Response:

```json
{
  "planner_mode": "fake",
  "plan": {
    "steps": [
      {
        "id": "step_1",
        "skill": "subset_cells",
        "target_object_id": "$active",
        "params": {
          "obs_field": "cell_type",
          "op": "eq",
          "value": "cortex"
        }
      }
    ]
  }
}
```

### `POST /api/sessions/{id}/planner-preview`

Builds the planner debug context for one session and one message.

Request:

```json
{
  "message": "根据这个 h5ad 的细胞类型字段做 subset"
}
```

Response shape:

```json
{
  "planner_mode": "fake",
  "planning_request": {
    "message": "根据这个 h5ad 的细胞类型字段做 subset",
    "session": {},
    "active_object": {},
    "objects": []
  },
  "developer_instructions": "",
  "request_body": {},
  "note": "..."
}
```

In `fake` mode this is mainly object context plus a note.
In `llm` mode it also includes the prompt/request preview built from the current `.h5ad` metadata.

### `GET /api/skills`

Returns the current registry snapshot and planner mode:

```json
{
  "planner_mode": "fake",
  "skills": []
}
```

### `GET /api/plugins`

Returns Skill Hub bundles plus the current skill list:

```json
{
  "plugins": [],
  "bundles": [],
  "skills": []
}
```

### `POST /api/plugins`

Accepts a multipart zip upload and installs a plugin bundle.

### `PATCH /api/plugins/{bundleID}`

Enables or disables one bundle:

```json
{
  "enabled": false
}
```

### `POST /api/sessions`

Compatibility alias for creating a new workspace plus its first conversation. Returns a `SessionSnapshot`.

### `POST /api/workspaces`

Creates a new workspace and its first conversation. Returns a `SessionSnapshot`.

### `GET /api/workspaces`

Lists known workspaces:

- `workspaces`

Each item is a lightweight `Workspace` summary used by the left sidebar workspace switcher.

### `GET /api/workspaces/{id}`

Returns the workspace snapshot:

- `workspace`
- `conversations`
- `objects`
- `artifacts`

### `POST /api/workspaces/{id}/conversations`

Creates another conversation under the same workspace and returns that conversation's `SessionSnapshot`.

### `GET /api/sessions/{id}`

Returns the session snapshot:

- `session`
- `workspace`
- `objects`
- `jobs`
- `artifacts`
- `messages`

Scoping rules:

- `objects` and `artifacts` are shared at workspace scope
- `jobs` and `messages` are only for the current conversation
- `workspace` carries the shared active object and dataset lineage context

### `POST /api/messages`

Accepts:

```json
{
  "session_id": "sess_000001",
  "message": "把 cortex 细胞拿出来重新聚类，然后画一下 marker"
}
```

Behavior:

1. Saves the user message
2. Creates a queued job
3. Returns immediately with the current `job` and `snapshot`
4. Continues execution in the background

During execution, the job may update multiple times:

- `summary` can change
- `plan` can change because checkpoint replanning may replace the remaining steps
- `steps` grows as execution proceeds
- `checkpoints` grows as planning, completion evaluation, or fallback decisions are recorded

### `GET /api/sessions/{id}/events`

SSE stream with:

- `job_updated`
  emits the current job payload, including `plan`, `steps`, and `checkpoints`
- `session_updated`
  emits the full session snapshot, including conversation-local messages plus workspace-visible objects and artifacts

There is no separate `message_added` event today; new assistant messages are observed through `session_updated`.
