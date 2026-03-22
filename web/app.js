const appState = {
  sessionId: null,
  workspaceId: null,
  activeObjectId: null,
  snapshot: null,
  workspaceSnapshot: null,
  workspaceList: [],
  workspaceStatus: "",
  skills: [],
  plugins: [],
  systemStatus: null,
  eventSource: null,
  plannerPreview: null,
  chatRenderVersion: 0,
  artifactTextCache: new Map(),
};

const storageKeys = {
  workspaceId: "scagent.workspaceId",
  sessionId: "scagent.sessionId",
  leftPanelWidth: "scagent.leftPanelWidth",
  rightPanelWidth: "scagent.rightPanelWidth",
  rightPanelCollapsed: "scagent.rightPanelCollapsed",
};

const layoutConfig = {
  defaultLeftPanelWidth: 300,
  defaultRightPanelWidth: 360,
  minLeftPanelWidth: 260,
  minRightPanelWidth: 280,
  minConsoleWidth: 420,
  keyboardResizeStep: 24,
};

const quickActions = [
  { label: "查看数据集", prompt: "查看当前数据集概览" },
  { label: "常规预处理", prompt: "完成常规的数据预处理" },
  { label: "绘制 UMAP", prompt: "绘制当前对象的 UMAP 图" },
  { label: "筛选 cortex 细胞", prompt: "把 cortex 细胞筛选出来" },
  { label: "重新聚类", prompt: "对当前对象重新聚类" },
  { label: "查找 marker", prompt: "查找当前对象的 marker 基因" },
  { label: "导出 h5ad", prompt: "导出当前对象为 h5ad" },
];

const roleLabels = {
  user: "你",
  assistant: "助手",
};

const skillLabels = {
  inspect_dataset: "查看数据集",
  assess_dataset: "评估数据集",
  normalize_total: "总表达归一化",
  log1p_transform: "log1p 变换",
  select_hvg: "选择高变基因",
  run_pca: "计算 PCA",
  compute_neighbors: "计算邻接图",
  run_umap: "计算 UMAP",
  prepare_umap: "完成常规预处理",
  subset_cells: "筛选细胞子集",
  subcluster_from_global: "全局对象亚群分析",
  recluster: "重新聚类",
  reanalyze_subset: "已提取亚群再分析",
  find_markers: "查找 marker 基因",
  plot_umap: "绘制 UMAP 图",
  run_python_analysis: "执行自定义 Python 分析",
  plot_gene_umap: "绘制基因 UMAP",
  plot_dotplot: "绘制点图",
  plot_violin: "绘制小提琴图",
  export_h5ad: "导出 h5ad",
};

const skillPrompts = {
  inspect_dataset: "查看当前数据集概览",
  assess_dataset: "评估当前数据集的预处理状态",
  normalize_total: "对当前对象做总表达归一化",
  log1p_transform: "对当前对象执行 log1p 变换",
  select_hvg: "为当前对象选择高变基因",
  run_pca: "为当前对象计算 PCA",
  compute_neighbors: "为当前对象计算邻接图",
  run_umap: "为当前对象计算 UMAP",
  prepare_umap: "完成当前对象的常规数据预处理并生成 UMAP",
  subset_cells: "从当前对象中筛选一组细胞",
  subcluster_from_global: "保持全局对象不变，只对某个群体执行亚群分析",
  recluster: "对当前对象重新聚类",
  reanalyze_subset: "对已经提取出来的亚群重新执行一轮亚群分析",
  find_markers: "查找当前对象的 marker 基因",
  plot_umap: "绘制当前对象的 UMAP 图",
  plot_gene_umap: "绘制当前对象中某个基因的 UMAP 图",
  run_python_analysis: "对当前对象执行一段自定义 Python 分析",
  plot_dotplot: "绘制当前对象的 marker 点图",
  plot_violin: "绘制当前对象的基因小提琴图",
  export_h5ad: "导出当前对象为 h5ad",
};

const systemModeLabels = {
  live: "正式模式",
  demo: "演示模式",
};

const plannerModeLabels = {
  llm: "LLM",
  fake: "规则规划",
};

const runtimeModeLabels = {
  hybrid_demo: "混合演示",
  demo: "演示",
  live: "正式",
  real: "真实",
  mock: "占位",
};

const jobStatusLabels = {
  queued: "排队中",
  pending: "等待中",
  running: "运行中",
  succeeded: "本次执行成功",
  incomplete: "本次执行未完成",
  failed: "失败",
  canceled: "已取消",
};

const jobPhaseStatusLabels = {
  pending: "等待中",
  running: "进行中",
  completed: "已完成",
  skipped: "已跳过",
  failed: "失败",
};

const objectKindLabels = {
  raw_dataset: "原始数据集",
  subset: "细胞子集",
  reclustered_subset: "重聚类子集",
};

const objectStateLabels = {
  resident: "常驻",
  materialized: "已落盘",
};

const annotationRoleLabels = {
  cell_type: "细胞类型",
  cluster: "聚类",
  covariate: "协变量",
  annotation: "注释",
};

const analysisStateLabels = {
  analysis_ready: "可直接分析",
  partially_processed: "部分预处理",
  raw_like: "接近原始数据",
};

document.addEventListener("DOMContentLoaded", async () => {
  bindSidebarResize();
  bindComposer();
  bindUpload();
  bindPlannerPreview();
  bindImageModal();
  bindStatusOverviewModal();
  renderQuickActions();
  await bootstrap();
});

async function bootstrap() {
  await refreshCapabilityState();
  const restored = await restoreContext();
  if (!restored) {
    const snapshot = await createWorkspaceWithLabel("PBMC3K 测试会话");
    syncSnapshot(snapshot);
    await refreshWorkspaceSnapshot();
  }
  connectEvents();
  render();
}

function syncSnapshot(snapshot) {
  appState.snapshot = snapshot;
  appState.sessionId = snapshot?.session?.id || null;
  appState.workspaceId = snapshot?.workspace?.id || snapshot?.session?.workspace_id || null;
  appState.activeObjectId = snapshot?.session?.active_object_id || null;
  persistContext();

  if (!snapshot?.workspace) {
    return;
  }

  const currentConversations =
    appState.workspaceSnapshot?.workspace?.id === snapshot.workspace.id
      ? appState.workspaceSnapshot.conversations || []
      : [];

  appState.workspaceSnapshot = {
    workspace: snapshot.workspace,
    conversations: upsertConversation(currentConversations, snapshot.session),
    objects: snapshot.objects || [],
    artifacts: snapshot.artifacts || [],
  };
  appState.workspaceList = upsertWorkspace(appState.workspaceList, snapshot.workspace);
}

function upsertConversation(conversations, session) {
  const next = Array.isArray(conversations) ? [...conversations] : [];
  if (!session?.id) {
    return next.sort(compareConversationCreatedAt);
  }

  const index = next.findIndex((item) => item.id === session.id);
  if (index >= 0) {
    next[index] = session;
  } else {
    next.push(session);
  }
  return next.sort(compareConversationCreatedAt);
}

function compareConversationCreatedAt(a, b) {
  return compareByTimestamp(
    a?.last_accessed_at || a?.updated_at || a?.created_at,
    b?.last_accessed_at || b?.updated_at || b?.created_at,
  );
}

function upsertWorkspace(workspaces, workspace) {
  const next = Array.isArray(workspaces) ? [...workspaces] : [];
  if (!workspace?.id) {
    return next.sort(compareWorkspaceRecency);
  }

  const index = next.findIndex((item) => item.id === workspace.id);
  if (index >= 0) {
    next[index] = workspace;
  } else {
    next.push(workspace);
  }
  return next.sort(compareWorkspaceRecency);
}

function compareWorkspaceRecency(a, b) {
  return compareByTimestamp(
    a?.last_accessed_at || a?.updated_at || a?.created_at,
    b?.last_accessed_at || b?.updated_at || b?.created_at,
  );
}

function compareByTimestamp(a, b) {
  return String(b || "").localeCompare(String(a || ""));
}

async function refreshWorkspaceSnapshot() {
  if (!appState.workspaceId) {
    appState.workspaceSnapshot = null;
    return;
  }
  appState.workspaceSnapshot = await fetchJSON(`/api/workspaces/${appState.workspaceId}`);
}

async function refreshWorkspaceList() {
  const payload = await fetchJSON("/api/workspaces");
  appState.workspaceList = (payload?.workspaces || []).sort(compareWorkspaceRecency);
  return appState.workspaceList;
}

