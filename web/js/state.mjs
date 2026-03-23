export const appState = {
  sessionId: null,
  workspaceId: null,
  activeObjectId: null,
  selectedResourceKey: null,
  snapshot: null,
  workspaceSnapshot: null,
  workspaceList: [],
  workspaceStatus: "",
  skills: [],
  plugins: [],
  systemStatus: null,
  eventSource: null,
  plannerPreview: null,
  workspaceNavigatorCollapsed: false,
  sessionMetaCollapsed: false,
  workspaceFilesModalRenderVersion: 0,
  chatRenderVersion: 0,
  artifactTextCache: new Map(),
  composerPending: false,
  cancelPendingJobId: null,
  composerEditJobId: null,
  composerEditOriginalMessage: "",
};

export const storageKeys = {
  workspaceId: "scagent.workspaceId",
  sessionId: "scagent.sessionId",
  leftPanelWidth: "scagent.leftPanelWidth",
  rightPanelWidth: "scagent.rightPanelWidth",
  rightPanelCollapsed: "scagent.rightPanelCollapsed",
};

export const layoutConfig = {
  defaultLeftPanelWidth: 300,
  defaultRightPanelWidth: 360,
  minLeftPanelWidth: 260,
  minRightPanelWidth: 280,
  minConsoleWidth: 420,
  keyboardResizeStep: 24,
};

export const quickActions = [
  { label: "查看数据集", prompt: "查看当前数据集概览" },
  { label: "常规预处理", prompt: "完成常规的数据预处理" },
  { label: "绘制 UMAP", prompt: "绘制当前对象的 UMAP 图" },
  { label: "筛选 cortex 细胞", prompt: "把 cortex 细胞筛选出来" },
  { label: "重新聚类", prompt: "对当前对象重新聚类" },
  { label: "查找 marker", prompt: "查找当前对象的 marker 基因" },
  { label: "导出 h5ad", prompt: "导出当前对象为 h5ad" },
];

export const roleLabels = {
  user: "你",
  assistant: "助手",
};

export const skillLabels = {
  inspect_dataset: "查看数据集",
  assess_dataset: "评估数据集",
  summarize_qc: "汇总 QC",
  plot_qc_metrics: "绘制 QC 指标",
  filter_cells: "按 QC 过滤细胞",
  filter_genes: "按阈值过滤基因",
  normalize_total: "总表达归一化",
  log1p_transform: "log1p 变换",
  select_hvg: "选择高变基因",
  scale_matrix: "缩放表达矩阵",
  run_pca: "计算 PCA",
  compute_neighbors: "计算邻接图",
  run_umap: "计算 UMAP",
  prepare_umap: "完成常规预处理",
  subset_cells: "筛选细胞子集",
  subcluster_from_global: "全局对象亚群分析",
  score_gene_set: "基因集打分",
  recluster: "重新聚类",
  reanalyze_subset: "已提取亚群再分析",
  subcluster_group: "按群体亚聚类",
  rename_clusters: "重命名聚类",
  find_markers: "查找 marker 基因",
  plot_umap: "绘制 UMAP 图",
  run_python_analysis: "执行自定义 Python 分析",
  plot_gene_umap: "绘制基因 UMAP",
  plot_dotplot: "绘制点图",
  plot_violin: "绘制小提琴图",
  plot_heatmap: "绘制热图",
  plot_celltype_composition: "绘制组成图",
  export_h5ad: "导出 h5ad",
  export_markers_csv: "导出 markers CSV",
};

export const skillPrompts = {
  inspect_dataset: "查看当前数据集概览",
  assess_dataset: "评估当前数据集的预处理状态",
  summarize_qc: "汇总当前对象的 QC 指标",
  plot_qc_metrics: "绘制当前对象的 QC 分布图",
  filter_cells: "按 QC 阈值过滤当前对象中的细胞",
  filter_genes: "按表达阈值过滤当前对象中的基因",
  normalize_total: "对当前对象做总表达归一化",
  log1p_transform: "对当前对象执行 log1p 变换",
  select_hvg: "为当前对象选择高变基因",
  scale_matrix: "对当前对象的表达矩阵做缩放",
  run_pca: "为当前对象计算 PCA",
  compute_neighbors: "为当前对象计算邻接图",
  run_umap: "为当前对象计算 UMAP",
  prepare_umap: "完成当前对象的常规数据预处理并生成 UMAP",
  subset_cells: "从当前对象中筛选一组细胞",
  subcluster_from_global: "保持全局对象不变，只对某个群体执行亚群分析",
  score_gene_set: "为当前对象计算一个基因集得分",
  recluster: "对当前对象重新聚类",
  reanalyze_subset: "对已经提取出来的亚群重新执行一轮亚群分析",
  subcluster_group: "提取一个或多个群体并重新做亚聚类",
  rename_clusters: "重命名当前对象中的聚类标签",
  find_markers: "查找当前对象的 marker 基因",
  plot_umap: "绘制当前对象的 UMAP 图",
  plot_gene_umap: "绘制当前对象中某个基因的 UMAP 图",
  run_python_analysis: "对当前对象执行一段自定义 Python 分析",
  plot_dotplot: "绘制当前对象的 marker 点图",
  plot_violin: "绘制当前对象的基因小提琴图",
  plot_heatmap: "绘制当前对象的基因表达热图",
  plot_celltype_composition: "按样本或条件绘制细胞组成图",
  export_h5ad: "导出当前对象为 h5ad",
  export_markers_csv: "将当前 marker 结果导出为 CSV",
};

export const systemModeLabels = {
  live: "正式模式",
  demo: "演示模式",
};

export const plannerModeLabels = {
  llm: "LLM",
  fake: "规则规划",
};

export const runtimeModeLabels = {
  hybrid_demo: "混合演示",
  demo: "演示",
  live: "正式",
  real: "真实",
  mock: "占位",
};

export const jobStatusLabels = {
  queued: "排队中",
  pending: "等待中",
  running: "运行中",
  succeeded: "本次执行成功",
  incomplete: "本次执行未完成",
  failed: "失败",
  canceled: "已取消",
};

export const jobPhaseStatusLabels = {
  pending: "等待中",
  running: "进行中",
  completed: "已完成",
  skipped: "已跳过",
  failed: "失败",
  canceled: "已取消",
};

export const objectKindLabels = {
  raw_dataset: "原始数据集",
  filtered_dataset: "过滤后数据集",
  subset: "细胞子集",
  reclustered_subset: "重聚类子集",
  de_result: "差异分析结果",
  marker_result: "marker 结果",
  plot_artifact: "绘图产物",
  object_summary: "对象摘要",
  unknown: "未知对象",
};

export const objectStateLabels = {
  resident: "常驻",
  materialized: "已落盘",
  evicted: "已卸载",
  deleted: "已删除",
};

export const artifactKindLabels = {
  plot: "图像结果",
  table: "表格结果",
  object_summary: "对象摘要",
  file: "通用文件",
};

export const annotationRoleLabels = {
  cell_type: "细胞类型",
  cluster: "聚类",
  covariate: "协变量",
  annotation: "注释",
};

export const analysisStateLabels = {
  analysis_ready: "可直接分析",
  partially_processed: "部分预处理",
  raw_like: "接近原始数据",
};
