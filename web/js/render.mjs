import { appState, getQuickActions } from "./state.mjs";
import { t } from "./i18n.mjs";
import {
  escapeAttribute,
  escapeHTML,
  formatAnalysisState,
  formatAnnotation,
  formatAnnotationRole,
  formatArtifactKind,
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
  openConfirmModal,
  openImageModal,
  openStatusOverviewModal,
  openWorkspaceFilesModal,
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
  async deleteWorkspace() {},
  async deleteConversation() {},
};

const artifactTablePreviewOptions = {
  maxTableRows: 12,
  maxTableCols: 8,
};

let entityContextMenu = null;
let entityContextMenuEventsBound = false;

export function configureRenderActions(actions) {
  Object.assign(renderActions, actions);
}

function ensureEntityContextMenu() {
  if (entityContextMenu?.isConnected) {
    return entityContextMenu;
  }

  entityContextMenu = document.getElementById("entityContextMenu");
  if (!entityContextMenu) {
    entityContextMenu = document.createElement("div");
    entityContextMenu.id = "entityContextMenu";
    entityContextMenu.className = "workspace-context-menu hidden";
    entityContextMenu.setAttribute("role", "menu");
    document.body.appendChild(entityContextMenu);
  }

  if (!entityContextMenuEventsBound) {
    document.addEventListener(
      "click",
      (event) => {
        const target = event.target;
        if (target instanceof Element && target.closest(".workspace-context-menu")) {
          return;
        }
        closeEntityContextMenu();
      },
      true,
    );
    document.addEventListener(
      "contextmenu",
      (event) => {
        const target = event.target;
        if (target instanceof Element && target.closest(".workspace-context-menu")) {
          return;
        }
        closeEntityContextMenu();
      },
      true,
    );
    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        closeEntityContextMenu();
      }
    });
    window.addEventListener("resize", closeEntityContextMenu);
    window.addEventListener("blur", closeEntityContextMenu);
    document.addEventListener(
      "scroll",
      () => {
        closeEntityContextMenu();
      },
      true,
    );
    entityContextMenuEventsBound = true;
  }

  return entityContextMenu;
}

function closeEntityContextMenu() {
  const menu = entityContextMenu || document.getElementById("entityContextMenu");
  if (!menu) {
    return;
  }
  menu.classList.add("hidden");
  menu.innerHTML = "";
  menu.style.left = "";
  menu.style.top = "";
}

function positionEntityContextMenu(menu, clientX, clientY) {
  menu.style.left = "0px";
  menu.style.top = "0px";
  const rect = menu.getBoundingClientRect();
  const left = Math.max(12, Math.min(clientX, window.innerWidth - rect.width - 12));
  const top = Math.max(12, Math.min(clientY, window.innerHeight - rect.height - 12));
  menu.style.left = `${left}px`;
  menu.style.top = `${top}px`;
}

function openEntityContextMenu(event, items) {
  event.preventDefault();
  event.stopPropagation();

  const menu = ensureEntityContextMenu();
  menu.innerHTML = `
    <div class="workspace-context-menu-surface">
      ${items
        .map(
          (item, index) => `
            <button
              type="button"
              class="workspace-context-menu-item ${item.danger ? "danger" : ""}"
              data-menu-index="${index}"
            >${escapeHTML(item.label)}</button>
          `,
        )
        .join("")}
    </div>
  `;

  for (const button of menu.querySelectorAll("[data-menu-index]")) {
    button.addEventListener("click", async () => {
      const item = items[Number(button.dataset.menuIndex)];
      closeEntityContextMenu();
      if (item?.onSelect) {
        await item.onSelect();
      }
    });
  }

  menu.classList.remove("hidden");
  positionEntityContextMenu(menu, event.clientX, event.clientY);
}

function startMenuInlineRename(element, onSave) {
  if (!(element instanceof HTMLElement) || !element.isConnected) {
    return;
  }
  const currentValue = element.textContent.trim();
  window.requestAnimationFrame(() => {
    if (!(element instanceof HTMLElement) || !element.isConnected) {
      return;
    }
    startInlineEdit(element, currentValue, onSave);
  });
}

function openWorkspaceContextMenu(event, workspaceID, renameElement) {
  openEntityContextMenu(event, [
    {
      label: t("ui.rename"),
      onSelect: async () => {
        startMenuInlineRename(renameElement, async (newLabel) => {
          await renderActions.renameWorkspace(workspaceID, newLabel);
        });
      },
    },
    {
      label: t("ui.deleteWorkspace"),
      danger: true,
      onSelect: async () => {
        const label = renameElement?.textContent?.trim() || workspaceID;
        const confirmed = await openConfirmModal({
          eyebrow: t("ui.deleteWorkspace"),
          title: t("ui.confirmDeleteWorkspaceTitle", { label }),
          message: t("ui.confirmDeleteWorkspaceMsg"),
          confirmLabel: t("ui.confirmDeleteWorkspaceButton"),
          danger: true,
        });
        if (!confirmed) {
          return;
        }
        await renderActions.deleteWorkspace(workspaceID);
      },
    },
  ]);
}

function openConversationContextMenu(event, conversationID, renameElement) {
  openEntityContextMenu(event, [
    {
      label: t("ui.rename"),
      onSelect: async () => {
        startMenuInlineRename(renameElement, async (newLabel) => {
          await renderActions.renameConversation(conversationID, newLabel);
        });
      },
    },
    {
      label: t("ui.deleteConversation"),
      danger: true,
      onSelect: async () => {
        const label = renameElement?.textContent?.trim() || conversationID;
        const confirmed = await openConfirmModal({
          eyebrow: t("ui.deleteConversation"),
          title: t("ui.confirmDeleteConversationTitle", { label }),
          message: t("ui.confirmDeleteConversationMsg"),
          confirmLabel: t("ui.confirmDeleteConversationButton"),
          danger: true,
        });
        if (!confirmed) {
          return;
        }
        await renderActions.deleteConversation(conversationID);
      },
    },
  ]);
}