function persistContext() {
  try {
    if (appState.workspaceId) {
      window.localStorage.setItem(storageKeys.workspaceId, appState.workspaceId);
    }
    if (appState.sessionId) {
      window.localStorage.setItem(storageKeys.sessionId, appState.sessionId);
    }
  } catch (_error) {
  }
}

function loadPersistedContext() {
  try {
    return {
      workspaceId: window.localStorage.getItem(storageKeys.workspaceId) || "",
      sessionId: window.localStorage.getItem(storageKeys.sessionId) || "",
    };
  } catch (_error) {
    return { workspaceId: "", sessionId: "" };
  }
}

function bindSidebarResize() {
  const shell = document.querySelector(".shell");
  const leftHandle = document.getElementById("leftSidebarHandle");
  const rightHandle = document.getElementById("rightSidebarHandle");
  const rightToggle = document.getElementById("rightSidebarToggle");
  if (!shell || !leftHandle || !rightHandle || !rightToggle) {
    return;
  }

  applySidebarWidths(shell, restoreSidebarWidths(), false);
  setRightSidebarCollapsed(shell, restoreRightSidebarCollapsed(), false);

  bindSidebarHandle({
    shell,
    handle: leftHandle,
    side: "left",
  });
  bindSidebarHandle({
    shell,
    handle: rightHandle,
    side: "right",
  });

  rightToggle.addEventListener("click", () => {
    setRightSidebarCollapsed(shell, !isRightSidebarCollapsed(shell), true);
  });

  window.addEventListener("resize", () => {
    applySidebarWidths(shell, readSidebarWidths(shell), false);
    if (window.matchMedia("(max-width: 1100px)").matches) {
      setRightSidebarCollapsed(shell, false, false);
    } else {
      setRightSidebarCollapsed(shell, restoreRightSidebarCollapsed(), false);
    }
  });
}

function bindSidebarHandle({ shell, handle, side }) {
  handle.addEventListener("pointerdown", (event) => {
    if (window.matchMedia("(max-width: 1100px)").matches) {
      return;
    }
    if (event.target.closest(".sidebar-collapse-button")) {
      return;
    }
    if (side === "right" && isRightSidebarCollapsed(shell)) {
      return;
    }

    event.preventDefault();
    const startX = event.clientX;
    const startWidths = readSidebarWidths(shell);

    document.body.classList.add("is-resizing");
    handle.classList.add("active");
    handle.setPointerCapture?.(event.pointerId);

    const onPointerMove = (moveEvent) => {
      const delta = moveEvent.clientX - startX;
      const nextWidths =
        side === "left"
          ? { ...startWidths, left: startWidths.left + delta }
          : { ...startWidths, right: startWidths.right - delta };
      applySidebarWidths(shell, nextWidths, true);
    };

    const stopResize = () => {
      document.body.classList.remove("is-resizing");
      handle.classList.remove("active");
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", stopResize);
      window.removeEventListener("pointercancel", stopResize);
    };

    window.addEventListener("pointermove", onPointerMove);
    window.addEventListener("pointerup", stopResize);
    window.addEventListener("pointercancel", stopResize);
  });

  handle.addEventListener("keydown", (event) => {
    if (window.matchMedia("(max-width: 1100px)").matches) {
      return;
    }
    if (side === "right" && isRightSidebarCollapsed(shell)) {
      return;
    }

    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") {
      return;
    }

    const delta = event.key === "ArrowLeft" ? -layoutConfig.keyboardResizeStep : layoutConfig.keyboardResizeStep;
    const widths = readSidebarWidths(shell);
    const nextWidths =
      side === "left"
        ? { ...widths, left: widths.left + delta }
        : { ...widths, right: widths.right - delta };

    applySidebarWidths(shell, nextWidths, true);
    event.preventDefault();
  });
}

function restoreSidebarWidths() {
  try {
    const left = parseStoredWidth(window.localStorage.getItem(storageKeys.leftPanelWidth));
    const right = parseStoredWidth(window.localStorage.getItem(storageKeys.rightPanelWidth));
    return {
      left: left || layoutConfig.defaultLeftPanelWidth,
      right: right || layoutConfig.defaultRightPanelWidth,
    };
  } catch (_error) {
    return {
      left: layoutConfig.defaultLeftPanelWidth,
      right: layoutConfig.defaultRightPanelWidth,
    };
  }
}

function restoreRightSidebarCollapsed() {
  try {
    return window.localStorage.getItem(storageKeys.rightPanelCollapsed) === "true";
  } catch (_error) {
    return false;
  }
}

function readSidebarWidths(shell) {
  const styles = getComputedStyle(shell);
  return {
    left: parseCSSPixelValue(styles.getPropertyValue("--left-panel-width"), layoutConfig.defaultLeftPanelWidth),
    right: parseCSSPixelValue(styles.getPropertyValue("--right-panel-width"), layoutConfig.defaultRightPanelWidth),
  };
}

function applySidebarWidths(shell, widths, persist = false) {
  const nextWidths = clampSidebarWidths(shell, widths);
  shell.style.setProperty("--left-panel-width", `${nextWidths.left}px`);
  shell.style.setProperty("--right-panel-width", `${nextWidths.right}px`);

  if (!persist) {
    return;
  }

  try {
    window.localStorage.setItem(storageKeys.leftPanelWidth, String(Math.round(nextWidths.left)));
    window.localStorage.setItem(storageKeys.rightPanelWidth, String(Math.round(nextWidths.right)));
  } catch (_error) {
  }
}

function isRightSidebarCollapsed(shell) {
  return shell.classList.contains("right-sidebar-collapsed");
}

function setRightSidebarCollapsed(shell, collapsed, persist = false) {
  const shouldCollapse = !window.matchMedia("(max-width: 1100px)").matches && collapsed;
  shell.classList.toggle("right-sidebar-collapsed", shouldCollapse);
  syncRightSidebarToggle(shell);

  if (!persist) {
    return;
  }

  try {
    window.localStorage.setItem(storageKeys.rightPanelCollapsed, String(shouldCollapse));
  } catch (_error) {
  }
}

function syncRightSidebarToggle(shell) {
  const button = document.getElementById("rightSidebarToggle");
  if (!button) {
    return;
  }

  const collapsed = isRightSidebarCollapsed(shell);
  button.textContent = collapsed ? "<" : ">";
  button.setAttribute("aria-expanded", collapsed ? "false" : "true");
  button.setAttribute("aria-label", collapsed ? "展开右侧信息栏" : "收起右侧信息栏");
  button.title = collapsed ? "展开右侧信息栏" : "收起右侧信息栏";
}

function clampSidebarWidths(shell, widths) {
  const styles = getComputedStyle(shell);
  const paddingLeft = parseFloat(styles.paddingLeft) || 0;
  const paddingRight = parseFloat(styles.paddingRight) || 0;
  const contentWidth = shell.clientWidth - paddingLeft - paddingRight;
  const handleWidth = parseCSSPixelValue(styles.getPropertyValue("--resize-handle-width"), 12);
  const usableWidth = Math.max(0, contentWidth - handleWidth * 2);

  let left = clampNumber(widths.left, layoutConfig.minLeftPanelWidth, usableWidth);
  let right = clampNumber(widths.right, layoutConfig.minRightPanelWidth, usableWidth);

  const maxLeft = Math.max(
    layoutConfig.minLeftPanelWidth,
    usableWidth - layoutConfig.minConsoleWidth - right,
  );
  left = clampNumber(left, layoutConfig.minLeftPanelWidth, maxLeft);

  const maxRight = Math.max(
    layoutConfig.minRightPanelWidth,
    usableWidth - layoutConfig.minConsoleWidth - left,
  );
  right = clampNumber(right, layoutConfig.minRightPanelWidth, maxRight);

  const finalMaxLeft = Math.max(
    layoutConfig.minLeftPanelWidth,
    usableWidth - layoutConfig.minConsoleWidth - right,
  );
  left = clampNumber(left, layoutConfig.minLeftPanelWidth, finalMaxLeft);

  return { left, right };
}

function parseStoredWidth(value) {
  const width = Number.parseFloat(value || "");
  return Number.isFinite(width) && width > 0 ? width : 0;
}

function parseCSSPixelValue(value, fallback) {
  const width = Number.parseFloat(String(value || "").trim());
  return Number.isFinite(width) ? width : fallback;
}

function clampNumber(value, min, max) {
  return Math.min(Math.max(value, min), max);
}

async function createWorkspaceWithLabel(label) {
  return fetchJSON("/api/workspaces", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ label }),
  });
}

