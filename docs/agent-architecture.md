# scAgent Agent 架构图

这份文档用来说明 `scAgent` 当前的 agent 架构、请求流转路径，以及哪里适合继续扩展。

## 系统总览

```text
+-------------------------------+
| Web 前端                      |
| 聊天区 / 状态栏 / 帮助站      |
+---------------+---------------+
                |
                | HTTP / SSE
                v
+---------------+---------------+
| Go API / Handlers             |
+---------------+---------------+
                |
                v
+---------------+---------------+        +-----------------------------+
| Go Orchestrator               | <----> | Session Store               |
| session / object / job        |        | messages / jobs / artifacts |
| artifact / snapshot           |        | snapshots                   |
+-------+-----------------+-----+        +-----------------------------+
        |                 |
        | plan            | execute
        v                 v
+-------+--------+   +----+----------------------+
| Planner         |   | Python Runtime           |
| LLM / Fake      |   | AnnData / Scanpy         |
+-------+--------+   +----+----------------------+
        |                 |
        v                 v
+-------+--------+   +----+----------------------+
| Skill Registry  |   | data/sessions/           |
| registry.json   |   | objects / artifacts      |
+-----------------+   +--------------------------+

Go API 同时读取 `docs/*.md`，提供帮助站内容。
```

## 单次消息时序

```text
用户
  |
  | 1. 输入“把这个图改一下”
  v
Web UI
  |
  | 2. POST /api/messages
  v
Go Orchestrator
  |
  | 3. 保存当前 user message，创建 job
  | 4. 读取 session snapshot
  | 5. buildPlanningRequest(
  |      active_object
  |      + recent_messages
  |      + recent_jobs
  |      + recent_artifacts
  |    )
  v
Planner
  |
  | 6. 返回 JSON steps
  v
Go Orchestrator
  |
  | 7. 逐步调用 runtime /execute
  v
Python Runtime
  |
  | 8. 返回 summary + object + artifacts
  v
Go Orchestrator
  |
  | 9. 更新 store，写 assistant message
  | 10. 通过 SSE 推送 snapshot / job / message
  v
Web UI
  |
  | 11. 更新聊天区和图表预览
  v
用户
```

## 组件职责

- `Web UI`
  负责聊天输入、作业状态、artifact 预览、帮助文档和系统状态展示。
- `Go API / Orchestrator`
  负责会话生命周期、对象树、作业编排、planner 调用、runtime 调用、SSE 推送。
- `Planner`
  负责把自然语言转成合法 plan。
  `LLM Planner` 面向真实语义理解，`Fake Planner` 面向规则兜底。
- `Skill Registry`
  负责声明哪些 tool 可见、可执行，以及每个 tool 的输入输出 schema。
- `Python Runtime`
  负责真实的 `.h5ad` 读取、AnnData/Scanpy 分析和图表/文件产出。
- `Session Store`
  负责保存最近消息、最近 job、artifact 和对象状态，给 follow-up 请求提供上下文。

## 现在最关键的扩展点

### 1. 新增分析 Tool

- 在 `skills/registry.json` 注册 schema
- 在 `runtime/server.py` 增加真实执行分支
- 必要时在 planner 指令里增加 workflow hint

### 2. 增强 follow-up 理解

- 当前已经把 `recent_messages / recent_jobs / recent_artifacts` 带进 planning context
- 这能解决“上一轮已经画了图，这一轮是在改图”这类续谈问题
- 如果后面要更强的多轮理解，可以继续补：
  - 更长的消息摘要
  - 最近一次 plot 的结构化配置
  - 当前 plot spec 的显式对象模型

### 3. 增强绘图可控性

- 当前更像是“重新调用最接近的 plot tool”
- 如果要精确改图，建议继续给 `plot_umap` 增加：
  - `legend_position`
  - `point_size`
  - `palette`
  - `title`
  - `figure_width` / `figure_height`

## 当前边界

- planner 已经能根据 registry 自动暴露 `wired` tool
- agent 已经能组合多步 workflow
- follow-up 请求已经能利用最近上下文
- 但“真正的绘图编辑”还没有独立 plot spec 层，当前主要是重跑最接近的绘图 tool
