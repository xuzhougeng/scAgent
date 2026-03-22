# Skill Hub

## 定位

`Skill Hub` 用来在不重启 `scAgent` 的情况下，动态增加新的 skill。

它支持两种来源：

1. 自动注册
   直接把插件目录放到 `data/skill-hub/plugins/`
2. 手动上传
   在 Web 左侧 `Skill Hub` 卡片上传 zip bundle

上传或放入目录后：

- Go 侧 registry 会自动 reload
- LLM planner 会把新的 `wired` skill 纳入可选集合
- Python runtime 会在执行时动态扫描插件目录

## 插件包格式

最小插件包：

```text
my-plugin.zip
├── plugin.json
└── plugin.py
```

`plugin.json` 最小示例：

```json
{
  "id": "demo-plugin",
  "name": "Demo Plugin",
  "description": "Example plugin bundle",
  "skills": [
    {
      "name": "demo_plugin_plot",
      "label": "Demo Plugin Plot",
      "category": "visualization",
      "support_level": "wired",
      "description": "Create a plot from a plugin.",
      "target_kinds": ["raw_dataset", "filtered_dataset", "subset", "reclustered_subset"],
      "input": {},
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

`plugin.py` 需要定义对应入口：

```python
def run(context):
    fig = context["plt"].figure(figsize=(4, 3))
    ax = fig.add_subplot(111)
    coords = context["adata"].obsm["X_umap"]
    ax.scatter(coords[:, 0], coords[:, 1], s=5)
    artifact = context["save_figure"](
        fig,
        "demo_plugin_plot",
        title="Demo Plugin Plot",
        summary="由插件生成的 UMAP 图。",
    )
    return {
        "summary": "插件运行完成。",
        "artifacts": [artifact],
    }
```

## 运行时上下文

当前插件入口会拿到一个 `context` 字典，常用内容包括：

- `adata`
- `counts_adata`
- `params`
- `target`
- `sc`
- `np`
- `plt`
- `session_root`
- `artifacts_dir`
- `persist_adata(label, adata, kind=...)`
- `save_figure(fig, stem, title=..., summary=...)`
- `save_table(df, stem, title=..., summary=...)`

## 设计原则

- 优先把稳定能力做成显式 skill
- 让 planner 自动组合多个 skill
- 只在确实需要的时候再退回 `run_python_analysis`
- 把可复用方法沉淀到 Skill Hub，而不是反复生成临时代码