function preferredConversation(workspaceSnapshot, preferredSessionId = "") {
  const conversations = workspaceSnapshot?.conversations || [];
  if (!conversations.length) {
    return null;
  }
  if (preferredSessionId) {
    const exact = conversations.find((conversation) => conversation.id === preferredSessionId);
    if (exact) {
      return exact;
    }
  }
  return [...conversations].sort(compareConversationCreatedAt)[0] || null;
}

async function restoreContext() {
  const persisted = loadPersistedContext();

  if (persisted.sessionId) {
    try {
      const snapshot = await fetchJSON(`/api/sessions/${persisted.sessionId}`);
      syncSnapshot(snapshot);
      await Promise.all([refreshWorkspaceSnapshot(), refreshWorkspaceList()]);
      return true;
    } catch (_error) {
    }
  }

  await refreshWorkspaceList();
  const fallbackWorkspaceId = persisted.workspaceId || appState.workspaceList[0]?.id;
  if (!fallbackWorkspaceId) {
    return false;
  }

  try {
    const workspaceSnapshot = await fetchJSON(`/api/workspaces/${fallbackWorkspaceId}`);
    const conversation = preferredConversation(workspaceSnapshot, persisted.sessionId);
    if (!conversation) {
      return false;
    }
    appState.workspaceSnapshot = workspaceSnapshot;
    const snapshot = await fetchJSON(`/api/sessions/${conversation.id}`);
    syncSnapshot(snapshot);
    return true;
  } catch (_error) {
    return false;
  }
}

async function switchConversation(sessionId) {
  if (!sessionId || sessionId === appState.sessionId) {
    return;
  }

  appState.workspaceStatus = "正在切换对话...";
  renderSessionMeta();

  try {
    const snapshot = await fetchJSON(`/api/sessions/${sessionId}`);
    appState.plannerPreview = null;
    syncSnapshot(snapshot);
    await Promise.all([refreshWorkspaceSnapshot(), refreshWorkspaceList()]);
    appState.workspaceStatus = `已切换到 ${formatConversationLabel(snapshot.session)}。`;
    connectEvents();
    render();
  } catch (error) {
    appState.workspaceStatus = error.message;
    renderSessionMeta();
  }
}

async function createConversation() {
  if (!appState.workspaceId) {
    return;
  }

  const nextIndex = (appState.workspaceSnapshot?.conversations?.length || 0) + 1;
  appState.workspaceStatus = "正在创建新对话...";
  renderSessionMeta();

  try {
    const snapshot = await fetchJSON(`/api/workspaces/${appState.workspaceId}/conversations`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label: `分析对话 ${nextIndex}` }),
    });
    appState.plannerPreview = null;
    syncSnapshot(snapshot);
    await Promise.all([refreshWorkspaceSnapshot(), refreshWorkspaceList()]);
    appState.workspaceStatus = `已创建并切换到 ${formatConversationLabel(snapshot.session)}。`;
    connectEvents();
    render();
  } catch (error) {
    appState.workspaceStatus = error.message;
    renderSessionMeta();
  }
}

async function switchWorkspace(workspaceId) {
  if (!workspaceId) {
    return;
  }
  if (workspaceId === appState.workspaceId) {
    appState.workspaceStatus = "当前已在该 workspace。";
    renderSessionMeta();
    return;
  }

  appState.workspaceStatus = "正在切换 workspace...";
  renderSessionMeta();

  try {
    const workspaceSnapshot = await fetchJSON(`/api/workspaces/${workspaceId}`);
    const conversation = preferredConversation(workspaceSnapshot);
    if (!conversation) {
      throw new Error("目标 workspace 暂无可用对话。");
    }
    appState.workspaceSnapshot = workspaceSnapshot;
    const snapshot = await fetchJSON(`/api/sessions/${conversation.id}`);
    appState.plannerPreview = null;
    syncSnapshot(snapshot);
    await refreshWorkspaceList();
    appState.workspaceStatus = `已切换到 ${workspaceSnapshot.workspace?.label || workspaceId}。`;
    connectEvents();
    render();
  } catch (error) {
    appState.workspaceStatus = error.message;
    renderSessionMeta();
  }
}

async function createWorkspace() {
  const nextIndex = (appState.workspaceList?.length || 0) + 1;
  appState.workspaceStatus = "正在创建 workspace...";
  renderSessionMeta();

  try {
    const snapshot = await createWorkspaceWithLabel(`分析工作区 ${nextIndex}`);
    appState.plannerPreview = null;
    syncSnapshot(snapshot);
    await Promise.all([refreshWorkspaceSnapshot(), refreshWorkspaceList()]);
    appState.workspaceStatus = `已创建 ${snapshot.workspace?.label || "新 workspace"}。`;
    connectEvents();
    render();
  } catch (error) {
    appState.workspaceStatus = error.message;
    renderSessionMeta();
  }
}

async function refreshCapabilityState() {
  const [status, skillsResponse, pluginsResponse] = await Promise.all([
    fetchJSON("/api/status"),
    fetchJSON("/api/skills"),
    fetchJSON("/api/plugins"),
  ]);
  appState.systemStatus = status;
  appState.skills = skillsResponse.skills || [];
  appState.plugins = pluginsResponse.bundles || pluginsResponse.plugins || [];
}

function bindComposer() {
  const form = document.getElementById("composer");
  const input = document.getElementById("messageInput");
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    await submitMessage();
  });

  input.addEventListener("keydown", async (event) => {
    if (event.key !== "Enter" || event.shiftKey || event.isComposing) {
      return;
    }
    event.preventDefault();
    await submitMessage();
  });
}

async function submitMessage() {
  const input = document.getElementById("messageInput");
  const message = input.value.trim();
  if (!message || !appState.sessionId) {
    return;
  }

  input.value = "";
  const response = await fetchJSON("/api/messages", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      session_id: appState.sessionId,
      message,
    }),
  });
  syncSnapshot(response.snapshot);
  render();
}

function bindUpload() {
  const form = document.getElementById("uploadForm");
  const input = document.getElementById("fileInput");
  const status = document.getElementById("uploadStatus");

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const file = input.files?.[0];
    if (!file || !appState.sessionId) {
      status.textContent = "请先选择一个 .h5ad 文件。";
      return;
    }

    const formData = new FormData();
    formData.append("file", file);
    status.textContent = `正在上传 ${file.name}...`;

    try {
      const response = await fetchJSON(`/api/sessions/${appState.sessionId}/upload`, {
        method: "POST",
        body: formData,
      });
      syncSnapshot(response.snapshot);
      appState.activeObjectId = response.object.id;
      status.textContent = `${file.name} 已作为 ${response.object.label} 附加到当前 workspace。`;
      input.value = "";
      render();
    } catch (error) {
      status.textContent = error.message;
    }
  });
}

function bindPlannerPreview() {
  const button = document.getElementById("plannerPreviewButton");
  const status = document.getElementById("plannerPreviewStatus");
  const input = document.getElementById("messageInput");

  button.addEventListener("click", async () => {
    if (!appState.sessionId) {
      status.textContent = "会话尚未就绪。";
      return;
    }

    const message = input.value.trim();
    if (!message) {
      status.textContent = "请先输入一条消息。";
      return;
    }

    status.textContent = "正在生成规划预览...";
    try {
      appState.plannerPreview = await fetchJSON(
        `/api/sessions/${appState.sessionId}/planner-preview`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ message }),
        },
      );
      status.textContent = `规划预览已生成，当前规划器为 ${formatPlannerMode(appState.plannerPreview.planner_mode)}。`;
      renderPlannerPreview();
    } catch (error) {
      status.textContent = error.message;
    }
  });
}

function bindImageModal() {
  const modal = document.getElementById("imageModal");
  const closeButton = document.getElementById("imageModalClose");
  const backdrop = document.getElementById("imageModalBackdrop");

  closeButton.addEventListener("click", closeImageModal);
  backdrop.addEventListener("click", closeImageModal);

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      closeImageModal();
    }
  });

  modal.addEventListener("click", (event) => {
    if (event.target === modal) {
      closeImageModal();
    }
  });
}

function renderQuickActions() {
  const container = document.getElementById("quickActions");
  container.innerHTML = "";
  for (const action of quickActions) {
    const button = document.createElement("button");
    button.className = "chip";
    button.type = "button";
    button.textContent = action.label;
    button.addEventListener("click", () => {
      const input = document.getElementById("messageInput");
      input.value = action.prompt;
      input.focus();
    });
    container.appendChild(button);
  }
}

