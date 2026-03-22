# scAgent Roadmap

## 已完成

### 上传即真实评估（原 P0）

- [x] 解析 `.h5ad` 基础形状：`n_obs`、`n_vars`
- [x] 解析 `obs`、`var`、`obsm`、`uns`、`layers`
- [x] 推断候选 cell type 字段和 cluster 字段
- [x] 给出 preprocessing 状态：`raw_like / partially_processed / analysis_ready`
- [x] 给出当前可做分析和缺失前置条件
- [x] 上传后前端结果卡和对象概览直接展示这些评估信息

### 已落地的 UMAP / gene UMAP 能力（原 P1）

- [x] 明确识别是否已有 `X_umap`
- [x] 明确提示如果没有 UMAP 当前缺什么
- [x] 接入真实 `plot_gene_umap`

### 已落地的 readiness 能力（原 P2）

- [x] `assess_dataset` 作为正式内置技能

### 已落地的帮助站基础能力（原 P5）

- [x] `docs/*.md` 自动渲染成 HTML 帮助页
- [x] 中文帮助文档首页

### 已落地的执行编排增强

- [x] 每个 step 后引入显式 completion / evaluator 判定
- [x] 基于当前对象状态做 checkpoint 重规划
- [x] job 保存结构化 `checkpoints`
- [x] 前端任务卡展示执行检查点，而不是只显示纯文本摘要

## 进行中

### UMAP 补全为可自动达成（P1 + P2）

目标：当对象没有 UMAP 时，不只是提示缺什么，而是让 AI 能自动规划并执行到可画 UMAP 的前置链路。

- [ ] 如果没有 UMAP，允许 AI 自动规划预处理链
- [ ] `filter_cells`
- [ ] `normalize_total`
- [ ] `log1p_transform`
- [ ] `select_hvg`
- [ ] `run_pca`
- [ ] `compute_neighbors`
- [ ] `run_umap`

### 亚群再聚类（原 P3）

目标：把“选中一个 cluster 后继续细分分析”做成对象级工作流，而不是一次性脚本动作。

- [ ] 新增 `subcluster_group` 技能
- [ ] 支持对已有 cluster 取子集
- [ ] 支持对子集对象单独 recluster
- [ ] 在对象树中清晰展示 parent / child 关系

### 会话与对象控制（原 P4）

目标：让对象切换、对象生命周期和结果持久化成为稳定后端能力。

- [ ] `set_active_object` 做成真实后端能力
- [ ] pin / evict / reload 策略
- [ ] SQLite 持久化 session / object / job / artifact

### 帮助站补全（原 P5）

目标：从“能看文档”升级到“能帮助生物用户快速上手”。

- [ ] 案例页和截图页
- [ ] 面向生物用户的 FAQ

## 下一步优先级

### 1. 打通自动预处理链，补齐 UMAP 生成能力

这是当前最关键的一步，因为它决定了系统能否从“告诉用户为什么还不能画图”升级到“自动把对象推进到可分析状态”。

- [ ] 优先补齐：`normalize_total`、`log1p_transform`、`select_hvg`、`run_pca`、`compute_neighbors`、`run_umap`
- [ ] 让 AI 能基于 `assess_dataset` 的结果自动规划这条链
- [ ] 在前端清楚展示每一步为什么需要、做完后对象状态怎么变化

### 2. 做亚群再聚类，把对象树真正用起来

当基础预处理链打通后，下一步最有价值的是支持对子群继续分析，这会让对象体系和技能体系真正联动起来。

- [ ] 实现 `subcluster_group`
- [ ] 建立 subset / recluster 的对象衍生关系
- [ ] 在 UI 中体现 parent / child 对象链路

### 3. 做对象控制和持久化，稳定长期使用体验

如果没有对象控制和持久化，前面能力越多，状态管理越容易变乱。这部分是进入“可长期使用”阶段前必须补上的基础设施。

- [ ] 实现 `set_active_object`
- [ ] 明确对象缓存与回收策略
- [ ] 引入 SQLite 持久化 session / object / job / artifact

### 4. 补帮助站内容，降低非技术用户门槛

这部分不阻塞核心分析链路，但会直接影响产品可理解性和可用性。

- [ ] 增加案例页
- [ ] 增加截图页
- [ ] 增加面向生物用户的 FAQ

## 远期规划

### 二元环境支持（引入 R 环境）

这是一个未来规划项，暂不立即实施，但需要提前记录为后续架构演进方向。目标是让系统在必要时能把部分复杂分析任务切换到 R 环境执行，再把结果带回当前 IDE 工作流。

- [ ] 支持将当前对象或所需数据结构导出为 R 环境可消费的格式
- [ ] 支持在 R 环境中通过其任务机制执行复杂分析逻辑
- [ ] 支持在分析完成后将结果读回，并保存到 IDE 相关文件中
- [ ] 设计 Python / R 双环境之间的数据交换、任务状态和结果回收机制
