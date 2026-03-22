# scAgent Agent 架构图

这份文档说明 `scAgent` 当前的请求流、关键组件职责，以及现在采用的“三阶段回答框架”。

## 系统总览

```text
+-----------------------------------+
| Web 前端                          |
| 聊天区 / 对象列表 / 帮助站 / 插件页 |
+----------------+------------------+
                 |
                 | HTTP / SSE
                 v
+----------------+------------------+
| Go API / Handlers                 |
+----------------+------------------+
                 |
                 v
+----------------+------------------+        +-----------------------------+
| Go Orchestrator                   | <----> | Session Store               |
| session / object / job / snapshot |        | messages / jobs / artifacts |
| decide / investigate / respond    |        | objects / snapshots         |
+-----+------------+----------------+        +-----------------------------+
      |            |                    \
      | decide     | investigate         \ execute
      v            v                      v
+-----+--------+  +---+--------------+  +-----------------------------+
| Answerer     |  | Planner +        |  | Python Runtime              |
| semantic QA  |  | Evaluator        |  | AnnData / Scanpy / plugins  |
+--------------+  +---+--------------+  +-----------------------------+
                         |                              |
                         v                              v
                  +------+-------+             +--------------------------+
                  | Skill Registry |           | data/sessions/           |
                  | wired/planned  |           | objects / artifacts      |
                  +----------------+           +--------------------------+
```

Go API 同时读取 `docs/*.md`，驱动帮助站。

## 单次消息时序

```text
用户
  |
  | 1. 输入请求
  v
Web UI
  |
  | 2. POST /api/messages
  v
Go Orchestrator
  |
  | 3. 保存 user message
  | 4. 先进入 decide 阶段，判断能否直接回答
  | 5. 如果可以，直接写 assistant message 并结束
  | 6. 如果不可以，创建 queued job
  | 7. 立即返回 snapshot，前端开始订阅 SSE
  | 8. 后台 goroutine 将 job 置为 running
  | 9. buildPlanningRequest(
  |      active_object
  |      + recent_messages
  |      + recent_jobs
  |      + recent_artifacts
  |    )
  v
Planner
  |
  | 10. 返回初始 JSON plan
  v
Go Orchestrator
  |
  | 11. 进入 investigate 阶段，记录“初始规划” checkpoint
  | 12. 逐步调用 runtime /execute
  v
Python Runtime
  |
  | 13. 返回 summary + facts + object + artifacts + metadata
  v
Go Orchestrator
  |
  | 14. 持久化 step / facts / object / artifact
  | 15. 调用 evaluator 判断请求是否已完成
  | 16. 若未完成，基于最新对象状态进行 checkpoint 重规划
  | 17. investigate 结束后进入 respond 阶段
  | 18. 基于 facts / evidence 生成最终 assistant message
  | 19. 持续通过 SSE 推送 job_updated / session_updated
  v
Web UI
  |
  | 20. 渲染 assistant 正文 + 结构化任务详情：
  |      phases / checkpoints / plan / steps / artifacts
  v
用户
```

## 当前执行模型

`scAgent` 现在不是“所有问题都直接进 plan/job”，而是显式分成三段：

1. `Decide`
   判断当前上下文是否已经足够直接回答。
2. `Investigate`
   如果不够，生成 plan，逐步执行，并持续收集 `facts / artifacts / objects / metadata`。
3. `Respond`
   不直接复用 step summary，而是基于已收集证据生成最终回答。

这让系统同时具备两种能力：

- 简单问题可以快速回答，而不是被包装成一张重型任务卡
- 复杂问题仍然可以中途重规划，并在收集完证据后再确认回答

## 组件职责

- `Web UI`
  负责聊天输入、assistant 正文、任务详情卡、对象概览、artifact 预览、帮助站和插件页。
- `Go API / Orchestrator`
  负责会话生命周期、对象树、三阶段编排、completion 判定、checkpoint 重规划、SSE 推送。
- `Answerer`
  负责两件事：
  - decide 阶段的语义直答判断
  - respond 阶段基于证据生成最终回答
- `Planner`
  把需要调查的自然语言请求转换成合法 plan。
  在 `llm` 模式下使用真实语义规划；`fake` 模式仅作为显式 demo / test 模式存在，不作为线上静默兜底。
- `Evaluator`
  在每个成功 step 后判断“是否已经满足用户请求”。
  `llm` 模式下不再静默降级为规则判断。
- `Skill Registry`
  定义 skill 是否为 `wired`、输入输出 schema、运行入口和元信息。
- `Python Runtime`
  负责 `.h5ad` 读取、AnnData/Scanpy 分析、插件技能执行，以及对象/artifact/facts 落盘。
- `Session Store`
  保存最近消息、对象、job、artifacts 和 snapshots，并作为 SSE 的数据源。

## 当前前端表达

主聊天区现在分成两层：

- assistant 正文
  用户真正看到的最终回答。
- job 详情卡
  只在有 job 时展示，用来解释执行过程。

任务详情卡会结构化展示：

- phases
- job summary
- 执行检查点
  例如 `初始规划`、`完成判定`、`检查点重规划`
- 当前 plan
- 已执行 step 列表
- 产出的文件和图表

这样既保留了过程可审计性，也避免把最终回答淹没在步骤摘要里。

## 现在最关键的扩展点

### 1. 新增分析 Skill

- 在 `skills/registry.json` 注册 schema
- 在 `runtime/server.py` 增加真实执行分支
- 必要时在 planner / evaluator 提示里补 workflow hint

### 2. 提升 Evidence / Completion 质量

- 当前已经有显式 evaluator 层和 respond 层
- 但前提仍然是 runtime/skill 需要产出足够好的 `facts`
- 后续可以继续补：
  - 更强的生物任务 completion prompt
  - 更明确的 artifact / object / facts success criteria
  - 更多结构化 evidence schema，而不是只靠 summary 文本

### 3. 继续丰富前端任务解释

- 当前已能展示 checkpoint
- 后续还可以把“为什么要做下一步”解释得更明确
- 例如把每次重规划前后的差异高亮出来

## 当前边界

- 只有 `wired` skills 会进入执行链
- `planned` 只是 registry 占位，不会被正常规划执行
- planner preview 只构造上下文，不执行 runtime
- 状态现在会落到 `data/state/store.db`，底层持久化改成 SQLite
