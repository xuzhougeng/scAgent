# scAgent 中文使用指南

## 当前定位

`scAgent` 目前是一个面向单细胞分析的交互式控制台：

- 前端是轻量、WebView 友好的静态页面
- Go 负责 session、对象树、job、planner、evaluator 和事件推送
- Python 负责 `.h5ad` 解析、分析执行和插件 skill 运行

## 页面里会看到什么

主页面大致分成四块：

1. 系统状态
   显示当前模式、规划器、运行时健康、可执行技能和环境检查。
2. 对象与会话概览
   显示当前 session、对象数量、任务数量和 artifact 数量。
3. 聊天与任务卡
   用户消息会触发一个后台 job，任务卡会展示计划、执行检查点、步骤结果和文件产物。
4. 帮助与插件入口
   可以打开 `help.html` 查看文档，也可以打开 `plugins.html` 管理 Skill Hub。

## 最小使用流程

1. 启动 Python runtime
2. 启动 Go server
3. 打开主页面
4. 上传一个 `.h5ad`
5. 先看系统自动给出的数据评估
6. 再继续做 UMAP、marker、subset、subcluster 或导出操作
7. 在任务卡里观察执行检查点和结果文件

## 上传后系统会自动告诉你什么

上传一个 `.h5ad` 后，系统会立刻评估：

- 细胞数量和基因数量
- `obs`、`var`、`obsm`、`uns`、`layers` 里有什么
- 有没有候选的细胞类型字段
- 有没有 cluster 字段
- 有没有 `X_pca`、`X_umap`、`neighbors`
- 当前更像是原始数据、部分处理数据，还是已经接近可分析状态

## 长任务执行时前端会显示什么

如果一个任务包含多步分析，主聊天区不会只显示一段纯文本，而是展示结构化任务卡。

常见内容包括：

- `执行计划`
  当前还准备执行哪些步骤。
- `执行检查点`
  例如：
  - `初始规划`
  - `完成判定`
  - `检查点重规划`
- `步骤结果`
  每一步的 summary、状态和输出对象。
- `结果文件`
  生成的图、表和导出文件。

这意味着即使中间某一步耗时很长，前端也能通过 SSE 持续看到 job 的最新状态，而不是让模型一直阻塞等待。

## 典型案例

### 案例 1：上传后先看数据状态

用户目标：

`先看看这个 h5ad 里面有什么，哪些分析能直接做。`

系统应该回答的重点：

- 当前对象的基础规模
- 已有注释字段
- 是否已经存在 UMAP / PCA / neighbors / clusters
- 现在可以直接做什么
- 如果想画 gene UMAP，还缺什么

### 案例 2：绘制基因 UMAP

用户目标：

`画一下 GATA3 的 UMAP`

系统预期行为：

1. 先判断当前对象有没有 `X_umap`
2. 如果有，直接执行 `plot_gene_umap`
3. 如果没有，自动规划需要的预处理链或明确提示缺失前置条件
4. 在任务卡里展示执行检查点和图像 artifact

### 案例 3：对某个 cluster 继续 subcluster

用户目标：

`把 leiden=0 的细胞单独拿出来，再用 0.6 的分辨率重聚类`

系统预期行为：

1. 在当前对象上按 `leiden == 0` 做 subset
2. 为子对象建立新的 object 节点
3. 对子对象执行 recluster
4. 在聊天结果卡、对象概览和 artifact 区里展示新的对象和图表

## Skill Hub 怎么用

打开 `plugins.html` 后，你可以：

- 上传 zip 插件包
- 刷新当前 Skill Hub 状态
- 启用或关闭某个技能包
- 点击任一技能查看规范、输入输出和运行配置

技能详情现在通过悬浮窗展示：

- `ESC` 可以关闭
- 方向键可以在不同技能之间快速切换

## 帮助站维护方式

帮助页来自 `docs/*.md`。

- 新增一个 Markdown 文件，就会自动出现在帮助站导航里
- 文档标题默认取文件里的第一个 `# Heading`
- 适合放：
  - 功能说明
  - 操作教程
  - 常见问题
  - 架构说明

## 扩展阅读

如果你要新增自定义 tool 或理解当前架构，优先看：

- [自定义 Tool 指南](custom-tools.md)
- [Skill Catalog](skill-catalog.md)
- [Skill Hub](skill-hub.md)
- [Agent 架构图](agent-architecture.md)

其中：

- `Skill Catalog` 用来看哪些 skill 已经是 `wired`
- `Skill Hub` 用来看插件包格式和管理方式
- `Agent 架构图` 用来看 planner、evaluator、runtime 和前端任务卡如何串起来
