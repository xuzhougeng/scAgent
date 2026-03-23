import { appState, quickActions } from "./state.mjs";
import {
  escapeAttribute,
  escapeHTML,
  formatAnalysisState,
  formatAnnotation,
  formatAnnotationRole,
  formatConversationLabel,
  formatJobPhaseKind,
  formatJobPhaseStatus,
  formatJobStatus,
  formatList,
  formatMemoryValue,
  formatObjectKind,
  formatObjectState,
  formatPlanTarget,
  formatPlannerMode,
  formatRole,
  formatRuntimeMode,
  formatSkillList,
  formatSkillName,
  formatSystemMode,
  normalizeCheckpointTone,
  objectLabel,
  promptForSkill,
  statusKindForJob,
  statusKindForPhase,
  statusPill,
} from "./format.mjs";
import {
  closeStatusOverviewModal,
  openImageModal,
  openStatusOverviewModal,
} from "./modals.mjs";

const renderActions = {
  async createConversation() {},
  async createWorkspace() {},
  async switchConversation() {},
  async switchWorkspace() {},
  async editJobRequest() {},
  async retryJob() {},
  async regenerateResponse() {},
  async renameWorkspace() {},
  async renameConversation() {},
};

const artifactTablePreviewOptions = {
  maxTableRows: 12,
  maxTableCols: 8,
};

export function configureRenderActions(actions) {
  Object.assign(renderActions, actions);
}

