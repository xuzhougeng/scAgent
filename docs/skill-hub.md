# Skill Hub

## 定位

`Skill Hub` 用来在不重启 `scAgent` 的情况下，动态增加、启用或关闭技能包。

当前入口是独立的插件管理页 `plugins.html`。主页面只保留摘要入口，具体上传和管理动作都放在插件页里。

它支持两种来源：

1. 自动注册
   直接把插件目录放到 `data/skill-hub/plugins/`
2. 手动上传
   在 `plugins.html` 上传 zip bundle

上传或放入目录后：

- Go 侧 registry 会自动 reload
- 只有 `wired` skill 会进入 planner 的可执行集合
- Python runtime 会在执行时动态扫描插件目录

## 插件管理页现在能做什么

插件管理页当前支持：

- 上传 zip 插件包
- 刷新当前 Skill Hub 状态
- 启用或关闭某个技能包
- 查看内置技能包和外部插件包的差异
- 点击某个技能查看详细规范

技能详情使用悬浮窗展示：

- `ESC` 可关闭
- 方向键可在不同技能间切换
- 详情里会显示输入参数、输出约定和运行配置

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

## 执行与规划的关系

Skill Hub 里注册的 skill 并不会因为“出现在 registry 中”就自动可执行。

- `planned`
  只表示 schema 已注册，当前不会被正常规划执行。
- `wired`
  表示 planner 和 runtime 都可以真正使用它。

因此一个插件 skill 想被自动调用，至少要满足：

1. `support_level` 为 `wired`
2. `plugin.py` 中存在真实入口
3. bundle 当前是启用状态

## 设计原则

- 优先把稳定能力做成显式 skill
- 让 planner 自动组合多个 skill
- 只在确实需要的时候再退回 `run_python_analysis`
- 把可复用方法沉淀到 Skill Hub，而不是反复生成临时代码