export function renderQuickActions() {
  const container = document.getElementById("quickActions");
  if (!container) {
    return;
  }

  container.innerHTML = "";
  for (const action of getQuickActions()) {
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
  closeEntityContextMenu();
  renderWorkspaceNavigator();
  renderStatusOverviewEntry();
  renderStatusOverviewModal();
  renderSessionMeta();
  renderConsoleInfoBar();
  renderSkillHub();
  renderWorkspaceFilesEntry();
  void renderWorkspaceFilesModal();
  renderChat();
  renderInspector();
  renderPlannerPreview();
}

export function renderSessionMeta() {
  const meta = document.getElementById("sessionMeta");
  if (!appState.snapshot) {
    meta.innerHTML = `<p class='muted'>${escapeHTML(t("ui.sessionNotLoaded"))}</p>`;
    return;
  }
  const { session, workspace, objects, jobs, artifacts } = appState.snapshot;
  const conversations = appState.workspaceSnapshot?.conversations || (session ? [session] : []);
  const currentWorkspace = appState.workspaceSnapshot?.workspace || workspace;
  const workspaceLabel = currentWorkspace?.label || t("ui.unnamedWorkspace");
  const collapsed = Boolean(appState.sessionMetaCollapsed);
  const workspaceStatus = appState.workspaceStatus ? `<p class="workspace-status muted">${escapeHTML(appState.workspaceStatus)}</p>` : "";
  const conversationMarkup = conversations.length
    ? `
      <div class="workspace-collection-meta">
        <span>${escapeHTML(t("ui.conversationCount", { count: conversations.length }))}</span>
        ${conversations.length > 3 ? `<span>${escapeHTML(t("ui.showOnly3Hint"))}</span>` : ""}
      </div>
      <div class="workspace-collection-scroll">
        <div class="conversation-list">
          ${conversations
            .map(
              (conversation) => `
                <button
                  type="button"
                  class="conversation-chip ${conversation.id === session.id ? "active" : ""}"
                  data-conversation-id="${escapeAttribute(conversation.id)}"
                >
                  <span
                    class="conversation-chip-label"
                    title="${escapeAttribute(t("ui.dblclickRename"))}"
                  >${escapeHTML(formatConversationLabel(conversation))}</span>
                  <span class="conversation-chip-id">${escapeHTML(conversation.id)}</span>
                </button>
              `,
            )
            .join("")}
        </div>
      </div>
    `
    : `<p class='muted'>${escapeHTML(t("ui.noConversations"))}</p>`;

  meta.innerHTML = `
    <div class="workspace-meta-eyebrow">${escapeHTML(t("ui.currentWorkspace"))}</div>
    <div class="workspace-meta-head">
      <div>
        <div class="workspace-title-row">
          <div
            class="workspace-title"
            data-workspace-id="${escapeAttribute(currentWorkspace?.id || "")}"
            title="${escapeAttribute(t("ui.dblclickRename"))}"
          >${escapeHTML(workspaceLabel)}</div>
          <details class="workspace-help-popover">
            <summary aria-label="${escapeAttribute(t("ui.dblclickRename"))}">?</summary>
            <div class="workspace-help-body">
              <p><strong>${escapeHTML(t("ui.helpNewWorkspace"))}</strong>${escapeHTML(t("ui.helpNewWorkspaceDesc"))}</p>
              <p><strong>${escapeHTML(t("ui.helpNewConversation"))}</strong>${escapeHTML(t("ui.helpNewConversationDesc"))}</p>
            </div>
          </details>
        </div>
      </div>
      <div class="workspace-meta-actions">
        <button
          id="newConversationButton"
          type="button"
          class="ghost-button conversation-create-button"
          title="${escapeAttribute(t("ui.newConversationTitle"))}"
        >${escapeHTML(t("ui.newConversationButton"))}</button>
        <button
          type="button"
          class="ghost-button workspace-panel-toggle"
          data-toggle-panel="session-meta"
          aria-expanded="${collapsed ? "false" : "true"}"
        >${escapeHTML(collapsed ? t("ui.expand") : t("ui.collapse"))}</button>
      </div>
    </div>
    <div class="workspace-panel-body ${collapsed ? "collapsed" : ""}">
      ${workspaceStatus}
      <div class="workspace-summary-grid">
        <div class="workspace-summary-item">
          <strong>${objects.length}</strong>
          <span>${escapeHTML(t("ui.h5adFiles"))}</span>
        </div>
        <div class="workspace-summary-item">
          <strong>${jobs.length}</strong>
          <span>${escapeHTML(t("ui.taskCount"))}</span>
        </div>
        <div class="workspace-summary-item">
          <strong>${artifacts.length}</strong>
          <span>${escapeHTML(t("ui.resultFiles"))}</span>
        </div>
      </div>
      <div class="workspace-section-label">${escapeHTML(t("ui.conversations"))}</div>
      ${conversationMarkup}
    </div>
  `;
  bindWorkspaceMeta(meta);
}

export function renderPlannerPreview() {
  const container = document.getElementById("plannerPreviewModalContent");
  const copyButton = document.getElementById("plannerPreviewCopyButton");
  const copyStatus = document.getElementById("plannerPreviewCopyStatus");
  const preview = appState.plannerPreview;
  if (!container) {
    return;
  }
  if (copyButton) {
    copyButton.disabled = !preview;
    copyButton.onclick = null;
  }
  if (copyStatus) {
    copyStatus.textContent = "";
  }
  if (!preview) {
    container.innerHTML = `<p class='muted'>${escapeHTML(t("ui.noPlannerPreview"))}</p>`;
    return;
  }

  const blocks = [];
  blocks.push(
    renderSidebarCard({
      title: t("ui.plannerPreview"),
      body: `
        <div class="kv"><span>${escapeHTML(t("ui.mode"))}</span><span>${escapeHTML(formatPlannerMode(preview.planner_mode))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.currentObjectLabel"))}</span><span>${escapeHTML(preview.planning_request?.focus_object?.label || t("ui.none"))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.note"))}</span><span>${escapeHTML(preview.note || t("ui.none"))}</span></div>
      `,
    }),
  );

  blocks.push(
    renderSidebarCard({
      title: t("ui.workingMemory"),
      open: false,
      body: renderWorkingMemoryMarkup(preview.planning_request?.working_memory),
    }),
  );

  if (preview.planner_context?.length) {
    blocks.push(
      renderSidebarCard({
        title: "Planner Context",
        body: `<pre>${escapeHTML(preview.planner_context.join("\n"))}</pre>`,
      }),
    );
  }

  blocks.push(
    renderSidebarCard({
      title: t("ui.internalPlanSnapshot"),
      open: false,
      body: `<pre>${escapeHTML(JSON.stringify(preview.planning_request, null, 2))}</pre>`,
    }),
  );

  if (preview.developer_instructions) {
    blocks.push(
      renderSidebarCard({
        title: t("ui.developerInstructions"),
        body: `<pre>${escapeHTML(preview.developer_instructions)}</pre>`,
      }),
    );
  }

  if (preview.request_body) {
    blocks.push(
      renderSidebarCard({
        title: t("ui.plannerRequestBody"),
        body: `<pre>${escapeHTML(JSON.stringify(preview.request_body, null, 2))}</pre>`,
      }),
    );
  }

  container.innerHTML = blocks.join("");

  if (copyButton) {
    copyButton.onclick = async () => {
      try {
        await copyTextToClipboard(JSON.stringify(buildPlannerPreviewClipboardPayload(preview), null, 2));
        if (copyStatus) {
          copyStatus.textContent = t("ui.copied");
        }
      } catch (error) {
        if (copyStatus) {
          copyStatus.textContent = error?.message || t("ui.copyFailed");
        }
      }
    };
  }
}

function buildPlannerPreviewClipboardPayload(preview) {
  return {
    planner_mode: preview?.planner_mode || "",
    note: preview?.note || "",
    planner_context: preview?.planner_context || [],
    planning_request: preview?.planning_request || null,
    developer_instructions: preview?.developer_instructions || "",
    request_body: preview?.request_body || null,
  };
}

async function copyTextToClipboard(text) {
  if (navigator?.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }

  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();

  try {
    if (!document.execCommand("copy")) {
      throw new Error(t("ui.browserCopyDenied"));
    }
  } finally {
    textarea.remove();
  }
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
    return `<p class='muted'>${escapeHTML(t("ui.systemStatusUnavailable"))}</p>`;
  }

  const pills = [
    statusPill(status.system_mode === "live" ? "ok" : "warn", t("ui.statusPillMode", { mode: formatSystemMode(status.system_mode) })),
    statusPill(status.planner_mode === "llm" ? "ok" : "warn", t("ui.statusPillPlanner", { mode: formatPlannerMode(status.planner_mode) })),
    statusPill(status.llm_loaded ? "ok" : "muted", t("ui.statusPillModel", { status: status.llm_loaded ? t("ui.modelLoaded") : t("ui.modelNotLoaded") })),
    status.planner_mode === "llm"
      ? statusPill(
          status.planner_reachable ? "ok" : "bad",
          t("ui.statusPillPlannerReachable", { status: status.planner_reachable ? t("ui.plannerNormal") : t("ui.plannerAbnormal") }),
        )
      : statusPill("muted", t("ui.plannerRuleMode")),
    statusPill(status.runtime_connected ? "ok" : "bad", t("ui.statusPillRuntime", { status: status.runtime_connected ? t("ui.runtimeConnected") : t("ui.runtimeOffline") })),
    statusPill(status.real_h5ad_inspection ? "ok" : "muted", t("ui.statusPillH5ad", { status: status.real_h5ad_inspection ? t("ui.h5adReal") : t("ui.h5adPlaceholder") })),
    statusPill(status.real_analysis_execution ? "ok" : "warn", t("ui.statusPillAnalysis", { status: status.real_analysis_execution ? t("ui.analysisReal") : t("ui.analysisPlaceholder") })),
  ];

  const runtime = status.runtime || {};
  const environmentChecks = runtime.environment_checks || [];
  const notes = status.notes || [];
  const failingChecks = environmentChecks.filter((check) => !check.ok);

  const cards = [
    renderSidebarCard({
      title: t("ui.systemStatus"),
      badge: statusPill(status.system_mode === "live" ? "ok" : "warn", formatSystemMode(status.system_mode)),
      body: `
        <div class="status-pills">${pills.join("")}</div>
        <div class="status-detail-grid">
          <div class="kv"><span>${escapeHTML(t("ui.runtimeMode"))}</span><span>${escapeHTML(formatRuntimeMode(status.runtime_mode))}</span></div>
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
        title: t("ui.executableSkills"),
        badge: statusPill("ok", t("ui.skillCountBadge", { count: status.executable_skills.length })),
        body: `
          <p class="muted">${escapeHTML(t("ui.clickSkillHint"))}</p>
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
        title: t("ui.envCheck"),
        badge: failingChecks.length ? statusPill("bad", t("ui.envFailed", { count: failingChecks.length })) : statusPill("ok", t("ui.envNormal")),
        body: `
          <div class="status-detail-grid compact">
            <div class="kv"><span>Python</span><span>${escapeHTML(runtime.python_version || t("ui.unknown"))}</span></div>
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
                          <p class="muted">${escapeHTML(check.detail || t("ui.unknownError"))}</p>
                        </div>
                      `,
                    )
                    .join("")}
                </div>
              `
              : `<p class="muted">${escapeHTML(t("ui.envAllPassed"))}</p>`
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
          <span class="status-overview-eyebrow">${escapeHTML(t("ui.statusOverviewEyebrow"))}</span>
          <strong>${escapeHTML(t("ui.statusOverviewTitle"))}</strong>
          <p class="muted">${escapeHTML(t("ui.systemStatusUnavailable"))}</p>
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
        <span class="status-overview-eyebrow">${escapeHTML(t("ui.statusOverviewEyebrow"))}</span>
        <strong>${escapeHTML(t("ui.statusOverviewTitle"))}</strong>
      </div>
      <div class="status-overview-meta">
        ${statusPill(status.system_mode === "live" ? "ok" : "warn", formatSystemMode(status.system_mode))}
        ${
          status.planner_mode === "llm"
            ? statusPill(status.planner_reachable ? "ok" : "bad", status.planner_reachable ? t("ui.plannerOnline") : t("ui.plannerOffline"))
            : statusPill("muted", t("ui.rulePlanner"))
        }
        ${statusPill(status.runtime_connected ? "ok" : "bad", status.runtime_connected ? t("ui.runtimeOnline") : t("ui.runtimeOfflineShort"))}
        ${statusPill(
          failingChecks.length ? "bad" : "ok",
          failingChecks.length ? t("ui.envFailedCount", { count: failingChecks.length }) : t("ui.envOk"),
        )}
        ${statusPill(skillCount ? "ok" : "muted", t("ui.skillCountShort", { count: skillCount }))}
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

function renderWorkspaceFilesEntry() {
  const button = document.getElementById("workspaceFilesButton");
  if (!button) {
    return;
  }

  const entries = buildWorkspaceFileEntries();
  const currentWorkspace = appState.workspaceSnapshot?.workspace || appState.snapshot?.workspace || null;
  const objectCount = entries.filter((entry) => entry.type === "object").length;
  const artifactCount = entries.filter((entry) => entry.type === "artifact").length;

  button.textContent = t("ui.viewWorkspaceFilesCount", { count: entries.length });
  button.title = entries.length
    ? t("ui.workspaceFilesTooltip", { label: currentWorkspace?.label || "workspace", objectCount, artifactCount })
    : t("ui.workspaceFilesEmpty");
  button.disabled = !entries.length;
  button.onclick = entries.length ? openWorkspaceFilesModal : null;
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
  const collapsed = Boolean(appState.workspaceNavigatorCollapsed);

  container.innerHTML = `
    <div class="workspace-navigator-head">
      <div>
        <div class="workspace-meta-eyebrow">${escapeHTML(t("ui.workspaceSection"))}</div>
        <div class="workspace-navigator-title">${escapeHTML(t("ui.workspaceList"))}</div>
      </div>
      <div class="workspace-meta-actions">
        <button
          id="newWorkspaceButton"
          type="button"
          class="ghost-button conversation-create-button"
          title="${escapeAttribute(t("ui.newWorkspaceTitle"))}"
        >${escapeHTML(t("ui.newWorkspaceButton"))}</button>
        <button
          type="button"
          class="ghost-button workspace-panel-toggle"
          data-toggle-panel="workspace-navigator"
          aria-expanded="${collapsed ? "false" : "true"}"
        >${escapeHTML(collapsed ? t("ui.expand") : t("ui.collapse"))}</button>
      </div>
    </div>
    <div class="workspace-panel-body ${collapsed ? "collapsed" : ""}">
      ${
        workspaceList.length
          ? `
            <div class="workspace-collection-meta">
              <span>${escapeHTML(t("ui.workspaceCount", { count: workspaceList.length }))}</span>
              ${workspaceList.length > 3 ? `<span>${escapeHTML(t("ui.showOnly3Hint"))}</span>` : ""}
            </div>
            <div class="workspace-collection-scroll">
              <div class="workspace-list">
                ${workspaceList
                  .map(
                    (item) => `
                      <button
                        type="button"
                        class="workspace-chip ${item.id === currentWorkspace?.id ? "active" : ""}"
                        data-workspace-id="${escapeAttribute(item.id)}"
                      >
                        <span
                          class="workspace-chip-label"
                          title="${escapeAttribute(t("ui.dblclickRename"))}"
                        >${escapeHTML(item.label || item.id)}</span>
                        <span class="workspace-chip-id">${escapeHTML(item.id)}</span>
                      </button>
                    `,
                  )
                  .join("")}
              </div>
            </div>
          `
          : `<p class='muted'>${escapeHTML(t("ui.noWorkspaces"))}</p>`
      }
    </div>
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
  const datasetID = currentWorkspace?.dataset_id || session?.dataset_id || t("ui.notSet");

  container.innerHTML = `
    <div class="console-info-grid">
      <div class="workspace-identity-card">
        <span>Workspace</span>
        <strong>${escapeHTML(currentWorkspace?.id || t("ui.notSet"))}</strong>
      </div>
      <div class="workspace-identity-card">
        <span>${escapeHTML(t("ui.currentConversation"))}</span>
        <strong>${escapeHTML(session?.id || t("ui.notSet"))}</strong>
      </div>
      <div class="workspace-identity-card">
        <span>${escapeHTML(t("ui.dataset"))}</span>
        <strong>${escapeHTML(datasetID)}</strong>
      </div>
    </div>
  `;
}

function bindWorkspaceNavigator(container) {
  const toggleButton = container.querySelector('[data-toggle-panel="workspace-navigator"]');
  if (toggleButton) {
    toggleButton.addEventListener("click", () => {
      appState.workspaceNavigatorCollapsed = !appState.workspaceNavigatorCollapsed;
      renderWorkspaceNavigator();
    });
  }

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
    button.addEventListener("contextmenu", (event) => {
      openWorkspaceContextMenu(event, button.dataset.workspaceId, label || button);
    });
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
  const toggleButton = container.querySelector('[data-toggle-panel="session-meta"]');
  if (toggleButton) {
    toggleButton.addEventListener("click", () => {
      appState.sessionMetaCollapsed = !appState.sessionMetaCollapsed;
      renderSessionMeta();
    });
  }

  const createButton = container.querySelector("#newConversationButton");
  if (createButton) {
    createButton.addEventListener("click", async () => {
      await renderActions.createConversation();
    });
  }

  const workspaceTitle = container.querySelector(".workspace-title[data-workspace-id]");
  if (workspaceTitle) {
    workspaceTitle.addEventListener("contextmenu", (event) => {
      openWorkspaceContextMenu(event, workspaceTitle.dataset.workspaceId, workspaceTitle);
    });
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
    button.addEventListener("contextmenu", (event) => {
      openConversationContextMenu(event, button.dataset.conversationId, label || button);
    });
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
    title: t("ui.skillHub"),
    badge: statusPill(enabledBundles.length ? "ok" : "muted", `${enabledBundles.length}/${bundles.length || 0}`),
    open: false,
    body: `
      <p class="muted">${escapeHTML(t("ui.skillHubDesc"))}</p>
      <div class="plugin-summary-grid">
        <div class="plugin-summary-item">
          <strong>${enabledBundles.length}</strong>
          <span>${escapeHTML(t("ui.enabledBundles"))}</span>
        </div>
        <div class="plugin-summary-item">
          <strong>${appState.skills.length}</strong>
          <span>${escapeHTML(t("ui.loadedSkills"))}</span>
        </div>
      </div>
      <div class="plugin-hub-actions">
        <a class="ghost-button button-link" href="/plugins.html">${escapeHTML(t("ui.openPluginManager"))}</a>
      </div>
    `,
  });
}