export function renderQuickActions() {
  const container = document.getElementById("quickActions");
  if (!container) {
    return;
  }

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

export function render() {
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

export function renderSessionMeta() {
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
          <div class="workspace-title" data-workspace-id="${escapeAttribute(currentWorkspace?.id || "")}" title="双击重命名">${escapeHTML(workspaceLabel)}</div>
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

export function renderPlannerPreview() {
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
    status.planner_mode === "llm"
      ? statusPill(
          status.planner_reachable ? "ok" : "bad",
          `规划连通：${status.planner_reachable ? "正常" : "异常"}`,
        )
      : statusPill("muted", "规划连通：规则模式"),
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
        ${
          status.planner_mode === "llm"
            ? statusPill(status.planner_reachable ? "ok" : "bad", status.planner_reachable ? "规划器在线" : "规划器异常")
            : statusPill("muted", "规则规划")
        }
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

function bindWorkspaceNavigator(container) {
  const createWorkspaceButton = container.querySelector("#newWorkspaceButton");
  if (createWorkspaceButton) {
    createWorkspaceButton.addEventListener("click", async () => {
      await renderActions.createWorkspace();
    });
  }

  for (const button of container.querySelectorAll("[data-workspace-id]")) {
    button.addEventListener("click", async () => {
      await renderActions.switchWorkspace(button.dataset.workspaceId);
    });
    const label = button.querySelector(".workspace-chip-label");
    if (label) {
      label.addEventListener("dblclick", (event) => {
        event.stopPropagation();
        event.preventDefault();
        startInlineEdit(label, label.textContent.trim(), async (newLabel) => {
          await renderActions.renameWorkspace(button.dataset.workspaceId, newLabel);
        });
      });
    }
  }
}

function bindWorkspaceMeta(container) {
  const createButton = container.querySelector("#newConversationButton");
  if (createButton) {
    createButton.addEventListener("click", async () => {
      await renderActions.createConversation();
    });
  }

  const workspaceTitle = container.querySelector(".workspace-title[data-workspace-id]");
  if (workspaceTitle) {
    workspaceTitle.addEventListener("dblclick", (event) => {
      event.stopPropagation();
      startInlineEdit(workspaceTitle, workspaceTitle.textContent.trim(), async (newLabel) => {
        await renderActions.renameWorkspace(workspaceTitle.dataset.workspaceId, newLabel);
      });
    });
  }

  for (const button of container.querySelectorAll("[data-conversation-id]")) {
    button.addEventListener("click", async () => {
      await renderActions.switchConversation(button.dataset.conversationId);
    });
    const label = button.querySelector(".conversation-chip-label");
    if (label) {
      label.addEventListener("dblclick", (event) => {
        event.stopPropagation();
        event.preventDefault();
        startInlineEdit(label, label.textContent.trim(), async (newLabel) => {
          await renderActions.renameConversation(button.dataset.conversationId, newLabel);
        });
      });
    }
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
  bindEditJobButtons(container);
  bindRetryButtons(container);
  bindRegenerateButtons(container);
  container.scrollTop = container.scrollHeight;
}

async function buildMessageNode(message, template) {
  const node = template.content.firstElementChild.cloneNode(true);
  const content = node.querySelector(".message-content");
  node.classList.add(`message-${message.role}`);
  if (message.local_status === "sending") {
    node.classList.add("message-pending");
  }
  node.querySelector(".message-role").textContent =
    message.local_status === "sending"
      ? `${formatRole(message.role)} · 发送中`
      : formatRole(message.role);

  const detailMarkup = await buildMessageDetailMarkup(message);
  if (detailMarkup) {
    const detail = document.createElement("div");
    detail.className = "message-detail";
    detail.innerHTML = detailMarkup;
    if (message.role === "assistant" && message.job_id) {
      detail.classList.add("message-detail-primary");
      node.insertBefore(detail, content);
    } else {
      node.appendChild(detail);
    }
  }
  if (String(message.content || "").trim()) {
    content.textContent = message.content;
    if (message.role === "assistant" && message.job_id && detailMarkup) {
      content.classList.add("message-content-secondary");
    }
  } else {
    content.remove();
  }

  if (message.role === "assistant" && message.job_id) {
    const job = (appState.snapshot?.jobs || []).find((item) => item.id === message.job_id);
    if (job && job.status !== "queued" && job.status !== "running") {
      const actions = document.createElement("div");
      actions.className = "message-actions";
      actions.innerHTML = buildMessageActionsMarkup(job);
      node.appendChild(actions);
    }
  }

  return node;
}

function buildMessageActionsMarkup(job) {
  if (!job || job.status === "queued" || job.status === "running") {
    return "";
  }

  const disabled = hasActiveJob() ? " disabled" : "";
  const buttons = [];
  if (job.status === "failed" || job.status === "incomplete" || job.status === "canceled") {
    buttons.push(
      `<button type="button" class="message-action-button retry-job-button" data-job-id="${escapeAttribute(job.id)}"${disabled}>重试</button>`,
    );
  }
  buttons.push(
    `<button type="button" class="message-action-button edit-job-button" data-job-id="${escapeAttribute(job.id)}"${disabled}>编辑并重发</button>`,
  );
  if (job.status === "succeeded" || job.status === "incomplete") {
    buttons.push(
      `<button type="button" class="message-action-button regenerate-button" data-job-id="${escapeAttribute(job.id)}"${disabled}>重新生成</button>`,
    );
  }
  return buttons.join("");
}

function hasActiveJob() {
  const jobs = appState.snapshot?.jobs || [];
  return jobs.some((job) => job && (job.status === "queued" || job.status === "running"));
}

function parseDelimitedTableBlock(text, formatHint = "") {
  const lines = splitNonEmptyLines(text);
  if (lines.length < 2) {
    return null;
  }

  const candidates =
    formatHint === "csv"
      ? [","]
      : formatHint === "tsv"
        ? ["\t"]
        : ["\t", ","];

  for (const delimiter of candidates) {
    const rows = lines.map((line) => parseDelimitedLine(line, delimiter));
    if (rows[0].length < 2) {
      continue;
    }
    if (!rows.every((row) => row.length === rows[0].length && rowHasContent(row))) {
      continue;
    }
    return {
      headers: rows[0],
      rows: rows.slice(1),
    };
  }
  return null;
}

function rowHasContent(row) {
  return row.some((cell) => String(cell || "").trim() !== "");
}

function parseDelimitedLine(line, delimiter) {
  const value = String(line || "").trim();
  const cells = [];
  let current = "";
  let inQuotes = false;

  for (let index = 0; index < value.length; index += 1) {
    const char = value[index];
    if (char === '"') {
      if (inQuotes && value[index + 1] === '"') {
        current += '"';
        index += 1;
      } else {
        inQuotes = !inQuotes;
      }
      continue;
    }
    if (char === delimiter && !inQuotes) {
      cells.push(current.trim());
      current = "";
      continue;
    }
    current += char;
  }

  cells.push(current.trim());
  return cells;
}

function splitNonEmptyLines(text) {
  return String(text || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
}

function renderStructuredTableMarkup(table, options) {
  const totalCols = Math.max(table.headers.length, ...table.rows.map((row) => row.length), 0);
  const totalRows = table.rows.length;
  const visibleCols = Math.min(totalCols, options.maxTableCols || totalCols);
  const visibleRows = Math.min(totalRows, options.maxTableRows || totalRows);
  const headers = normalizeTableRow(table.headers, totalCols).slice(0, visibleCols);
  const rows = table.rows
    .slice(0, visibleRows)
    .map((row) => normalizeTableRow(row, totalCols).slice(0, visibleCols));

  const truncation = [];
  if (totalRows > visibleRows) {
    truncation.push(`前 ${visibleRows} 行 / 共 ${totalRows} 行`);
  }
  if (totalCols > visibleCols) {
    truncation.push(`前 ${visibleCols} 列 / 共 ${totalCols} 列`);
  }

  return `
    <div class="message-table-block">
      <div class="message-table-wrap">
        <table class="message-table">
          <thead>
            <tr>${headers.map((cell) => `<th>${escapeHTML(cell || "")}</th>`).join("")}</tr>
          </thead>
          <tbody>
            ${
              rows.length
                ? rows
                    .map(
                      (row) =>
                        `<tr>${row.map((cell) => `<td>${escapeHTML(cell || "")}</td>`).join("")}</tr>`,
                    )
                    .join("")
                : `<tr><td colspan="${visibleCols || 1}" class="message-table-empty">暂无数据行</td></tr>`
            }
          </tbody>
        </table>
      </div>
      ${
        truncation.length
          ? `<div class="message-table-note">${escapeHTML(truncation.join("，"))}</div>`
          : ""
      }
    </div>
  `;
}

function normalizeTableRow(row, width) {
  const values = Array.isArray(row) ? [...row] : [];
  while (values.length < width) {
    values.push("");
  }
  return values;
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
  const showSummary = shouldRenderJobSummary(job, assistantMessage?.content || "");
  const artifactCards = await Promise.all(
    relatedArtifacts.map((artifact) => buildArtifactCardMarkup(artifact, "chat")),
  );
  const cardClass =
    job.status === "failed"
      ? "failed"
      : job.status === "incomplete"
        ? "incomplete"
        : job.status === "canceled"
          ? "canceled"
          : "done";
  const phaseMarkup = buildJobPhasesMarkup(job);
  const showInlinePhases = job.status === "failed";
  const detailMarkup = buildJobExecutionDetailsMarkup(job, {
    phasesMarkup: showInlinePhases ? "" : phaseMarkup,
    summaryLabel: showInlinePhases ? "查看任务详情" : "查看过程信息",
    summaryHint: showInlinePhases ? "计划与执行记录" : "阶段、计划与执行记录",
  });

  return `
    <section class="message-job-card ${cardClass}">
      <div class="message-job-head">
        <strong>任务详情</strong>
        ${statusPill(statusKindForJob(job.status), formatJobStatus(job.status))}
      </div>
      ${showInlinePhases ? phaseMarkup : ""}
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
      ${""/* retry button hidden — kept in backend API only */}
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
                ${buildCheckpointDebugMarkup(checkpoint)}
              </div>
            `,
          )
          .join("")}
      </div>
    </div>
  `;
}

function buildCheckpointDebugMarkup(checkpoint) {
  const rawError = String(checkpoint?.metadata?.raw_error || "").trim();
  if (!rawError) {
    return "";
  }

  return `
    <div class="message-debug-block">
      <div class="message-debug-label">底层错误</div>
      <pre>${escapeHTML(rawError)}</pre>
    </div>
  `;
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

function buildJobExecutionDetailsMarkup(job, options = {}) {
  const {
    phasesMarkup = "",
    summaryLabel = "查看任务详情",
    summaryHint = "计划与执行记录",
  } = options;
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

  if (!phasesMarkup && !checkpointsMarkup && !planMarkup && !stepMarkup) {
    return "";
  }

  return `
    <details class="message-plan-details message-job-details">
      <summary>
        <span>${summaryLabel}</span>
        <span class="message-plan-summary">${summaryHint}</span>
      </summary>
      <div class="message-job-extra">
        ${phasesMarkup}
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
  const artifactURL = artifactResourceURL(artifact);
  let body = `<p class="muted">${escapeHTML(artifact.summary || artifact.content_type || "")}</p>`;
  if (artifact.kind === "plot") {
    body += `
      <button
        type="button"
        class="artifact-preview-button"
        data-artifact-url="${escapeAttribute(artifactURL)}"
        data-artifact-title="${escapeAttribute(artifact.title)}"
      >
        <img
          src="${artifactURL}"
          alt="${escapeAttribute(artifact.title)}"
          loading="lazy"
          decoding="async"
        />
      </button>
    `;
  } else if (isPreviewableDelimitedArtifact(artifact)) {
    const text = await getArtifactTextPreview(artifact);
    const previewMarkup = renderDelimitedArtifactPreviewMarkup(artifact, text);
    if (previewMarkup) {
    body += `
      <div class="artifact-text-preview">
        ${previewMarkup}
      </div>
    `;
    }
  }

  return `
    <section class="artifact-card artifact-card-${variant}">
      <div class="artifact-head">
        <h3>${escapeHTML(artifact.title)}</h3>
        <div class="artifact-actions">
          <a class="inline-link" href="${artifactURL}" target="_blank" rel="noreferrer">打开</a>
          <a class="inline-link" href="${artifactURL}" download>下载</a>
        </div>
      </div>
      ${body}
    </section>
  `;
}

function renderDelimitedArtifactPreviewMarkup(artifact, text) {
  const table = parseDelimitedTableBlock(text, artifactDelimiterHint(artifact));
  if (!table) {
    return "<p class='muted'>无法生成表格预览，请下载查看。</p>";
  }
  return renderStructuredTableMarkup(table, artifactTablePreviewOptions);
}

function isPreviewableDelimitedArtifact(artifact) {
  if (!artifact) {
    return false;
  }
  const contentType = String(artifact.content_type || "").toLowerCase();
  if (contentType === "text/csv" || contentType === "text/tab-separated-values") {
    return true;
  }
  const path = String(artifact.path || artifact.url || artifact.title || "").toLowerCase();
  return path.endsWith(".csv") || path.endsWith(".tsv");
}

function artifactDelimiterHint(artifact) {
  const contentType = String(artifact?.content_type || "").toLowerCase();
  if (contentType === "text/tab-separated-values") {
    return "tsv";
  }
  if (contentType === "text/csv") {
    return "csv";
  }
  const path = String(artifact?.path || artifact?.url || artifact?.title || "").toLowerCase();
  if (path.endsWith(".tsv")) {
    return "tsv";
  }
  return "csv";
}

async function getArtifactTextPreview(artifact) {
  if (appState.artifactTextCache.has(artifact.id)) {
    return appState.artifactTextCache.get(artifact.id);
  }

  try {
    const response = await fetch(artifactResourceURL(artifact));
    const text = await response.text();
    appState.artifactTextCache.set(artifact.id, text);
    return text;
  } catch (error) {
    const fallback = `无法加载预览：${error.message}`;
    appState.artifactTextCache.set(artifact.id, fallback);
    return fallback;
  }
}

function artifactResourceURL(artifact) {
  const rawURL = artifact?.url || "";
  const version = artifact?.id || artifact?.created_at || "";
  if (!rawURL || !version) {
    return rawURL;
  }
  return `${rawURL}${rawURL.includes("?") ? "&" : "?"}v=${encodeURIComponent(version)}`;
}

function bindArtifactPreviewButtons(container) {
  for (const button of container.querySelectorAll(".artifact-preview-button")) {
    button.addEventListener("click", () => {
      openImageModal(button.dataset.artifactUrl, button.dataset.artifactTitle);
    });
  }
}

function bindEditJobButtons(container) {
  for (const button of container.querySelectorAll(".edit-job-button")) {
    button.addEventListener("click", () => {
      const jobId = button.dataset.jobId;
      if (jobId) {
        renderActions.editJobRequest(jobId);
      }
    });
  }
}

function bindRetryButtons(container) {
  for (const button of container.querySelectorAll(".retry-job-button")) {
    button.addEventListener("click", () => {
      const jobId = button.dataset.jobId;
      if (jobId) {
        button.disabled = true;
        button.textContent = "重试中…";
        renderActions.retryJob(jobId);
      }
    });
  }
}

function bindRegenerateButtons(container) {
  for (const button of container.querySelectorAll(".regenerate-button")) {
    button.addEventListener("click", () => {
      const jobId = button.dataset.jobId;
      if (jobId) {
        button.disabled = true;
        button.textContent = "重新生成中…";
        renderActions.regenerateResponse(jobId);
      }
    });
  }
}

function startInlineEdit(element, currentValue, onSave) {
  if (element.querySelector(".inline-edit-input")) {
    return;
  }
  const input = document.createElement("input");
  input.type = "text";
  input.className = "inline-edit-input";
  input.value = currentValue;

  const originalHTML = element.innerHTML;
  element.innerHTML = "";
  element.appendChild(input);
  input.focus();
  input.select();

  let committed = false;

  function commit() {
    if (committed) return;
    committed = true;
    const newValue = input.value.trim();
    if (newValue && newValue !== currentValue) {
      element.textContent = newValue;
      onSave(newValue);
    } else {
      element.innerHTML = originalHTML;
    }
  }

  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      event.preventDefault();
      commit();
    } else if (event.key === "Escape") {
      committed = true;
      element.innerHTML = originalHTML;
    }
  });
  input.addEventListener("blur", commit);
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