function connectEvents() {
  if (appState.eventSource) {
    appState.eventSource.close();
  }
  appState.eventSource = new EventSource(`/api/sessions/${appState.sessionId}/events`);
  appState.eventSource.addEventListener("session_updated", (event) => {
    syncSnapshot(JSON.parse(event.data));
    render();
  });
}

function render() {
  renderWorkspaceNavigator();
  renderStatusOverviewEntry();
  renderStatusOverviewModal();
  renderSessionMeta();
  renderConsoleInfoBar();
  renderSkillHub();
  renderObjectTree();
  renderChat();
  renderInspector();
  renderPlannerPreview();
}

function renderSidebarCard({ title, body, badge = "", open = true }) {
  return `
    <details class="sidebar-card"${open ? " open" : ""}>
      <summary class="sidebar-card-summary">
        <div class="sidebar-card-heading">
          <strong>${escapeHTML(title)}</strong>
        </div>
        <div class="sidebar-card-summary-right">
          ${badge ? `<span class="sidebar-card-badge">${badge}</span>` : ""}
          <span class="sidebar-card-chevron" aria-hidden="true"></span>
        </div>
      </summary>
      <div class="sidebar-card-body">
        ${body}
      </div>
    </details>
  `;
}

function buildSystemStatusMarkup() {
  const status = appState.systemStatus;
  if (!status) {
    return "<p class='muted'>系统状态暂不可用。</p>";
  }

  const pills = [
    statusPill(status.system_mode === "live" ? "ok" : "warn", `模式：${formatSystemMode(status.system_mode)}`),
    statusPill(status.planner_mode === "llm" ? "ok" : "warn", `规划器：${formatPlannerMode(status.planner_mode)}`),
    statusPill(status.llm_loaded ? "ok" : "muted", `模型：${status.llm_loaded ? "已加载" : "未加载"}`),
    statusPill(status.runtime_connected ? "ok" : "bad", `运行时：${status.runtime_connected ? "已连接" : "离线"}`),
    statusPill(status.real_h5ad_inspection ? "ok" : "muted", `h5ad 检查：${status.real_h5ad_inspection ? "真实" : "占位"}`),
    statusPill(status.real_analysis_execution ? "ok" : "warn", `分析执行：${status.real_analysis_execution ? "真实" : "占位"}`),
  ];

  const runtime = status.runtime || {};
  const environmentChecks = runtime.environment_checks || [];
  const notes = status.notes || [];
  const failingChecks = environmentChecks.filter((check) => !check.ok);

  const cards = [
      renderSidebarCard({
        title: "系统状态",
        badge: statusPill(status.system_mode === "live" ? "ok" : "warn", formatSystemMode(status.system_mode)),
        body: `
        <div class="status-pills">${pills.join("")}</div>
        <div class="status-detail-grid">
          <div class="kv"><span>运行模式</span><span>${escapeHTML(formatRuntimeMode(status.runtime_mode))}</span></div>
        </div>
        ${
          notes.length
            ? `<div class="status-notes">${notes.map((note) => `<p class="muted">${escapeHTML(note)}</p>`).join("")}</div>`
            : ""
        }
      `,
    }),
  ];

  if (status.executable_skills?.length) {
    cards.push(
      renderSidebarCard({
        title: "可执行技能",
        badge: statusPill("ok", `${status.executable_skills.length} 个`),
        body: `
          <p class="muted">点击技能按钮会把建议指令填入输入框。</p>
          <div class="loaded-skill-grid">
            ${status.executable_skills
              .map(
                (skillName) => `
                  <button
                    type="button"
                    class="loaded-skill-chip"
                    data-skill-name="${escapeAttribute(skillName)}"
                    data-skill-prompt="${escapeAttribute(promptForSkill(skillName))}"
                    title="${escapeAttribute(skillName)}"
                  >
                    <span class="loaded-skill-name">${escapeHTML(formatSkillName(skillName))}</span>
                    <span class="loaded-skill-id">${escapeHTML(skillName)}</span>
                  </button>
                `,
              )
              .join("")}
          </div>
        `,
      }),
    );
  }

  if (environmentChecks.length) {
    cards.push(
      renderSidebarCard({
        title: "环境检查",
        badge: failingChecks.length ? statusPill("bad", `失败 ${failingChecks.length}`) : statusPill("ok", "正常"),
        body: `
          <div class="status-detail-grid compact">
            <div class="kv"><span>Python</span><span>${escapeHTML(runtime.python_version || "未知")}</span></div>
          </div>
          ${
            failingChecks.length
              ? `
                <div class="status-check-failures">
                  ${failingChecks
                    .map(
                      (check) => `
                        <div class="status-check-failure">
                          <strong>${escapeHTML(check.name)}</strong>
                          <p class="muted">${escapeHTML(check.detail || "未知错误")}</p>
                        </div>
                      `,
                    )
                    .join("")}
                </div>
              `
              : `<p class="muted">运行时检查全部通过。</p>`
          }
        `,
      }),
    );
  }

  return cards.join("");
}

function renderStatusOverviewEntry() {
  const container = document.getElementById("statusOverviewEntry");
  const status = appState.systemStatus;
  if (!status) {
    container.innerHTML = `
      <div class="status-overview-entry disabled">
        <div class="status-overview-copy">
          <span class="status-overview-eyebrow">运行概览</span>
          <strong>系统状态、技能与环境</strong>
          <p class="muted">系统状态暂不可用。</p>
        </div>
      </div>
    `;
    return;
  }

  const runtime = status.runtime || {};
  const failingChecks = (runtime.environment_checks || []).filter((check) => !check.ok);
  const skillCount = status.executable_skills?.length || 0;

  container.innerHTML = `
    <button
      id="statusOverviewButton"
      type="button"
      class="status-overview-entry"
      aria-haspopup="dialog"
      aria-controls="statusOverviewModal"
    >
      <div class="status-overview-copy">
        <span class="status-overview-eyebrow">运行概览</span>
        <strong>系统状态、技能与环境</strong>
      </div>
      <div class="status-overview-meta">
        ${statusPill(status.system_mode === "live" ? "ok" : "warn", formatSystemMode(status.system_mode))}
        ${statusPill(status.runtime_connected ? "ok" : "bad", status.runtime_connected ? "运行时在线" : "运行时离线")}
        ${statusPill(
          failingChecks.length ? "bad" : "ok",
          failingChecks.length ? `环境失败 ${failingChecks.length}` : "环境正常",
        )}
        ${statusPill(skillCount ? "ok" : "muted", `技能 ${skillCount}`)}
      </div>
    </button>
  `;

  const button = document.getElementById("statusOverviewButton");
  button?.addEventListener("click", openStatusOverviewModal);
}

function renderStatusOverviewModal() {
  const content = document.getElementById("statusOverviewModalContent");
  if (!content) {
    return;
  }
  content.innerHTML = buildSystemStatusMarkup();
  bindLoadedSkillButtons(content);
}

function bindStatusOverviewModal() {
  const modal = document.getElementById("statusOverviewModal");
  const closeButton = document.getElementById("statusOverviewModalClose");
  const backdrop = document.getElementById("statusOverviewModalBackdrop");

  closeButton.addEventListener("click", closeStatusOverviewModal);
  backdrop.addEventListener("click", closeStatusOverviewModal);

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      closeStatusOverviewModal();
    }
  });

  modal.addEventListener("click", (event) => {
    if (event.target === modal) {
      closeStatusOverviewModal();
    }
  });
}

function renderWorkspaceNavigator() {
  const container = document.getElementById("workspaceNavigator");
  if (!container) {
    return;
  }

  const currentWorkspace = appState.workspaceSnapshot?.workspace || appState.snapshot?.workspace || null;
  const workspaceList = appState.workspaceList?.length
    ? appState.workspaceList
    : currentWorkspace
      ? [currentWorkspace]
      : [];

  container.innerHTML = `
    <div class="workspace-navigator-head">
      <div>
        <div class="workspace-meta-eyebrow">工作区</div>
        <div class="workspace-navigator-title">工作区列表</div>
      </div>
      <button
        id="newWorkspaceButton"
        type="button"
        class="ghost-button conversation-create-button"
        title="新建独立工作区，适合换数据集或开始全新分析"
      >新工作区</button>
    </div>
    ${
      workspaceList.length
        ? `
          <div class="workspace-list">
            ${workspaceList
              .map(
                (item) => `
                  <button
                    type="button"
                    class="workspace-chip ${item.id === currentWorkspace?.id ? "active" : ""}"
                    data-workspace-id="${escapeAttribute(item.id)}"
                  >
                    <span class="workspace-chip-label">${escapeHTML(item.label || item.id)}</span>
                    <span class="workspace-chip-id">${escapeHTML(item.id)}</span>
                  </button>
                `,
              )
              .join("")}
          </div>
        `
        : "<p class='muted'>当前还没有工作区。</p>"
    }
  `;

  bindWorkspaceNavigator(container);
}