async function renderWorkspaceFilesModal() {
  const container = document.getElementById("workspaceFilesModalContent");
  const title = document.getElementById("workspaceFilesModalTitle");
  if (!container) {
    return;
  }

  const renderVersion = ++appState.workspaceFilesModalRenderVersion;
  const entries = buildWorkspaceFileEntries();
  const currentWorkspace = appState.workspaceSnapshot?.workspace || appState.snapshot?.workspace || null;
  if (title) {
    title.textContent = currentWorkspace?.label
      ? t("ui.currentWorkspaceFiles", { label: currentWorkspace.label })
      : t("ui.currentWorkspaceOutputFiles");
  }
  if (!entries.length) {
    container.innerHTML = `
      <div class="workspace-files-modal-empty">
        <p class="muted">${escapeHTML(t("ui.workspaceFilesEmpty"))}</p>
      </div>
    `;
    return;
  }

  const groups = buildWorkspaceFileGroups(entries);
  const selectedResource = selectedWorkspaceResource();
  const detailMarkup = await buildWorkspaceFileDetailMarkup(selectedResource);
  if (renderVersion !== appState.workspaceFilesModalRenderVersion) {
    return;
  }

  container.innerHTML = `
    <div class="workspace-files-modal-summary">
      <div class="workspace-summary-item">
        <strong>${entries.length}</strong>
        <span>${escapeHTML(t("ui.totalFiles"))}</span>
      </div>
      <div class="workspace-summary-item">
        <strong>${entries.filter((entry) => entry.type === "object").length}</strong>
        <span>${escapeHTML(t("ui.h5adFiles"))}</span>
      </div>
      <div class="workspace-summary-item">
        <strong>${entries.filter((entry) => entry.type === "artifact").length}</strong>
        <span>${escapeHTML(t("ui.resultFiles"))}</span>
      </div>
    </div>
    <div class="workspace-files-modal-layout">
      <section class="workspace-files-modal-panel">
        <div class="workspace-files-modal-panel-head">
          <strong>${escapeHTML(t("ui.fileList"))}</strong>
          <span class="muted">${escapeHTML(t("ui.fileListHint"))}</span>
        </div>
        <div class="workspace-files-modal-list">
          ${groups
            .map(
              (group) => `
                <section class="workspace-file-group">
                  <div class="workspace-section-label">${escapeHTML(group.title)}</div>
                  <div class="workspace-file-list">
                    ${group.entries.map((entry) => buildWorkspaceFileNodeMarkup(entry)).join("")}
                  </div>
                </section>
              `,
            )
            .join("")}
        </div>
      </section>
      <section class="workspace-files-modal-panel workspace-files-modal-detail-panel">
        <div class="workspace-files-modal-panel-head">
          <strong>${escapeHTML(selectedResource?.label || selectedResource?.fileName || t("ui.fileDetail"))}</strong>
          <span class="muted">${escapeHTML(selectedResource?.fileName || t("ui.selectFileForDetail"))}</span>
        </div>
        <div class="workspace-files-modal-detail">
          ${detailMarkup}
        </div>
      </section>
    </div>
  `;

  bindWorkspaceFileList(container, () => {
    void renderWorkspaceFilesModal();
  });
  bindArtifactPreviewButtons(container);
}

