# scAgent Agent 架构图

这份文档说明 `scAgent` 当前的请求流、关键组件职责，以及最近新增的 completion / evaluator / checkpoint 重规划链路。

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
| plan / checkpoints / publish      |        | objects / snapshots         |
+-----+---------------+-------------+        +-----------------------------+
      |               |                \
      | plan          | evaluate        \ execute
      v               v                  v
+-----+--------+  +---+--------------+  +-----------------------------+
| Planner      |  | Evaluator        |  | Python Runtime              |
| LLM / Fake   |  | LLM / Fake       |  | AnnData / Scanpy / plugins  |
+-----+--------+  +---+--------------+  +-----------------------------+
      |               |                               |
      v               |                               v
+-----+--------+      |                      +--------------------------+
| Skill Registry |    |                      | data/sessions/           |
| wired/planned  |    |                      | objects / artifacts      |
+----------------+    |                      +--------------------------+
                      |
                      +-> 生成“已满足请求 / 继续执行 / 沿用原计划”等 checkpoint
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
  | 3. 保存 user message，创建 queued job
  | 4. 立即返回 snapshot，前端开始订阅 SSE
  | 5. 后台 goroutine 将 job 置为 running
  | 6. buildPlanningRequest(
  |      active_object
  |      + recent_messages
  |      + recent_jobs
  |      + recent_artifacts
  |    )
  v
Planner
  |
  | 7. 返回初始 JSON plan
  v
Go Orchestrator
  |
  | 8. 记录“初始规划” checkpoint
  | 9. 逐步调用 runtime /execute
  v
Python Runtime
  |
  | 10. 返回 summary + object + artifacts + metadata
  v
Go Orchestrator
  |
  | 11. 持久化 step / object / artifact
  | 12. 调用 evaluator 判断请求是否已完成
  | 13. 若未完成，基于最新对象状态进行 checkpoint 重规划
  | 14. 记录“完成判定 / 检查点重规划” checkpoint
  | 15. 持续通过 SSE 推送 job_updated / session_updated
  | 16. 全部结束后写 assistant message
  v
Web UI
  |
  | 17. 渲染结构化任务卡：
  |      summary / checkpoints / plan / steps / artifacts
  v
用户
```

## 当前执行模型

`scAgent` 现在不是“一次规划后盲跑到底”，而是：

1. 先生成初始 plan
2. 执行一个 step
3. 用 evaluator 判断请求是否已经完成
4. 如果未完成，再用当前对象状态和当前 job 上下文重规划剩余步骤
5. 持续记录 checkpoint，并通过 SSE 推给前端

这让长任务具备两个关键能力：

- 模型不需要阻塞等待 10 分钟以上的单次 response
- 编排器可以在中途根据真实执行结果修正后续步骤

## 组件职责

- `Web UI`
  负责聊天输入、任务卡、对象概览、artifact 预览、帮助站和插件页。
- `Go API / Orchestrator`
  负责会话生命周期、对象树、作业编排、completion 判定、checkpoint 重规划、SSE 推送。
- `Planner`
  把自然语言请求转换成合法 plan。
  `LLM Planner` 面向真实语义理解，`Fake Planner` 负责规则兜底。
- `Evaluator`
  在每个成功 step 后判断“是否已经满足用户请求”。
  当前也有 `LLM / Fake` 两种实现。
- `Skill Registry`
  定义 skill 是否为 `wired`、输入输出 schema、运行入口和元信息。
- `Python Runtime`
  负责 `.h5ad` 读取、AnnData/Scanpy 分析、插件技能执行，以及对象/artifact 落盘。
- `Session Store`
  保存最近消息、对象、job、artifacts 和 snapshots，并作为 SSE 的数据源。

## 当前前端表达

主聊天区里的 job 卡现在会结构化展示：

- job summary
- 执行检查点
  例如 `初始规划`、`完成判定`、`检查点重规划`
- 当前 plan
- 已执行 step 列表
- 产出的文件和图表

这比把所有状态都塞进一段 assistant 文本里更稳定，也更适合长任务。

## 现在最关键的扩展点

### 1. 新增分析 Skill

- 在 `skills/registry.json` 注册 schema
- 在 `runtime/server.py` 增加真实执行分支
- 必要时在 planner / evaluator 提示里补 workflow hint

### 2. 提升 Completion / Evaluator 质量

- 当前已经有显式 evaluator 层
- 但对“目标真正完成”的判断仍然偏启发式
- 后续可以继续补：
  - 更强的生物任务 completion prompt
  - 更明确的 artifact / object success criteria
  - 基于对象 metadata 的硬性判定规则

### 3. 继续丰富前端任务解释

- 当前已能展示 checkpoint
- 后续还可以把“为什么要做下一步”解释得更明确
- 例如把每次重规划前后的差异高亮出来

## 当前边界

- 只有 `wired` skills 会进入执行链
- `planned` 只是 registry 占位，不会被正常规划执行
- planner preview 只构造上下文，不执行 runtime
- 状态现在会落到 `data/state/store.db`，底层持久化改成 SQLite