function renderConsoleInfoBar() {
  const container = document.getElementById("consoleInfoBar");
  if (!container) {
    return;
  }

  if (!appState.snapshot) {
    container.innerHTML = "";
    return;
  }

  const { session, workspace } = appState.snapshot;
  const currentWorkspace = appState.workspaceSnapshot?.workspace || workspace;
  const datasetID = currentWorkspace?.dataset_id || session?.dataset_id || "未设置";

  container.innerHTML = `
    <div class="console-info-grid">
      <div class="workspace-identity-card">
        <span>Workspace</span>
        <strong>${escapeHTML(currentWorkspace?.id || "未设置")}</strong>
      </div>
      <div class="workspace-identity-card">
        <span>当前对话</span>
        <strong>${escapeHTML(session?.id || "未设置")}</strong>
      </div>
      <div class="workspace-identity-card">
        <span>数据集</span>
        <strong>${escapeHTML(datasetID)}</strong>
      </div>
    </div>
  `;
}

function renderSessionMeta() {
  const meta = document.getElementById("sessionMeta");
  if (!appState.snapshot) {
    meta.innerHTML = "<p class='muted'>尚未加载会话。</p>";
    return;
  }
  const { session, workspace, objects, jobs, artifacts } = appState.snapshot;
  const conversations = appState.workspaceSnapshot?.conversations || (session ? [session] : []);
  const currentWorkspace = appState.workspaceSnapshot?.workspace || workspace;
  const workspaceLabel = currentWorkspace?.label || "未命名 workspace";
  const workspaceStatus = appState.workspaceStatus ? `<p class="workspace-status muted">${escapeHTML(appState.workspaceStatus)}</p>` : "";
  const conversationMarkup = conversations.length
    ? `
      <div class="conversation-list">
        ${conversations
          .map(
            (conversation) => `
              <button
                type="button"
                class="conversation-chip ${conversation.id === session.id ? "active" : ""}"
                data-conversation-id="${escapeAttribute(conversation.id)}"
              >
                <span class="conversation-chip-label">${escapeHTML(formatConversationLabel(conversation))}</span>
                <span class="conversation-chip-id">${escapeHTML(conversation.id)}</span>
              </button>
            `,
          )
          .join("")}
      </div>
    `
    : "<p class='muted'>当前 workspace 还没有其他对话。</p>";

  meta.innerHTML = `
    <div class="workspace-meta-eyebrow">当前工作区</div>
    <div class="workspace-meta-head">
      <div>
        <div class="workspace-title-row">
          <div class="workspace-title">${escapeHTML(workspaceLabel)}</div>
          <details class="workspace-help-popover">
            <summary aria-label="查看工作区与对话说明">?</summary>
            <div class="workspace-help-body">
              <p><strong>新工作区</strong>：新建独立容器，适合换数据集或重新开始。</p>
              <p><strong>新对话</strong>：复用当前工作区对象与结果，只开启新线程。</p>
            </div>
          </details>
        </div>
      </div>
      <div class="workspace-meta-actions">
        <button
          id="newConversationButton"
          type="button"
          class="ghost-button conversation-create-button"
          title="保留当前工作区里的对象和结果，只开启新的聊天线程"
        >新对话</button>
    </div>
    </div>
    ${workspaceStatus}
    <div class="workspace-summary-grid">
      <div class="workspace-summary-item">
        <strong>${objects.length}</strong>
        <span>共享对象</span>
      </div>
      <div class="workspace-summary-item">
        <strong>${jobs.length}</strong>
        <span>本对话任务</span>
      </div>
      <div class="workspace-summary-item">
        <strong>${artifacts.length}</strong>
        <span>共享结果</span>
      </div>
    </div>
    <div class="workspace-section-label">对话</div>
    ${conversationMarkup}
  `;
  bindWorkspaceMeta(meta);
}

function bindWorkspaceNavigator(container) {
  const createWorkspaceButton = container.querySelector("#newWorkspaceButton");
  if (createWorkspaceButton) {
    createWorkspaceButton.addEventListener("click", async () => {
      await createWorkspace();
    });
  }

  for (const button of container.querySelectorAll("[data-workspace-id]")) {
    button.addEventListener("click", async () => {
      await switchWorkspace(button.dataset.workspaceId);
    });
  }
}

function bindWorkspaceMeta(container) {
  const createButton = container.querySelector("#newConversationButton");
  if (createButton) {
    createButton.addEventListener("click", async () => {
      await createConversation();
    });
  }

  for (const button of container.querySelectorAll("[data-conversation-id]")) {
    button.addEventListener("click", async () => {
      await switchConversation(button.dataset.conversationId);
    });
  }
}

function renderSkillHub() {
  const container = document.getElementById("skillHubEntry");
  const bundles = appState.plugins || [];
  const enabledBundles = bundles.filter((bundle) => bundle.enabled);
  container.innerHTML = renderSidebarCard({
    title: "Skill Hub",
    badge: statusPill(enabledBundles.length ? "ok" : "muted", `${enabledBundles.length}/${bundles.length || 0}`),
    open: false,
    body: `
      <p class="muted">插件安装、启停和内置技能开关都已移到独立的 Skill Hub 页面统一管理。</p>
      <div class="plugin-summary-grid">
        <div class="plugin-summary-item">
          <strong>${enabledBundles.length}</strong>
          <span>启用中的技能包</span>
        </div>
        <div class="plugin-summary-item">
          <strong>${appState.skills.length}</strong>
          <span>当前已加载技能</span>
        </div>
      </div>
      <div class="plugin-hub-actions">
        <a class="ghost-button button-link" href="/plugins.html">打开插件管理页</a>
      </div>
    `,
  });
}

function renderObjectTree() {
  const container = document.getElementById("objectTree");
  container.innerHTML = "";

  if (!appState.snapshot?.objects?.length) {
    container.innerHTML = "<p class='muted'>当前 workspace 还没有分析对象。</p>";
    return;
  }

  const objectsByParent = new Map();
  for (const object of appState.snapshot.objects) {
    const parentKey = object.parent_id || "root";
    if (!objectsByParent.has(parentKey)) {
      objectsByParent.set(parentKey, []);
    }
    objectsByParent.get(parentKey).push(object);
  }

  const walk = (parentId, depth) => {
    const children = objectsByParent.get(parentId) || [];
    for (const object of children) {
      const node = document.createElement("button");
      node.type = "button";
      node.className = `tree-node depth-${Math.min(depth, 3)} ${
        object.id === appState.activeObjectId ? "active" : ""
      }`;
      node.innerHTML = `
        <span class="label">${escapeHTML(object.label)}</span>
        <span class="meta">${escapeHTML(formatObjectKind(object.kind))} · ${escapeHTML(String(object.n_obs))} 个细胞 · ${escapeHTML(formatObjectState(object.state))}</span>
      `;
      node.addEventListener("click", () => {
        appState.activeObjectId = object.id;
        renderInspector();
        renderObjectTree();
      });
      container.appendChild(node);
      walk(object.id, depth + 1);
    }
  };

  walk("root", 0);
}

async function renderChat() {
  const container = document.getElementById("chat");
  const template = document.getElementById("messageTemplate");
  const renderVersion = ++appState.chatRenderVersion;
  const messages = appState.snapshot?.messages || [];
  const nodes = await Promise.all(messages.map((message) => buildMessageNode(message, template)));

  if (renderVersion !== appState.chatRenderVersion) {
    return;
  }

  container.innerHTML = "";
  for (const node of nodes) {
    container.appendChild(node);
  }
  bindArtifactPreviewButtons(container);
  container.scrollTop = container.scrollHeight;
}

async function buildMessageNode(message, template) {
  const node = template.content.firstElementChild.cloneNode(true);
  const content = node.querySelector(".message-content");
  node.classList.add(`message-${message.role}`);
  node.querySelector(".message-role").textContent = formatRole(message.role);

  const detailMarkup = await buildMessageDetailMarkup(message);
  if (String(message.content || "").trim()) {
    content.textContent = message.content;
  } else {
    content.remove();
  }
  if (detailMarkup) {
    const detail = document.createElement("div");
    detail.className = "message-detail";
    detail.innerHTML = detailMarkup;
    node.appendChild(detail);
  }

  return node;
}

