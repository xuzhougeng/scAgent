# scAgent To-Do Roadmap

## P0: 上传即真实评估

- [x] 解析 `.h5ad` 基础形状：`n_obs`、`n_vars`
- [x] 解析 `obs`、`var`、`obsm`、`uns`、`layers`
- [x] 推断候选 cell type 字段和 cluster 字段
- [x] 给出 preprocessing 状态：`raw_like / partially_processed / analysis_ready`
- [x] 给出当前可做分析和缺失前置条件
- [x] 上传后前端 inspector 直接展示这些评估信息

## P1: UMAP 与 gene UMAP

- [x] 明确识别是否已有 `X_umap`
- [x] 明确提示如果没有 UMAP 当前缺什么
- [x] 接入真实 `plot_gene_umap`
- [ ] 如果没有 UMAP，允许 AI 自动规划预处理链

## P2: 预处理与 readiness 推理

- [ ] `filter_cells`
- [ ] `normalize_total`
- [ ] `log1p_transform`
- [ ] `select_hvg`
- [ ] `run_pca`
- [ ] `compute_neighbors`
- [ ] `run_umap`
- [x] `assess_dataset` 作为正式内置技能

## P3: 亚群再聚类

- [ ] 新增 `subcluster_group` 技能
- [ ] 支持对已有 cluster 取子集
- [ ] 支持对子集对象单独 recluster
- [ ] 在对象树中清晰展示 parent / child 关系

## P4: 会话与对象控制

- [ ] `set_active_object` 做成真实后端能力
- [ ] pin / evict / reload 策略
- [ ] SQLite 持久化 session/object/job/artifact

## P5: 帮助站

- [x] `docs/*.md` 自动渲染成 HTML 帮助页
- [x] 中文帮助文档首页
- [ ] 案例页和截图页
- [ ] 面向生物用户的 FAQ