function buildWorkspaceFileGroups(entries) {
  return [
    {
      title: t("ui.h5adFiles"),
      entries: entries.filter((entry) => entry.type === "object"),
    },
    {
      title: t("ui.resultFiles"),
      entries: entries.filter((entry) => entry.type === "artifact"),
    },
  ].filter((group) => group.entries.length);
}

function buildWorkspaceFileNodeMarkup(entry) {
  return `
    <button
      type="button"
      class="tree-node workspace-file-node ${entry.resourceKey === selectedResourceKey() ? "active" : ""}"
      data-resource-key="${escapeAttribute(entry.resourceKey)}"
    >
      <div class="resource-node-head">
        <span class="resource-node-title">${escapeHTML(entry.fileName)}</span>
        ${entry.isActiveContext ? `<span class="resource-node-badge">${escapeHTML(t("ui.currentContextBadge"))}</span>` : ""}
      </div>
      <span class="label">${escapeHTML(entry.label)}</span>
      <span class="meta">${escapeHTML(entry.metaPrimary)}</span>
      <span class="meta meta-secondary">${escapeHTML(entry.metaSecondary)}</span>
    </button>
  `;
}

function bindWorkspaceFileList(container, onSelect = null) {
  for (const button of container.querySelectorAll("[data-resource-key]")) {
    button.addEventListener("click", () => {
      appState.selectedResourceKey = button.dataset.resourceKey || null;
      if (typeof onSelect === "function") {
        onSelect();
      }
      renderInspector();
    });
  }
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
      ? `${formatRole(message.role)} · ${t("ui.sending")}`
      : formatRole(message.role);

  const detailResult = await buildMessageDetailMarkup(message);
  if (detailResult.markup) {
    const detail = document.createElement("div");
    detail.className = "message-detail";
    detail.innerHTML = detailResult.markup;
    if (detailResult.primary) {
      detail.classList.add("message-detail-primary");
      node.insertBefore(detail, content);
    } else {
      node.appendChild(detail);
    }
  }
  if (String(message.content || "").trim()) {
    content.textContent = message.content;
    if (message.role === "assistant" && detailResult.primary) {
      content.classList.add("message-content-secondary");
    }
  } else {
    content.remove();
  }

  if (message.role === "assistant") {
    const job = snapshotJob(message.job_id || turnForMessage(message)?.job_id);
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
      `<button type="button" class="message-action-button retry-job-button" data-job-id="${escapeAttribute(job.id)}"${disabled}>${escapeHTML(t("ui.retryButton"))}</button>`,
    );
  }
  buttons.push(
    `<button type="button" class="message-action-button edit-job-button" data-job-id="${escapeAttribute(job.id)}"${disabled}>${escapeHTML(t("ui.editResendButton"))}</button>`,
  );
  if (job.status === "succeeded" || job.status === "incomplete") {
    buttons.push(
      `<button type="button" class="message-action-button regenerate-button" data-job-id="${escapeAttribute(job.id)}"${disabled}>${escapeHTML(t("ui.regenerateButton"))}</button>`,
    );
  }
  return buttons.join("");
}