async function buildMessageDetailMarkup(message) {
  if (!appState.snapshot) {
    return "";
  }

  if (message.role === "user") {
    const job = (appState.snapshot.jobs || []).find((item) => item.message_id === message.id);
    if (!job || (job.status !== "queued" && job.status !== "running")) {
      return "";
    }
    return buildJobStatusMarkup(job);
  }

  if (message.role === "assistant" && message.job_id) {
    const job = (appState.snapshot.jobs || []).find((item) => item.id === message.job_id);
    if (!job) {
      return "";
    }
    return buildJobResultMarkup(job, message);
  }

  return "";
}

function buildJobStatusMarkup(job) {
  return `
    <section class="message-job-card pending">
      <div class="message-job-head">
        <strong>任务状态</strong>
        ${statusPill(statusKindForJob(job.status), formatJobStatus(job.status))}
      </div>
      ${buildJobPhasesMarkup(job)}
      <p class="message-job-summary">${escapeHTML(job.summary || "请求已接收，等待规划器和运行时返回更新。")}</p>
      ${buildCheckpointMarkup(job)}
      ${buildPlanMarkup(job)}
      ${
        job.steps?.length
          ? `<div class="message-step-list">
              ${job.steps
                .map(
                  (step) => `
                    <div class="message-step-row">
                      <span>${escapeHTML(formatSkillName(step.skill))}</span>
                      <span>${escapeHTML(step.summary || formatJobStatus(step.status))}</span>
                    </div>
                  `,
                )
                .join("")}
            </div>`
          : ""
      }
    </section>
  `;
}

async function buildJobResultMarkup(job, assistantMessage) {
  const relatedArtifacts = (appState.snapshot?.artifacts || []).filter((artifact) => artifact.job_id === job.id);
  const artifactCards = await Promise.all(
    relatedArtifacts.map((artifact) => buildArtifactCardMarkup(artifact, "chat")),
  );
  const showSummary = shouldRenderJobSummary(job, assistantMessage?.content || "");
  const cardClass = job.status === "failed" ? "failed" : job.status === "incomplete" ? "incomplete" : "done";
  const detailMarkup = buildJobExecutionDetailsMarkup(job);

  return `
    <section class="message-job-card ${cardClass}">
      <div class="message-job-head">
        <strong>任务详情</strong>
        ${statusPill(statusKindForJob(job.status), formatJobStatus(job.status))}
      </div>
      ${buildJobPhasesMarkup(job)}
      ${
        showSummary && job.summary
          ? `<p class="message-job-summary">${escapeHTML(job.summary)}</p>`
          : ""
      }
      ${
        job.error
          ? `<p class="message-job-error">${escapeHTML(job.error)}</p>`
          : ""
      }
      ${
        artifactCards.length
          ? `<div class="message-artifact-group">
              <div class="message-artifact-head">
                <strong>结果文件</strong>
                <span class="muted">${artifactCards.length} 项</span>
              </div>
              ${artifactCards.join("")}
            </div>`
          : ""
      }
      ${detailMarkup}
    </section>
  `;
}

function buildJobPhasesMarkup(job) {
  const phases = job.phases || [];
  if (!phases.length) {
    return "";
  }

  return `
    <div class="message-phase-group">
      <div class="message-checkpoint-head">
        <strong>执行阶段</strong>
        <span class="muted">${escapeHTML(`${phases.length} 个`)}</span>
      </div>
      <div class="message-phase-list">
        ${phases
          .map((phase) => {
            const activeClass = phase.kind === job.current_phase ? " active" : "";
            return `
              <div class="message-phase-card${activeClass}">
                <div class="message-checkpoint-title">
                  <strong>${escapeHTML(phase.title || formatJobPhaseKind(phase.kind))}</strong>
                  ${statusPill(statusKindForPhase(phase.status), formatJobPhaseStatus(phase.status))}
                </div>
                ${
                  phase.summary
                    ? `<p class="muted">${escapeHTML(phase.summary)}</p>`
                    : ""
                }
              </div>
            `;
          })
          .join("")}
      </div>
    </div>
  `;
}

function buildCheckpointMarkup(job) {
  const checkpoints = job.checkpoints || [];
  if (!checkpoints.length) {
    return "";
  }

  return `
    <div class="message-checkpoint-group">
      <div class="message-checkpoint-head">
        <strong>执行检查点</strong>
        <span class="muted">${escapeHTML(`${checkpoints.length} 条`)}</span>
      </div>
      <div class="message-checkpoint-list">
        ${checkpoints
          .map(
            (checkpoint) => `
              <div class="message-checkpoint-card">
                <div class="message-checkpoint-title">
                  <strong>${escapeHTML(checkpoint.title || "检查点")}</strong>
                  ${statusPill(normalizeCheckpointTone(checkpoint.tone), checkpoint.label || "已记录")}
                </div>
                ${
                  checkpoint.summary
                    ? `<p class="muted">${escapeHTML(checkpoint.summary)}</p>`
                    : ""
                }
              </div>
            `,
          )
          .join("")}
      </div>
    </div>
  `;
}

function formatJobPhaseStatus(status) {
  return translateLabel(status, jobPhaseStatusLabels, status || "未知");
}

function formatJobPhaseKind(kind) {
  switch (kind) {
    case "decide":
      return "快速判断";
    case "investigate":
      return "信息收集";
    case "respond":
      return "确认与回答";
    default:
      return kind || "阶段";
  }
}

function statusKindForPhase(status) {
  switch (status) {
    case "completed":
      return "ok";
    case "running":
      return "warn";
    case "failed":
      return "error";
    case "skipped":
      return "muted";
    default:
      return "muted";
  }
}

function shouldRenderJobSummary(job, assistantContent = "") {
  if (!job.summary) {
    return false;
  }

  if (job.summary.trim() === String(assistantContent || "").trim()) {
    return false;
  }

  const steps = job.steps || [];
  if (steps.length !== 1) {
    return true;
  }

  const stepSummary = (steps[0]?.summary || "").trim();
  if (!stepSummary) {
    return true;
  }

  return job.summary.trim() !== stepSummary;
}

function buildJobExecutionDetailsMarkup(job) {
  const checkpointsMarkup = buildCheckpointMarkup(job);
  const planMarkup = buildPlanMarkup(job);
  const stepMarkup =
    job.steps?.length
      ? `<div class="message-step-list">
          ${job.steps
            .map(
              (step) => `
                <div class="message-step-card">
                  <div class="message-step-head">
                    <strong>${escapeHTML(formatSkillName(step.skill))}</strong>
                    ${statusPill(statusKindForJob(step.status), formatJobStatus(step.status))}
                  </div>
                  <p class="muted">${escapeHTML(step.summary || "未返回摘要。")}</p>
                  ${
                    step.output_object_id
                      ? `<div class="message-step-meta">输出对象：${escapeHTML(objectLabel(step.output_object_id))}</div>`
                      : ""
                  }
                </div>
              `,
            )
            .join("")}
        </div>`
      : "";

  if (!checkpointsMarkup && !planMarkup && !stepMarkup) {
    return "";
  }

  return `
    <details class="message-plan-details message-job-details">
      <summary>
        <span>查看任务详情</span>
        <span class="message-plan-summary">计划与执行记录</span>
      </summary>
      <div class="message-job-extra">
        ${checkpointsMarkup}
        ${planMarkup}
        ${stepMarkup}
      </div>
    </details>
  `;
}

function buildPlanMarkup(job) {
  const steps = job.plan?.steps || [];
  if (!steps.length) {
    return "";
  }

  return `
    <details class="message-plan-details">
      <summary>
        <span>执行计划</span>
        <span class="message-plan-summary">${escapeHTML(`${steps.length} 步`)}</span>
      </summary>
      <div class="message-plan-list">
        ${steps.map((step, index) => buildPlanStepMarkup(step, index)).join("")}
      </div>
    </details>
  `;
}

