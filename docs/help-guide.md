# scAgent 中文使用指南

## 当前定位

`scAgent` 目前是一个面向单细胞分析的交互式控制面板：

- 前端是轻量 WebView 友好的静态页面
- Go 负责 session、对象树、作业、技能编排
- Python 负责 `.h5ad` 文件解析和分析执行

## 最小使用流程

1. 启动 Python runtime
2. 启动 Go server
3. 打开浏览器访问主页面
4. 上传一个 `.h5ad`
5. 查看系统自动给出的数据评估
6. 再继续做 UMAP、marker、subcluster 等操作

## 上传后系统会自动告诉你什么

上传一个 `.h5ad` 后，系统会立刻评估：

- 细胞数量和基因数量
- `obs`、`var`、`obsm`、`uns`、`layers` 里有什么
- 有没有候选的细胞类型字段
- 有没有 cluster 字段
- 有没有 `X_pca`、`X_umap`、`neighbors`
- 当前更像是原始数据、部分处理数据，还是已经接近可分析状态

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
3. 如果没有，先明确提示缺少 UMAP
4. 再给出建议预处理链

### 案例 3：对某个 cluster 继续 subcluster

用户目标：

`把 leiden=0 的细胞单独拿出来，再用 0.6 的分辨率重聚类`

系统预期行为：

1. 在当前对象上按 `leiden == 0` 做 subset
2. 为子对象建立新的 object 节点
3. 对子对象执行 recluster
4. 把新的子对象和结果图表挂到右侧 inspector

## 帮助站维护方式

帮助页本身来自 `docs/*.md`。

- 新增一个 Markdown 文件，就会自动出现在帮助站导航里
- 文档标题默认取文件里的第一个 `# Heading`
- 适合放：
  - 功能说明
  - 操作教程
  - 测试案例
  - 架构说明

## Tool 扩展

如果你要新增自定义 tool，直接看：

- [自定义 Tool 指南](custom-tools.md)
- [Skill Catalog](skill-catalog.md)
- [Agent 架构图](agent-architecture.md)

前者说明 registry、runtime 和 planner 三层怎么接；后者说明当前哪些 skill 已经是 `wired`，哪些还只是 `planned`。