function snapshotJob(jobId) {
  if (!jobId) {
    return null;
  }
  return (appState.snapshot?.jobs || []).find((item) => item.id === jobId) || null;
}

function snapshotTurn(turnId) {
  if (!turnId) {
    return null;
  }
  return (appState.snapshot?.turns || []).find((item) => item.id === turnId) || null;
}

function snapshotArtifact(artifactId) {
  if (!artifactId) {
    return null;
  }
  return (appState.snapshot?.artifacts || []).find((item) => item.id === artifactId) || null;
}

function snapshotObject(objectId) {
  if (!objectId) {
    return null;
  }
  return (appState.snapshot?.objects || []).find((item) => item.id === objectId) || null;
}

function turnForMessage(message) {
  return snapshotTurn(message?.turn_id);
}

function formatTurnStatusLabel(status) {
  switch (status) {
    case "fulfilled":
      return t("ui.turnFulfilled");
    case "failed":
      return t("ui.turnFailed");
    case "canceled":
      return t("ui.turnCanceled");
    default:
      return t("ui.turnPending");
  }
}

function statusKindForTurn(status) {
  switch (status) {
    case "fulfilled":
      return "ok";
    case "failed":
      return "bad";
    case "canceled":
      return "muted";
    default:
      return "warn";
  }
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
    truncation.push(t("ui.truncationRows", { visible: visibleRows, total: totalRows }));
  }
  if (totalCols > visibleCols) {
    truncation.push(t("ui.truncationCols", { visible: visibleCols, total: totalCols }));
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
                : `<tr><td colspan="${visibleCols || 1}" class="message-table-empty">${escapeHTML(t("ui.noDataRows"))}</td></tr>`
            }
          </tbody>
        </table>
      </div>
      ${
        truncation.length
          ? `<div class="message-table-note">${escapeHTML(truncation.join(t("ui.truncationSeparator")))}</div>`
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
    return { markup: "", primary: false };
  }

  if (message.role === "user") {
    const job = (appState.snapshot.jobs || []).find((item) => item.message_id === message.id);
    if (!job || (job.status !== "queued" && job.status !== "running")) {
      return { markup: "", primary: false };
    }
    return { markup: buildJobStatusMarkup(job), primary: false };
  }

  if (message.role === "assistant") {
    const turn = turnForMessage(message);
    const job = snapshotJob(message.job_id || turn?.job_id);
    if (job) {
      return {
        markup: await buildJobResultMarkup(job, message, turn),
        primary: true,
      };
    }
    if (turn) {
      const markup = await buildTurnResultMarkup(turn, message);
      if (markup) {
        return { markup, primary: true };
      }
    }
  }

  return { markup: "", primary: false };
}