function buildPlanStepMarkup(step, index) {
  const params = step.params && Object.keys(step.params).length
    ? `<pre>${escapeHTML(JSON.stringify(step.params, null, 2))}</pre>`
    : "<p class='muted'>无额外参数。</p>";
  const memoryRefs = step.memory_refs?.length
    ? `<div class="kv"><span>记忆引用</span><span>${escapeHTML(step.memory_refs.join("、"))}</span></div>`
    : "";

  return `
    <details class="message-plan-step">
      <summary>
        <span>第 ${index + 1} 步 · ${escapeHTML(formatSkillName(step.skill))}</span>
        <span class="message-plan-step-target">${escapeHTML(formatPlanTarget(step.target_object_id))}</span>
      </summary>
      <div class="message-plan-step-body">
        <div class="kv"><span>技能</span><span>${escapeHTML(step.skill || "未知")}</span></div>
        <div class="kv"><span>目标</span><span>${escapeHTML(formatPlanTarget(step.target_object_id))}</span></div>
        ${memoryRefs}
        ${params}
      </div>
    </details>
  `;
}

function renderInspector() {
  const container = document.getElementById("inspector");
  const object = activeObject();
  const blocks = [
    renderSidebarCard({
      title: "Working Memory",
      open: true,
      body: renderWorkingMemoryMarkup(appState.snapshot?.working_memory),
    }),
  ];

  if (!object) {
    blocks.push(
      renderSidebarCard({
        title: "当前对象",
        body: "<p class='muted'>请选择一个对象查看详情。</p>",
      }),
    );
    container.innerHTML = blocks.join("");
    return;
  }

  const relatedJobs = (appState.snapshot?.jobs || []).filter((job) =>
    (job.steps || []).some(
      (step) =>
        step.output_object_id === object.id || step.resolved_target_object_id === object.id,
    ),
  );

  const metadata = object.metadata || {};
  const assessment = metadata.assessment || {};
  const cellType = metadata.cell_type_annotation;
  const cluster = metadata.cluster_annotation;
  const categorical = metadata.categorical_obs_fields || [];

  blocks.push(
    renderSidebarCard({
      title: object.label,
      body: `
        <div class="kv"><span>对象 ID</span><span>${escapeHTML(object.id)}</span></div>
        <div class="kv"><span>类型</span><span>${escapeHTML(formatObjectKind(object.kind))}</span></div>
        <div class="kv"><span>父对象</span><span>${escapeHTML(object.parent_id || "无")}</span></div>
        <div class="kv"><span>后端引用</span><span>${escapeHTML(object.backend_ref)}</span></div>
        <div class="kv"><span>细胞数</span><span>${escapeHTML(String(object.n_obs))}</span></div>
        <div class="kv"><span>基因数</span><span>${escapeHTML(String(object.n_vars))}</span></div>
        <div class="kv"><span>状态</span><span>${escapeHTML(formatObjectState(object.state))}</span></div>
        <div class="kv"><span>落盘文件</span><span>${escapeHTML(object.materialized_path || "尚未生成")}</span></div>
        <div class="kv"><span>下载</span><span>${
          object.materialized_url
            ? `<a class="inline-link" href="${object.materialized_url}" download>获取 h5ad</a>`
            : "暂不可用"
        }</span></div>
      `,
    }),
  );

  blocks.push(
    renderSidebarCard({
      title: "数据集评估",
      body: `
        <div class="kv"><span>状态</span><span>${escapeHTML(formatAnalysisState(assessment.preprocessing_state))}</span></div>
        <div class="kv"><span>矩阵层</span><span>${escapeHTML(formatList(metadata.layer_keys))}</span></div>
        <div class="kv"><span>Obs 字段</span><span>${escapeHTML(formatList(metadata.obs_fields))}</span></div>
        <div class="kv"><span>Var 字段</span><span>${escapeHTML(formatList(metadata.var_fields))}</span></div>
        <div class="kv"><span>嵌入</span><span>${escapeHTML(formatList(metadata.obsm_keys))}</span></div>
        <div class="kv"><span>Uns 键</span><span>${escapeHTML(formatList(metadata.uns_keys))}</span></div>
        <div class="kv"><span>细胞类型</span><span>${escapeHTML(formatAnnotation(cellType))}</span></div>
        <div class="kv"><span>聚类</span><span>${escapeHTML(formatAnnotation(cluster))}</span></div>
        <div class="kv"><span>可执行分析</span><span>${escapeHTML(formatSkillList(assessment.available_analyses))}</span></div>
        <div class="kv"><span>缺失条件</span><span>${escapeHTML(formatList(assessment.missing_requirements))}</span></div>
        <div class="kv"><span>建议下一步</span><span>${escapeHTML(formatList(assessment.suggested_next_steps))}</span></div>
      `,
    }),
  );

  blocks.push(
    renderSidebarCard({
      title: "注释候选字段",
      body: categorical.length
        ? categorical
            .slice(0, 8)
            .map(
              (item) => `
                <div class="kv">
                  <span>${escapeHTML(item.field)}</span>
                  <span>${escapeHTML(`${formatAnnotationRole(item.role)} · ${item.n_categories} 组 · ${(item.sample_values || []).join("、")}`)}</span>
                </div>
              `,
            )
            .join("")
        : "<p class='muted'>暂未发现分类 obs 字段。</p>",
    }),
  );

  blocks.push(
    renderSidebarCard({
      title: "最近任务",
      body: relatedJobs.length
        ? relatedJobs
            .slice(-3)
            .reverse()
            .map(
              (job) => `
                <div class="kv"><span>${escapeHTML(formatJobStatus(job.status))}</span><span>${escapeHTML(job.summary || "等待中...")}</span></div>
              `,
            )
            .join("")
        : "<p class='muted'>这个对象还没有关联任务。</p>",
    }),
  );

  container.innerHTML = blocks.join("");
}

function renderPlannerPreview() {
  const container = document.getElementById("plannerPreview");
  const preview = appState.plannerPreview;
  if (!preview) {
    container.innerHTML = "";
    return;
  }

  const blocks = [];
  blocks.push(
    renderSidebarCard({
      title: "规划预览",
      body: `
        <div class="kv"><span>模式</span><span>${escapeHTML(formatPlannerMode(preview.planner_mode))}</span></div>
        <div class="kv"><span>当前对象</span><span>${escapeHTML(preview.planning_request?.active_object?.label || "无")}</span></div>
        <div class="kv"><span>说明</span><span>${escapeHTML(preview.note || "无")}</span></div>
      `,
    }),
  );

  blocks.push(
    renderSidebarCard({
      title: "Working Memory",
      open: false,
      body: renderWorkingMemoryMarkup(preview.planning_request?.working_memory),
    }),
  );

  blocks.push(
    renderSidebarCard({
      title: "规划请求",
      body: `<pre>${escapeHTML(JSON.stringify(preview.planning_request, null, 2))}</pre>`,
    }),
  );

  if (preview.developer_instructions) {
    blocks.push(
      renderSidebarCard({
        title: "开发者指令",
        body: `<pre>${escapeHTML(preview.developer_instructions)}</pre>`,
      }),
    );
  }

  if (preview.request_body) {
    blocks.push(
      renderSidebarCard({
        title: "规划器请求体",
        body: `<pre>${escapeHTML(JSON.stringify(preview.request_body, null, 2))}</pre>`,
      }),
    );
  }

  container.innerHTML = blocks.join("");
}

function renderWorkingMemoryMarkup(memory) {
  if (!memory) {
    return "<p class='muted'>当前还没有 working memory。</p>";
  }

  const focus = memory.focus;
  const recentArtifacts = memory.recent_artifacts || [];
  const confirmedPreferences = memory.confirmed_preferences || [];
  const stateChanges = memory.semantic_state_changes || [];

  const sections = [];
  sections.push(`
    <div class="workspace-section-label">当前焦点</div>
    ${
      focus
        ? `
          <div class="kv"><span>当前对象</span><span>${escapeHTML(focus.active_object_label || focus.active_object_id || "无")}</span></div>
          <div class="kv"><span>最近输出对象</span><span>${escapeHTML(focus.last_output_object_label || focus.last_output_object_id || "无")}</span></div>
          <div class="kv"><span>最近结果</span><span>${escapeHTML(focus.last_artifact_title || focus.last_artifact_id || "无")}</span></div>
        `
        : "<p class='muted'>暂无焦点信息。</p>"
    }
  `);

  sections.push(`
    <div class="workspace-section-label">确认偏好</div>
    ${
      confirmedPreferences.length
        ? confirmedPreferences
            .map(
              (item) => `
                <div class="kv">
                  <span>${escapeHTML(`${item.skill}.${item.param}`)}</span>
                  <span>${escapeHTML(formatMemoryValue(item.value))}</span>
                </div>
              `,
            )
            .join("")
        : "<p class='muted'>暂无确认偏好。</p>"
    }
  `);

  sections.push(`
    <div class="workspace-section-label">最近结果引用</div>
    ${
      recentArtifacts.length
        ? recentArtifacts
            .map(
              (artifact) => `
                <div class="kv">
                  <span>${escapeHTML(artifact.kind || "artifact")}</span>
                  <span>${escapeHTML(artifact.title || artifact.id || "未命名结果")}</span>
                </div>
              `,
            )
            .join("")
        : "<p class='muted'>暂无最近结果引用。</p>"
    }
  `);

  sections.push(`
    <div class="workspace-section-label">语义状态变更</div>
    ${
      stateChanges.length
        ? stateChanges
            .map(
              (change) => `
                <div class="kv">
                  <span>${escapeHTML(change.kind || "change")}</span>
                  <span>${escapeHTML(change.summary || change.object_label || change.artifact_title || change.skill || "已记录")}</span>
                </div>
              `,
            )
            .join("")
        : "<p class='muted'>暂无语义状态变更。</p>"
    }
  `);

  return sections.join("");
}

