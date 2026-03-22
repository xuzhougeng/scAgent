# 自定义 Tool 指南

这份文档说明如何给 `scAgent` 增加一个新的分析 tool，并让 planner 能基于已注册 tool 自动组合步骤。

## 三层结构

一个 tool 要真正可用，需要同时落到三层：

1. `skills/registry.json`
   定义 tool 的名字、输入参数、输出结构、`support_level`。
2. `runtime/server.py`
   实现真实执行逻辑，返回对象、artifact 和 summary。
3. planner
   `LLM planner` 会读取 registry 里的 `wired` tool 自动选择；
   `fake planner` 只会走少量关键词规则，需要手动补启发式。

## `support_level` 怎么用

- `planned`
  schema 先注册，但 planner 不会把它当成可执行 tool。
- `wired`
  表示这个 tool 已经进入真实执行链，planner 可以直接选它。

如果 runtime 还没实现，不要把 tool 标成 `wired`，否则 agent 会规划出一个无法执行的步骤。

## 新增一个 Tool 的步骤

### 1. 在 registry 里注册

在 [skills/registry.json](/home/xzg/project/scAgent/skills/registry.json) 新增一条定义：

- `name`
  tool 的稳定 ID，planner 和 runtime 都靠它路由
- `label`
  UI 展示名
- `category`
  所属工作流阶段
- `support_level`
  未实现前先用 `planned`
- `input`
  参数 schema
- `output`
  结果结构

建议：

- `name` 保持动词开头，比如 `run_umap`、`score_gene_set`
- 参数尽量少而稳，不要把实现细节直接暴露给 planner
- 如果某个参数不是必须的，不要标 `required: true`

### 2. 在 runtime 里实现

在 [runtime/server.py](/home/xzg/project/scAgent/runtime/server.py) 的 `RuntimeState.execute()` 中增加分支。

真实 tool 的建议实现方式：

- 从 `target.materialized_path` 读取 `.h5ad`
- 执行 Scanpy / AnnData 操作
- 如果会生成新对象：
  调用 `_persist_adata_object(...)` 写回新的 `.h5ad`
- 如果会生成图或表：
  在 `session_root / artifacts` 下写 artifact 文件
- 返回：
  - `summary`
  - `object` 可选
  - `artifacts` 可选
  - `metadata` 可选

## 让 agent 自动选择

`LLM planner` 不需要手工列白名单，它会读取 registry 中所有 `wired` tool。

关键代码在 [internal/orchestrator/llm_planner.go](/home/xzg/project/scAgent/internal/orchestrator/llm_planner.go)：

- `p.skills.ListExecutable()`
  决定哪些 tool 会暴露给模型
- `planSchema(...)`
  用 registry 自动生成严格 JSON schema
- `instructions(...)`
  给模型的规划约束和工作流提示

这意味着：

- 只要一个 tool 被注册成 `wired`
- 并且 runtime 真实现了
- planner 就可以把它纳入自动规划

## 如何支持“组合式工作流”

`scAgent` 当前不是把“常规预处理”写死成单一命令，而是鼓励 planner 组合多个 tool。

例如“完成常规的数据预处理”，更理想的 plan 是：

1. `normalize_total`
2. `log1p_transform`
3. `select_hvg`
4. `run_pca`
5. `compute_neighbors`
6. `run_umap`

要让这种组合更稳定，有两种办法：

1. 给 `LLM planner` 增加 workflow hint
   在 [internal/orchestrator/llm_planner.go](/home/xzg/project/scAgent/internal/orchestrator/llm_planner.go) 的 `instructions()` 里补充“遇到常规预处理时如何拆步骤”。
2. 给 `fake planner` 增加兜底规则
   在 [internal/orchestrator/fake_planner.go](/home/xzg/project/scAgent/internal/orchestrator/fake_planner.go) 中为“预处理”等关键词返回多步 plan。

## 如何保留“自主设计”空间

如果 registry 里没有现成 tool，但又不希望 agent 彻底卡死，可以保留一个受控的代码兜底 tool。

当前对应的是 `run_python_analysis`：