function buildJobStatusMarkup(job) {
  return `
    <section class="message-job-card pending">
      <div class="message-job-head">
        <strong>${escapeHTML(t("ui.taskStatus"))}</strong>
        ${statusPill(statusKindForJob(job.status), formatJobStatus(job.status))}
      </div>
      ${buildJobPhasesMarkup(job)}
      <p class="message-job-summary">${escapeHTML(job.summary || t("ui.requestReceived"))}</p>
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

async function buildJobResultMarkup(job, assistantMessage, turn) {
  const resultMarkup = await buildTurnResultGroupMarkup(turn, { fallbackJob: job });
  const showSummary = shouldRenderJobSummary(job, assistantMessage?.content || "");
  const primaryError = primaryJobErrorText(job);
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
    summaryLabel: showInlinePhases ? t("ui.viewJobDetails") : t("ui.viewProcessInfo"),
    summaryHint: showInlinePhases ? t("ui.planAndExecution") : t("ui.phasesPlanExecution"),
  });

  return `
    <section class="message-job-card ${cardClass}">
      <div class="message-job-head">
        <strong>${escapeHTML(t("ui.taskDetail"))}</strong>
        ${statusPill(statusKindForJob(job.status), formatJobStatus(job.status))}
      </div>
      ${showInlinePhases ? phaseMarkup : ""}
      ${
        showSummary && job.summary
          ? `<p class="message-job-summary">${escapeHTML(job.summary)}</p>`
          : ""
      }
      ${
        primaryError
          ? `<p class="message-job-error">${escapeHTML(primaryError)}</p>`
          : ""
      }
      ${
        resultMarkup
      }
      ${""/* retry button hidden — kept in backend API only */}
      ${detailMarkup}
    </section>
  `;
}

function shouldRenderTurnSummary(turn, assistantContent = "") {
  if (!turn?.summary) {
    return false;
  }
  return turn.summary.trim() !== String(assistantContent || "").trim();
}

async function buildTurnResultMarkup(turn, assistantMessage) {
  const resultMarkup = await buildTurnResultGroupMarkup(turn);
  if (!resultMarkup) {
    return "";
  }
  const showSummary = shouldRenderTurnSummary(turn, assistantMessage?.content || "");
  const cardClass =
    turn.status === "failed"
      ? "failed"
      : turn.status === "canceled"
        ? "canceled"
        : "done";

  return `
    <section class="message-job-card ${cardClass}">
      <div class="message-job-head">
        <strong>${escapeHTML(t("ui.resultDetail"))}</strong>
        ${statusPill(statusKindForTurn(turn.status), formatTurnStatusLabel(turn.status))}
      </div>
      ${
        showSummary
          ? `<p class="message-job-summary">${escapeHTML(turn.summary)}</p>`
          : ""
      }
      ${resultMarkup}
    </section>
  `;
}

async function buildTurnResultGroupMarkup(turn, { fallbackJob = null } = {}) {
  const resultCards = await buildTurnResultCards(turn, { fallbackJob });
  if (!resultCards.length) {
    return "";
  }

  return `
    <div class="message-artifact-group">
      <div class="message-artifact-head">
        <strong>${escapeHTML(turnResultGroupLabel(turn, resultCards.length))}</strong>
        <span class="muted">${escapeHTML(t("ui.itemCount", { count: resultCards.length }))}</span>
      </div>
      ${resultCards.join("")}
    </div>
  `;
}

function turnResultGroupLabel(turn, count) {
  if (!count) {
    return t("ui.resultContent");
  }
  const refs = turn?.result_refs || [];
  const artifactCount = refs.filter((ref) => ref.kind === "artifact").length;
  return artifactCount === count ? t("ui.resultArtifacts") : t("ui.resultContent");
}

async function buildTurnResultCards(turn, { fallbackJob = null } = {}) {
  const refs = [];
  for (const ref of turn?.result_refs || []) {
    if (ref?.kind === "artifact" && snapshotArtifact(ref.artifact_id)) {
      refs.push(ref);
      continue;
    }
    if (ref?.kind === "object" && snapshotObject(ref.object_id)) {
      refs.push(ref);
    }
  }

  if (!refs.length && fallbackJob) {
    for (const artifact of appState.snapshot?.artifacts || []) {
      if (artifact.job_id === fallbackJob.id) {
        refs.push({
          kind: "artifact",
          artifact_id: artifact.id,
          artifact_kind: artifact.kind,
        });
      }
    }
  }

  const cards = [];
  for (const ref of refs) {
    if (ref.kind === "artifact") {
      const artifact = snapshotArtifact(ref.artifact_id);
      if (!artifact) {
        continue;
      }
      cards.push(await buildArtifactCardMarkup(artifact, "chat"));
      continue;
    }
    if (ref.kind === "object") {
      const object = snapshotObject(ref.object_id);
      if (!object) {
        continue;
      }
      cards.push(buildObjectResultCardMarkup(object, "chat"));
    }
  }
  return cards;
}

function buildObjectResultCardMarkup(object, variant = "chat") {
  return `
    <section class="artifact-card artifact-card-${variant}">
      <div class="artifact-head">
        <h3>${escapeHTML(object.label || object.id || t("ui.objectResult"))}</h3>
      </div>
      <p class="muted">${escapeHTML(`${formatObjectKind(object.kind)} · ${t("ui.cellCount", { count: object.n_obs || 0 })} · ${t("ui.geneCount", { count: object.n_vars || 0 })}`)}</p>
      <div class="kv"><span>${escapeHTML(t("ui.objectId"))}</span><span>${escapeHTML(object.id)}</span></div>
      <div class="kv"><span>${escapeHTML(t("ui.status"))}</span><span>${escapeHTML(formatObjectState(object.state))}</span></div>
      <div class="kv"><span>${escapeHTML(t("ui.currentContext"))}</span><span>${escapeHTML(object.id === appState.focusObjectId ? t("ui.yes") : t("ui.no"))}</span></div>
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
        <strong>${escapeHTML(t("ui.executionPhases"))}</strong>
        <span class="muted">${escapeHTML(t("ui.phaseCount", { count: phases.length }))}</span>
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
        <strong>${escapeHTML(t("ui.executionCheckpoints"))}</strong>
        <span class="muted">${escapeHTML(t("ui.checkpointCount", { count: checkpoints.length }))}</span>
      </div>
      <div class="message-checkpoint-list">
        ${checkpoints
          .map(
            (checkpoint) => `
              <div class="message-checkpoint-card">
                <div class="message-checkpoint-title">
                  <strong>${escapeHTML(checkpoint.title || t("ui.checkpoint"))}</strong>
                  ${statusPill(normalizeCheckpointTone(checkpoint.tone), checkpoint.label || t("ui.recorded"))}
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
      <div class="message-debug-label">${escapeHTML(t("ui.underlyingError"))}</div>
      <pre>${escapeHTML(rawError)}</pre>
    </div>
  `;
}

function primaryJobErrorText(job) {
  const publicError = String(job?.error || "").trim();
  if (publicError && publicError !== t("ui.executionFailedRetry")) {
    return publicError;
  }

  const rawError = primaryJobRawError(job);
  if (rawError) {
    return t("ui.failureReason", { reason: rawError });
  }

  return publicError;
}

function primaryJobRawError(job) {
  for (const checkpoint of job?.checkpoints || []) {
    const rawError = String(checkpoint?.metadata?.raw_error || "").trim();
    if (rawError) {
      return rawError;
    }
  }

  for (const phase of job?.phases || []) {
    const rawError = String(phase?.metadata?.raw_error || "").trim();
    if (rawError) {
      return rawError;
    }
  }

  return "";
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
    summaryLabel = t("ui.viewJobDetails"),
    summaryHint = t("ui.planAndExecution"),
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
                  <p class="muted">${escapeHTML(step.summary || t("ui.noSummaryReturned"))}</p>
                  ${
                    step.output_object_id
                      ? `<div class="message-step-meta">${escapeHTML(t("ui.outputObject"))}${escapeHTML(objectLabel(step.output_object_id))}</div>`
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
        <span>${escapeHTML(t("ui.executionPlan"))}</span>
        <span class="message-plan-summary">${escapeHTML(t("ui.stepCount", { count: steps.length }))}</span>
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
    : `<p class='muted'>${escapeHTML(t("ui.noExtraParams"))}</p>`;
  const memoryRefs = step.memory_refs?.length
    ? `<div class="kv"><span>${escapeHTML(t("ui.memoryRef"))}</span><span>${escapeHTML(step.memory_refs.join(t("ui.listSeparator")))}</span></div>`
    : "";

  return `
    <details class="message-plan-step">
      <summary>
        <span>${escapeHTML(t("ui.stepN", { n: index + 1, skill: formatSkillName(step.skill) }))}</span>
        <span class="message-plan-step-target">${escapeHTML(formatPlanTarget(step.target_object_id))}</span>
      </summary>
      <div class="message-plan-step-body">
        <div class="kv"><span>${escapeHTML(t("ui.skillLabel"))}</span><span>${escapeHTML(step.skill || t("ui.unknown"))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.target"))}</span><span>${escapeHTML(formatPlanTarget(step.target_object_id))}</span></div>
        ${memoryRefs}
        ${params}
      </div>
    </details>
  `;
}

function buildSelectedResourceDetailBlocks({ includeWorkingMemory = true } = {}) {
  const resource = selectedWorkspaceResource();
  const object = resource?.type === "object" ? resource.object : activeObject();
  const blocks = [];

  if (includeWorkingMemory) {
    blocks.push(
      renderSidebarCard({
        title: t("ui.workingMemory"),
        open: true,
        body: renderWorkingMemoryMarkup(appState.snapshot?.working_memory),
      }),
    );
  }

  if (!resource && !object) {
    blocks.push(
      renderSidebarCard({
        title: t("ui.fileDetail"),
        body: `<p class='muted'>${escapeHTML(t("ui.noFiles"))}</p>`,
      }),
    );
    return blocks;
  }

  if (resource?.type === "artifact") {
    const artifact = resource.artifact;
    const artifactURL = artifactResourceURL(artifact);
    const linkedObject = findWorkspaceObject(artifact.object_id);
    blocks.push(
      renderSidebarCard({
        title: resource.fileName,
        body: `
          <div class="kv"><span>${escapeHTML(t("ui.fileTitle"))}</span><span>${escapeHTML(artifact.title || resource.fileName)}</span></div>
          <div class="kv"><span>${escapeHTML(t("ui.type"))}</span><span>${escapeHTML(formatArtifactKind(artifact.kind))}</span></div>
          <div class="kv"><span>${escapeHTML(t("ui.contentType"))}</span><span>${escapeHTML(artifact.content_type || t("ui.notRecorded"))}</span></div>
          <div class="kv"><span>${escapeHTML(t("ui.filePath"))}</span><span>${escapeHTML(artifact.path || t("ui.notRecorded"))}</span></div>
          <div class="kv"><span>${escapeHTML(t("ui.linkedObject"))}</span><span>${escapeHTML(linkedObject?.label || artifact.object_id || t("ui.none"))}</span></div>
          <div class="kv"><span>${escapeHTML(t("ui.linkedJob"))}</span><span>${escapeHTML(artifact.job_id || t("ui.none"))}</span></div>
          <div class="kv"><span>${escapeHTML(t("ui.summary"))}</span><span>${escapeHTML(artifact.summary || t("ui.none"))}</span></div>
          <div class="kv"><span>${escapeHTML(t("ui.actions"))}</span><span>${
            artifactURL
              ? `<a class="inline-link" href="${artifactURL}" target="_blank" rel="noreferrer">${escapeHTML(t("ui.open"))}</a> · <a class="inline-link" href="${artifactURL}" download>${escapeHTML(t("ui.download"))}</a>`
              : escapeHTML(t("ui.notAvailable"))
          }</span></div>
        `,
      }),
    );
    if (linkedObject) {
      blocks.push(
        renderSidebarCard({
          title: t("ui.linkedObjectCard"),
          body: `
            <div class="kv"><span>${escapeHTML(t("ui.name"))}</span><span>${escapeHTML(linkedObject.label)}</span></div>
            <div class="kv"><span>${escapeHTML(t("ui.type"))}</span><span>${escapeHTML(formatObjectKind(linkedObject.kind))}</span></div>
            <div class="kv"><span>${escapeHTML(t("ui.cellCountLabel"))}</span><span>${escapeHTML(String(linkedObject.n_obs))}</span></div>
            <div class="kv"><span>${escapeHTML(t("ui.status"))}</span><span>${escapeHTML(formatObjectState(linkedObject.state))}</span></div>
          `,
        }),
      );
    }
  } else if (object) {
    appendObjectInspectorCards(blocks, object, {
      title: resource?.fileName || object.label,
    });
  }

  return blocks;
}

function renderInspector() {
  const container = document.getElementById("inspector");
  if (!container) {
    return;
  }
  container.innerHTML = buildSelectedResourceDetailBlocks({ includeWorkingMemory: true }).join("");
}

async function buildWorkspaceFileDetailMarkup(resource) {
  const blocks = buildSelectedResourceDetailBlocks({ includeWorkingMemory: false });
  if (resource?.type === "artifact") {
    const previewCard = await buildArtifactCardMarkup(resource.artifact, "modal");
    return `${previewCard}${blocks.join("")}`;
  }
  return blocks.join("");
}

function appendObjectInspectorCards(blocks, object, { title } = {}) {
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
      title: title || object.label,
      body: `
        <div class="kv"><span>${escapeHTML(t("ui.objectName"))}</span><span>${escapeHTML(object.label)}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.currentContext"))}</span><span>${escapeHTML(object.id === appState.focusObjectId ? t("ui.yes") : t("ui.no"))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.objectId"))}</span><span>${escapeHTML(object.id)}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.type"))}</span><span>${escapeHTML(formatObjectKind(object.kind))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.parentObject"))}</span><span>${escapeHTML(object.parent_id || t("ui.none"))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.backendRef"))}</span><span>${escapeHTML(object.backend_ref)}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.cellCountLabel"))}</span><span>${escapeHTML(String(object.n_obs))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.geneCountLabel"))}</span><span>${escapeHTML(String(object.n_vars))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.status"))}</span><span>${escapeHTML(formatObjectState(object.state))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.materializedPath"))}</span><span>${escapeHTML(object.materialized_path || t("ui.notGenerated"))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.download"))}</span><span>${
          object.materialized_url
            ? `<a class="inline-link" href="${object.materialized_url}" download>${escapeHTML(t("ui.getH5ad"))}</a>`
            : escapeHTML(t("ui.notAvailable"))
        }</span></div>
      `,
    }),
  );

  blocks.push(
    renderSidebarCard({
      title: t("ui.datasetAssessment"),
      body: `
        <div class="kv"><span>${escapeHTML(t("ui.status"))}</span><span>${escapeHTML(formatAnalysisState(assessment.preprocessing_state))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.matrixLayers"))}</span><span>${escapeHTML(formatList(metadata.layer_keys))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.obsFields"))}</span><span>${escapeHTML(formatList(metadata.obs_fields))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.varFields"))}</span><span>${escapeHTML(formatList(metadata.var_fields))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.embeddings"))}</span><span>${escapeHTML(formatList(metadata.obsm_keys))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.unsKeys"))}</span><span>${escapeHTML(formatList(metadata.uns_keys))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.cellType"))}</span><span>${escapeHTML(formatAnnotation(cellType))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.clustering"))}</span><span>${escapeHTML(formatAnnotation(cluster))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.availableAnalyses"))}</span><span>${escapeHTML(formatSkillList(assessment.available_analyses))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.missingRequirements"))}</span><span>${escapeHTML(formatList(assessment.missing_requirements))}</span></div>
        <div class="kv"><span>${escapeHTML(t("ui.suggestedNextSteps"))}</span><span>${escapeHTML(formatList(assessment.suggested_next_steps))}</span></div>
      `,
    }),
  );

  blocks.push(
    renderSidebarCard({
      title: t("ui.annotationCandidates"),
      body: categorical.length
        ? categorical
            .slice(0, 8)
            .map(
              (item) => `
                <div class="kv">
                  <span>${escapeHTML(item.field)}</span>
                  <span>${escapeHTML(`${formatAnnotationRole(item.role)} · ${item.n_categories} ${t("ui.groups")} · ${(item.sample_values || []).join(t("ui.listSeparator"))}`)}</span>
                </div>
              `,
            )
            .join("")
        : `<p class='muted'>${escapeHTML(t("ui.noCategoricalFields"))}</p>`,
    }),
  );

  blocks.push(
    renderSidebarCard({
      title: t("ui.recentJobs"),
      body: relatedJobs.length
        ? relatedJobs
            .slice(-3)
            .reverse()
            .map(
              (job) => `
                <div class="kv"><span>${escapeHTML(formatJobStatus(job.status))}</span><span>${escapeHTML(job.summary || t("ui.waiting"))}</span></div>
              `,
            )
            .join("")
        : `<p class='muted'>${escapeHTML(t("ui.noRelatedJobs"))}</p>`,
    }),
  );
}