async function buildArtifactCardMarkup(artifact, variant = "chat") {
  let body = `<p class="muted">${escapeHTML(artifact.summary || artifact.content_type || "")}</p>`;
  if (artifact.kind === "plot") {
    body += `
      <button
        type="button"
        class="artifact-preview-button"
        data-artifact-url="${escapeAttribute(artifact.url)}"
        data-artifact-title="${escapeAttribute(artifact.title)}"
      >
        <img src="${artifact.url}" alt="${escapeAttribute(artifact.title)}" />
      </button>
    `;
  } else if (artifact.kind === "table" || artifact.kind === "file") {
    const text = await getArtifactTextPreview(artifact);
    body += `<pre>${escapeHTML(text.slice(0, 1200))}</pre>`;
  }

  return `
    <section class="artifact-card artifact-card-${variant}">
      <div class="artifact-head">
        <h3>${escapeHTML(artifact.title)}</h3>
        <div class="artifact-actions">
          <a class="inline-link" href="${artifact.url}" target="_blank" rel="noreferrer">打开</a>
          <a class="inline-link" href="${artifact.url}" download>下载</a>
        </div>
      </div>
      ${body}
    </section>
  `;
}

async function getArtifactTextPreview(artifact) {
  if (appState.artifactTextCache.has(artifact.id)) {
    return appState.artifactTextCache.get(artifact.id);
  }

  try {
    const response = await fetch(artifact.url);
    const text = await response.text();
    appState.artifactTextCache.set(artifact.id, text);
    return text;
  } catch (error) {
    const fallback = `无法加载预览：${error.message}`;
    appState.artifactTextCache.set(artifact.id, fallback);
    return fallback;
  }
}

function bindArtifactPreviewButtons(container) {
  for (const button of container.querySelectorAll(".artifact-preview-button")) {
    button.addEventListener("click", () => {
      openImageModal(button.dataset.artifactUrl, button.dataset.artifactTitle);
    });
  }
}

function bindLoadedSkillButtons(container) {
  for (const button of container.querySelectorAll(".loaded-skill-chip")) {
    button.addEventListener("click", () => {
      const input = document.getElementById("messageInput");
      input.value = button.dataset.skillPrompt || button.dataset.skillName || "";
      closeStatusOverviewModal();
      input.focus();
    });
  }
}

function activeObject() {
  return (appState.snapshot?.objects || []).find((object) => object.id === appState.activeObjectId);
}

async function fetchJSON(url, options) {
  const response = await fetch(url, options);
  const contentType = response.headers.get("Content-Type") || "";

  if (!response.ok) {
    let message = "";
    if (contentType.includes("application/json")) {
      const payload = await response.json().catch(() => null);
      message = payload?.error || payload?.message || "";
    } else {
      message = await response.text();
    }
    throw new Error(message || `请求失败：${response.status}`);
  }

  if (contentType.includes("application/json")) {
    return response.json();
  }
  return null;
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function escapeAttribute(value) {
  return escapeHTML(value).replaceAll('"', "&quot;");
}

function translateLabel(value, labels, fallback = "未知") {
  if (value === null || value === undefined || value === "") {
    return fallback;
  }
  return labels[value] || String(value);
}

function formatList(values) {
  if (!values || !values.length) {
    return "无";
  }
  return values.join("、");
}

function formatMemoryValue(value) {
  if (value === null || value === undefined || value === "") {
    return "无";
  }
  if (Array.isArray(value)) {
    return value.join("、");
  }
  if (typeof value === "object") {
    return JSON.stringify(value);
  }
  return String(value);
}

function formatSkillList(values) {
  if (!values || !values.length) {
    return "无";
  }
  return values.map((value) => formatSkillName(value)).join("、");
}

function objectLabel(objectId) {
  const object = (appState.snapshot?.objects || []).find((item) => item.id === objectId);
  return object ? object.label : objectId;
}

function formatPlanTarget(targetObjectId) {
  if (!targetObjectId || targetObjectId === "$active") {
    return "当前对象";
  }
  if (targetObjectId === "$prev") {
    return "上一步输出";
  }
  return objectLabel(targetObjectId);
}

function formatRole(role) {
  return roleLabels[role] || role || "未知";
}

function formatConversationLabel(session) {
  if (!session) {
    return "未命名对话";
  }
  return session.label || session.id || "未命名对话";
}

function formatSkillName(skill) {
  return translateLabel(skill, skillLabels, skill || "未知技能");
}

function promptForSkill(skill) {
  return skillPrompts[skill] || skill || "";
}

function formatJobStatus(status) {
  return translateLabel(status, jobStatusLabels, status || "未知");
}

function formatSystemMode(mode) {
  return translateLabel(mode, systemModeLabels, mode || "未知");
}

function formatPlannerMode(mode) {
  return translateLabel(mode, plannerModeLabels, mode || "未知");
}

function formatRuntimeMode(mode) {
  return translateLabel(mode, runtimeModeLabels, mode || "未知");
}

function formatObjectKind(kind) {
  return translateLabel(kind, objectKindLabels, kind || "未知类型");
}

function formatObjectState(state) {
  return translateLabel(state, objectStateLabels, state || "未知");
}

function formatAnnotationRole(role) {
  return translateLabel(role, annotationRoleLabels, role || "注释");
}

function formatAnalysisState(state) {
  return translateLabel(state, analysisStateLabels, state || "未知");
}

function formatAnnotation(annotation) {
  if (!annotation) {
    return "未识别";
  }
  const sample = (annotation.sample_values || []).slice(0, 4).join("、");
  return `${annotation.field} · ${annotation.n_categories} 组${sample ? ` · ${sample}` : ""}`;
}

function statusPill(kind, label) {
  return `<span class="status-pill ${kind}">${escapeHTML(label)}</span>`;
}

function statusKindForJob(status) {
  switch (status) {
    case "succeeded":
      return "ok";
    case "incomplete":
      return "warn";
    case "failed":
      return "bad";
    case "running":
      return "warn";
    default:
      return "muted";
  }
}

function normalizeCheckpointTone(tone) {
  switch (tone) {
    case "ok":
    case "warn":
    case "bad":
    case "muted":
      return tone;
    default:
      return "muted";
  }
}

function openImageModal(url, title) {
  const modal = document.getElementById("imageModal");
  const titleNode = document.getElementById("imageModalTitle");
  const image = document.getElementById("imageModalImage");
  const openLink = document.getElementById("imageModalOpen");
  const downloadLink = document.getElementById("imageModalDownload");

  titleNode.textContent = title || "结果预览";
  image.src = url;
  image.alt = title || "结果预览";
  openLink.href = url;
  downloadLink.href = url;
  downloadLink.setAttribute("download", "");
  modal.classList.remove("hidden");
  modal.setAttribute("aria-hidden", "false");
}

function closeImageModal() {
  const modal = document.getElementById("imageModal");
  const image = document.getElementById("imageModalImage");
  if (modal.classList.contains("hidden")) {
    return;
  }
  modal.classList.add("hidden");
  modal.setAttribute("aria-hidden", "true");
  image.removeAttribute("src");
}

function openStatusOverviewModal() {
  const modal = document.getElementById("statusOverviewModal");
  modal.classList.remove("hidden");
  modal.setAttribute("aria-hidden", "false");
}

function closeStatusOverviewModal() {
  const modal = document.getElementById("statusOverviewModal");
  if (modal.classList.contains("hidden")) {
    return;
  }
  modal.classList.add("hidden");
  modal.setAttribute("aria-hidden", "true");
}
