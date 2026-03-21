const appState = {
  sessionId: null,
  activeObjectId: null,
  snapshot: null,
  skills: [],
  systemStatus: null,
  eventSource: null,
  plannerPreview: null,
  chatRenderVersion: 0,
  artifactTextCache: new Map(),
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
  recluster: "重新聚类",
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
  recluster: "对当前对象重新聚类",
  find_markers: "查找当前对象的 marker 基因",
  plot_umap: "绘制当前对象的 UMAP 图",
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
  succeeded: "成功",
  failed: "失败",
  canceled: "已取消",
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
  bindComposer();
  bindUpload();
  bindPlannerPreview();
  bindImageModal();
  renderQuickActions();
  await bootstrap();
});

async function bootstrap() {
  appState.systemStatus = await fetchJSON("/api/status");
  const skillsResponse = await fetchJSON("/api/skills");
  appState.skills = skillsResponse.skills || [];

  const snapshot = await fetchJSON("/api/sessions", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ label: "拟南芥图谱会话" }),
  });
  appState.sessionId = snapshot.session.id;
  appState.snapshot = snapshot;
  appState.activeObjectId = snapshot.session.active_object_id;
  connectEvents();
  render();
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
  appState.snapshot = response.snapshot;
  appState.activeObjectId = response.snapshot.session.active_object_id;
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
      appState.snapshot = response.snapshot;
      appState.activeObjectId = response.object.id;
      status.textContent = `${file.name} 已作为 ${response.object.label} 附加到当前会话。`;
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
    appState.snapshot = JSON.parse(event.data);
    appState.activeObjectId = appState.snapshot.session.active_object_id;
    render();
  });
}

function render() {
  renderSystemStatus();
  renderSessionMeta();
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

function renderSystemStatus() {
  const container = document.getElementById("systemStatus");
  const status = appState.systemStatus;
  if (!status) {
    container.innerHTML = "<p class='muted'>系统状态暂不可用。</p>";
    return;
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
        <p class="muted">${escapeHTML(status.summary || "")}</p>
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

  container.innerHTML = cards.join("");
  bindLoadedSkillButtons(container);
}

function renderSessionMeta() {
  const meta = document.getElementById("sessionMeta");
  if (!appState.snapshot) {
    meta.innerHTML = "<p class='muted'>尚未加载会话。</p>";
    return;
  }
  const { session, objects, jobs, artifacts } = appState.snapshot;
  meta.innerHTML = `
    <div class="kv"><span>会话</span><span>${session.id}</span></div>
    <div class="kv"><span>数据集</span><span>${escapeHTML(session.dataset_id || "未设置")}</span></div>
    <div class="kv"><span>对象</span><span>${objects.length}</span></div>
    <div class="kv"><span>任务</span><span>${jobs.length}</span></div>
    <div class="kv"><span>结果</span><span>${artifacts.length}</span></div>
  `;
}

function renderObjectTree() {
  const container = document.getElementById("objectTree");
  container.innerHTML = "";

  if (!appState.snapshot?.objects?.length) {
    container.innerHTML = "<p class='muted'>当前还没有对象。</p>";
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
  node.classList.add(`message-${message.role}`);
  node.querySelector(".message-role").textContent = formatRole(message.role);
  node.querySelector(".message-content").textContent = message.content;

  const detailMarkup = await buildMessageDetailMarkup(message);
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
    return buildJobResultMarkup(job);
  }

  return "";
}

function buildJobStatusMarkup(job) {
  return `
    <section class="message-job-card pending">
      <div class="message-job-head">
        <strong>任务状态</strong>
        ${statusPill(job.status === "running" ? "warn" : "muted", formatJobStatus(job.status))}
      </div>
      <p class="message-job-summary">${escapeHTML(job.summary || "请求已接收，等待规划器和运行时返回更新。")}</p>
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

async function buildJobResultMarkup(job) {
  const relatedArtifacts = (appState.snapshot?.artifacts || []).filter((artifact) => artifact.job_id === job.id);
  const artifactCards = await Promise.all(
    relatedArtifacts.map((artifact) => buildArtifactCardMarkup(artifact, "chat")),
  );

  return `
    <section class="message-job-card ${job.status === "failed" ? "failed" : "done"}">
      <div class="message-job-head">
        <strong>${job.status === "failed" ? "任务结果" : "分析结果"}</strong>
        ${statusPill(statusKindForJob(job.status), formatJobStatus(job.status))}
      </div>
      ${
        job.summary
          ? `<p class="message-job-summary">${escapeHTML(job.summary)}</p>`
          : ""
      }
      ${
        job.error
          ? `<p class="message-job-error">${escapeHTML(job.error)}</p>`
          : ""
      }
      ${
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
    </section>
  `;
}

function renderInspector() {
  const container = document.getElementById("inspector");
  const object = activeObject();
  if (!object) {
    container.innerHTML = "<p class='muted'>请选择一个对象查看详情。</p>";
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

  container.innerHTML = [
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
  ].join("");
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

function formatRole(role) {
  return roleLabels[role] || role || "未知";
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
    case "failed":
      return "bad";
    case "running":
      return "warn";
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