function renderWorkingMemoryMarkup(memory) {
  if (!memory) {
    return `<p class='muted'>${escapeHTML(t("ui.noWorkingMemory"))}</p>`;
  }

  const focus = memory.focus;
  const recentArtifacts = memory.recent_artifacts || [];
  const confirmedPreferences = memory.confirmed_preferences || [];
  const stateChanges = memory.semantic_state_changes || [];

  const sections = [];
  sections.push(`
    <div class="workspace-section-label">${escapeHTML(t("ui.currentFocus"))}</div>
    ${
      focus
        ? `
          <div class="kv"><span>${escapeHTML(t("ui.currentObjectLabel"))}</span><span>${escapeHTML(focus.focus_object_label || focus.focus_object_id || t("ui.none"))}</span></div>
          <div class="kv"><span>${escapeHTML(t("ui.lastOutputObject"))}</span><span>${escapeHTML(focus.last_output_object_label || focus.last_output_object_id || t("ui.none"))}</span></div>
          <div class="kv"><span>${escapeHTML(t("ui.lastResult"))}</span><span>${escapeHTML(focus.last_artifact_title || focus.last_artifact_id || t("ui.none"))}</span></div>
        `
        : `<p class='muted'>${escapeHTML(t("ui.noFocus"))}</p>`
    }
  `);

  sections.push(`
    <div class="workspace-section-label">${escapeHTML(t("ui.confirmedPreferences"))}</div>
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
        : `<p class='muted'>${escapeHTML(t("ui.noConfirmedPreferences"))}</p>`
    }
  `);

  sections.push(`
    <div class="workspace-section-label">${escapeHTML(t("ui.recentArtifactRefs"))}</div>
    ${
      recentArtifacts.length
        ? recentArtifacts
            .map(
              (artifact) => `
                <div class="kv">
                  <span>${escapeHTML(artifact.kind || "artifact")}</span>
                  <span>${escapeHTML(artifact.title || artifact.id || t("ui.unnamedResult"))}</span>
                </div>
              `,
            )
            .join("")
        : `<p class='muted'>${escapeHTML(t("ui.noRecentArtifacts"))}</p>`
    }
  `);

  sections.push(`
    <div class="workspace-section-label">${escapeHTML(t("ui.semanticStateChanges"))}</div>
    ${
      stateChanges.length
        ? stateChanges
            .map(
              (change) => `
                <div class="kv">
                  <span>${escapeHTML(change.kind || "change")}</span>
                  <span>${escapeHTML(change.summary || change.object_label || change.artifact_title || change.skill || t("ui.recorded"))}</span>
                </div>
              `,
            )
            .join("")
        : `<p class='muted'>${escapeHTML(t("ui.noStateChanges"))}</p>`
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
          <a class="inline-link" href="${artifactURL}" target="_blank" rel="noreferrer">${escapeHTML(t("ui.open"))}</a>
          <a class="inline-link" href="${artifactURL}" download>${escapeHTML(t("ui.download"))}</a>
        </div>
      </div>
      ${body}
    </section>
  `;
}

function renderDelimitedArtifactPreviewMarkup(artifact, text) {
  const table = parseDelimitedTableBlock(text, artifactDelimiterHint(artifact));
  if (!table) {
    return `<p class='muted'>${escapeHTML(t("ui.cannotPreviewTable"))}</p>`;
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
    const fallback = t("ui.cannotLoadPreview", { message: error.message });
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
        button.textContent = t("ui.retrying");
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
        button.textContent = t("ui.regenerating");
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

function buildWorkspaceFileEntries() {
  const deduped = new Map();

  for (const object of workspaceObjects()) {
    const entry = buildObjectFileEntry(object);
    if (entry) {
      storeWorkspaceFileEntry(deduped, entry);
    }
  }

  for (const artifact of workspaceArtifacts()) {
    const entry = buildArtifactFileEntry(artifact);
    if (entry) {
      storeWorkspaceFileEntry(deduped, entry);
    }
  }

  return Array.from(deduped.values()).sort(compareWorkspaceFileEntries);
}

function buildObjectFileEntry(object) {
  if (!object) {
    return null;
  }
  const location = object.materialized_path || object.materialized_url || "";
  if (!location) {
    return null;
  }

  const parent = findWorkspaceObject(object.parent_id);
  return {
    type: "object",
    resourceKey: `object:${object.id}`,
    fileName: resourceBasename(location) || `${object.label || object.id}.h5ad`,
    label: object.label || object.id,
    metaPrimary: `${formatObjectKind(object.kind)} · ${t("ui.cellCount", { count: object.n_obs })}`,
    metaSecondary: parent
      ? `${formatObjectState(object.state)} · ${t("ui.fromLabel")} ${parent.label}`
      : formatObjectState(object.state),
    createdAt: object.last_accessed_at || object.created_at || "",
    isActiveContext: object.id === appState.focusObjectId,
    locationKey: normalizeResourceLocation(location),
    object,
  };
}

function buildArtifactFileEntry(artifact) {
  if (!artifact) {
    return null;
  }
  const location = artifact.path || artifact.url || artifact.title || "";
  if (!location) {
    return null;
  }

  const linkedObject = findWorkspaceObject(artifact.object_id);
  return {
    type: "artifact",
    resourceKey: `artifact:${artifact.id}`,
    fileName: resourceBasename(location) || artifact.id || "artifact",
    label: artifact.title || resourceBasename(location) || artifact.id || t("ui.unnamedResultLabel"),
    metaPrimary: `${formatArtifactKind(artifact.kind)} · ${linkedObject?.label || artifact.object_id || t("ui.unlinkedObject")}`,
    metaSecondary: artifact.summary || artifact.content_type || t("ui.resultFiles"),
    createdAt: artifact.created_at || "",
    isActiveContext: false,
    locationKey: normalizeResourceLocation(location),
    artifact,
  };
}

function storeWorkspaceFileEntry(entries, entry) {
  const existing = entries.get(entry.locationKey);
  if (!existing) {
    entries.set(entry.locationKey, entry);
    return;
  }
  if (existing.type === "object") {
    return;
  }
  if (entry.type === "object") {
    entries.set(entry.locationKey, entry);
    return;
  }
  if (String(entry.createdAt || "").localeCompare(String(existing.createdAt || "")) > 0) {
    entries.set(entry.locationKey, entry);
  }
}

function compareWorkspaceFileEntries(a, b) {
  if (a.type !== b.type) {
    return a.type === "object" ? -1 : 1;
  }
  if (a.isActiveContext !== b.isActiveContext) {
    return a.isActiveContext ? -1 : 1;
  }
  const createdAtOrder = String(b.createdAt || "").localeCompare(String(a.createdAt || ""));
  if (createdAtOrder !== 0) {
    return createdAtOrder;
  }
  return String(a.fileName || "").localeCompare(String(b.fileName || ""), "zh-CN");
}

function selectedResourceKey() {
  if (appState.selectedResourceKey) {
    return appState.selectedResourceKey;
  }
  if (appState.focusObjectId) {
    return `object:${appState.focusObjectId}`;
  }
  return null;
}

function selectedWorkspaceResource() {
  const desiredKey = selectedResourceKey();
  const entries = buildWorkspaceFileEntries();
  if (desiredKey) {
    const selected = entries.find((entry) => entry.resourceKey === desiredKey);
    if (selected) {
      return selected;
    }
  }
  return entries[0] || null;
}

function workspaceObjects() {
  return appState.workspaceSnapshot?.objects || appState.snapshot?.objects || [];
}

function workspaceArtifacts() {
  return appState.workspaceSnapshot?.artifacts || appState.snapshot?.artifacts || [];
}

function findWorkspaceObject(objectID) {
  if (!objectID) {
    return null;
  }
  return workspaceObjects().find((object) => object.id === objectID) || null;
}

function resourceBasename(value) {
  const normalized = String(value || "").replaceAll("\\", "/");
  const parts = normalized.split("/");
  return parts[parts.length - 1] || normalized;
}

function normalizeResourceLocation(value) {
  return String(value || "").trim().replaceAll("\\", "/");
}

function activeObject() {
  return findWorkspaceObject(appState.focusObjectId);
}