- planner 仍然优先选择已有 `wired` tool
- 只有当现成 tool 不足以表达需求时，才退到 `run_python_analysis`
- 代码直接在内存中的 `adata` 上执行，而不是重新发明一套外部脚本流程

当前约定：

- runtime 会把这些变量注入执行环境：
  - `adata`
  - `counts_adata`
  - `sc`
  - `np`
  - `pd`
  - `plt`
  - `session_root`
  - `artifacts_dir`
- 代码里可以设置：
  - `result_summary`
  - `output_adata`
  - `persist_output`
  - `figure`
  - `result_table`

其中：

- `adata` 表示当前对象本身
- `counts_adata` 表示更适合做归一化 / HVG / PCA 这类预处理的 count-safe 副本

这样 agent 就有两层能力：

1. 优先使用稳定、可复用的显式 tool
2. 在必要时用短代码直接处理内存中的数据对象

这比“所有需求都硬编码成固定 tool”更灵活，也比“完全自由执行任意脚本”更可控。

## 自定义 Tool 的最小检查清单

- registry 已增加定义
- `support_level` 与真实实现状态一致
- runtime 已实现执行逻辑
- 返回 summary 清楚说明做了什么
- 新对象会落成 `.h5ad`
- artifact 会写入 `artifacts/`
- planner 提示里必要时补 workflow hint
- 文档同步更新

## 推荐做法

- 先把 tool 以 `planned` 方式注册
- runtime 写完并验证后再升成 `wired`
- 优先做粒度清晰、可组合的 tool
- 让复合任务由 planner 组合，而不是把所有需求都塞进一个超大 tool

这样扩展性最好，后续 agent 才能真正“按已注册能力自主选择和编排”。

## Skill Hub 插件包

除了直接改仓库里的 `skills/registry.json`，现在还支持通过 `Skill Hub` 动态加载插件包。

插件包的最小结构：

```text
my-plugin.zip
├── plugin.json
└── plugin.py
```

其中：

- `plugin.json`
  描述插件 id、说明和要注册的 skill
- `plugin.py`
  真实执行逻辑

最小示例：

```json
{
  "id": "demo-plugin",
  "name": "Demo Plugin",
  "description": "A minimal Skill Hub plugin.",
  "skills": [
    {
      "name": "demo_plot",
      "label": "Demo Plot",
      "category": "visualization",
      "support_level": "wired",
      "description": "Create a demo plugin plot.",
      "target_kinds": ["raw_dataset", "filtered_dataset", "subset", "reclustered_subset"],
      "input": {
        "target_object_id": {
          "type": "string",
          "required": false,
          "description": "Object to plot."
        }
      },
      "output": {
        "artifacts": "plot[]",
        "summary": "string"
      },
      "runtime": {
        "kind": "python",
        "entrypoint": "plugin.py",
        "callable": "run"
      }
    }
  ]
}
```

`plugin.py` 里需要定义入口函数，例如：

```python
def run(context):
    fig = context["plt"].figure(figsize=(4, 3))
    ax = fig.add_subplot(111)
    ax.scatter(context["adata"].obsm["X_umap"][:, 0], context["adata"].obsm["X_umap"][:, 1], s=6)
    artifact = context["save_figure"](
        fig,
        "demo_plugin_plot",
        title="Demo Plugin Plot",
        summary="由 Skill Hub 插件生成。",
    )
    return {
        "summary": "插件绘图完成。",
        "artifacts": [artifact],
    }
```

当前插件入口会拿到这些常用对象：

- `context["adata"]`
- `context["counts_adata"]`
- `context["params"]`
- `context["target"]`
- `context["sc"]`
- `context["np"]`
- `context["plt"]`
- `context["persist_adata"]`
- `context["save_figure"]`
- `context["save_table"]`

上传方式：

- Web 端左侧 `Skill Hub` 卡片上传 zip
- 或者手动把插件目录放到 `data/skill-hub/plugins/`

系统行为：

- Go 侧会自动重新加载 registry
- Python runtime 会在执行时动态扫描插件目录
- 不需要重启就可以在当前分析过程中新增 skill
