async function startApp() {
  const [
    { fetchJSON },
    { formatConversationLabel, formatPlannerMode },
    { bindSidebarResize },
    {
      bindConfirmModal,
      bindImageModal,
      bindPlannerPreviewModal,
      bindStatusOverviewModal,
      bindWorkspaceFilesModal,
      openPlannerPreviewModal,
    },
    { appState, storageKeys },
    {
      configureRenderActions,
      render,
      renderPlannerPreview,
      renderQuickActions,
      renderSessionMeta,
    },
  ] = await Promise.all([
    import("/js/api.mjs"),
    import("/js/format.mjs"),
    import("/js/layout.mjs"),
    import("/js/modals.mjs"),
    import("/js/state.mjs"),
    import("/js/render.mjs"),
  ]);

  configureRenderActions({
    createConversation,
    createWorkspace,
    switchConversation,
    switchWorkspace,
    editJobRequest,
    retryJob,
    regenerateResponse,
    renameWorkspace,
    renameConversation,
    deleteWorkspace,
    deleteConversation,
  });

  bindSidebarResize();
  bindComposer();
  bindUpload();
  bindPlannerPreview();
  bindConfirmModal();
  bindImageModal();
  bindPlannerPreviewModal();
  bindStatusOverviewModal();
  bindWorkspaceFilesModal();
  renderQuickActions();
  await bootstrap();

  async function bootstrap() {
    await refreshCapabilityState();
    const restored = await restoreContext();
    if (!restored) {
      const snapshot = await createWorkspaceWithLabel("PBMC3K 测试会话");
      syncSnapshot(snapshot);
      await refreshWorkspaceSnapshot();
    }
    connectEvents();
    renderApp();
  }

  function renderApp() {
    render();
    renderComposerMode();
    updateComposerControls();
  }

  function currentActiveJob() {
    const jobs = appState.snapshot?.jobs || [];
    for (let index = jobs.length - 1; index >= 0; index -= 1) {
      const job = jobs[index];
      if (!job) {
        continue;
      }
      if (job.status === "queued" || job.status === "running") {
        return job;
      }
    }
    return null;
  }

  function clearComposerEditState() {
    appState.composerEditJobId = null;
    appState.composerEditOriginalMessage = "";
  }

  function updateComposerControls() {
    const button = document.getElementById("composerSubmitButton");
    if (!button) {
      return;
    }

    const activeJob = currentActiveJob();
    const isStopping =
      activeJob &&
      (appState.cancelPendingJobId === activeJob.id ||
        activeJob.summary === "正在停止当前任务...");
    const isSubmitting = Boolean(appState.composerPending);
    const isEditing = Boolean(appState.composerEditJobId);

    button.classList.toggle("is-stop", Boolean(activeJob));
    if (activeJob) {
      button.textContent = isStopping ? "停止中..." : "停止";
      button.disabled = Boolean(isStopping);
      return;
    }

    button.textContent = isSubmitting ? (isEditing ? "重发中..." : "提交中...") : isEditing ? "重发" : "运行";
    button.disabled = isSubmitting || !appState.sessionId;
  }

  function renderComposerMode() {
    const container = document.getElementById("composerModeBar");
    if (!container) {
      return;
    }

    if (!appState.composerEditJobId) {
      container.classList.add("hidden");
      container.innerHTML = "";
      return;
    }

    container.classList.remove("hidden");
    container.innerHTML = `
      <div class="composer-mode-copy">
        <strong>编辑并重发</strong>
        <p class="muted">修改后点击“重发”。提交成功后，这条旧请求及其结果会被替换。</p>
      </div>
      <button id="composerModeCancelButton" type="button">取消</button>
    `;

    container.querySelector("#composerModeCancelButton")?.addEventListener("click", () => {
      clearComposerEditState();
      appState.workspaceStatus = "已取消编辑并重发。";
      renderApp();
    });
  }

  function syncSnapshot(snapshot) {
    const previousWorkspaceId = appState.workspaceId;
    const previousFocusObjectId = appState.focusObjectId;
    appState.snapshot = snapshot;
    appState.sessionId = snapshot?.session?.id || null;
    appState.workspaceId = snapshot?.workspace?.id || snapshot?.session?.workspace_id || null;
    appState.focusObjectId = snapshot?.session?.focus_object_id || null;
    const previousDefaultResourceKey = previousFocusObjectId ? `object:${previousFocusObjectId}` : null;
    if (previousWorkspaceId && appState.workspaceId !== previousWorkspaceId) {
      appState.selectedResourceKey = null;
    }
    if (!appState.selectedResourceKey || appState.selectedResourceKey === previousDefaultResourceKey) {
      appState.selectedResourceKey = appState.focusObjectId ? `object:${appState.focusObjectId}` : null;
    }
    if (
      appState.composerEditJobId &&
      !snapshot?.jobs?.some((job) => job?.id === appState.composerEditJobId)
    ) {
      clearComposerEditState();
    }
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

  async function createWorkspaceWithLabel(label, { withSample = true } = {}) {
    return fetchJSON("/api/workspaces", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label, with_sample: withSample }),
    });
  }

  function defaultWorkspaceLabel() {
    return `分析工作区 ${(appState.workspaceList?.length || 0) + 1}`;
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

  async function activateConversation(sessionId) {
    const snapshot = await fetchJSON(`/api/sessions/${sessionId}`);
    appState.plannerPreview = null;
    syncSnapshot(snapshot);
    connectEvents();
    return snapshot;
  }

  async function activateWorkspaceSnapshot(workspaceSnapshot, preferredSessionId = "") {
    const conversation = preferredConversation(workspaceSnapshot, preferredSessionId);
    if (!conversation) {
      throw new Error("目标 workspace 暂无可用对话。");
    }
    appState.workspaceSnapshot = workspaceSnapshot;
    const snapshot = await activateConversation(conversation.id);
    return { snapshot, conversation };
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
      clearComposerEditState();
      const snapshot = await activateConversation(sessionId);
      await Promise.all([refreshWorkspaceSnapshot(), refreshWorkspaceList()]);
      appState.workspaceStatus = `已切换到 ${formatConversationLabel(snapshot.session)}。`;
      renderApp();
    } catch (error) {
      appState.workspaceStatus = error.message;
      renderSessionMeta();
      updateComposerControls();
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
      clearComposerEditState();
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
      renderApp();
    } catch (error) {
      appState.workspaceStatus = error.message;
      renderSessionMeta();
      updateComposerControls();
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
      clearComposerEditState();
      const { snapshot } = await activateWorkspaceSnapshot(workspaceSnapshot);
      await refreshWorkspaceList();
      appState.workspaceStatus = `已切换到 ${workspaceSnapshot.workspace?.label || workspaceId}。`;
      renderApp();
    } catch (error) {
      appState.workspaceStatus = error.message;
      renderSessionMeta();
      updateComposerControls();
    }
  }

  async function createWorkspace() {
    appState.workspaceStatus = "正在创建 workspace...";
    renderSessionMeta();

    try {
      clearComposerEditState();
      const snapshot = await createWorkspaceWithLabel(defaultWorkspaceLabel(), { withSample: false });
      appState.plannerPreview = null;
      syncSnapshot(snapshot);
      await Promise.all([refreshWorkspaceSnapshot(), refreshWorkspaceList()]);
      appState.workspaceStatus = `已创建 ${snapshot.workspace?.label || "新 workspace"}。`;
      connectEvents();
      renderApp();
    } catch (error) {
      appState.workspaceStatus = error.message;
      renderSessionMeta();
      updateComposerControls();
    }
  }

  async function editJobRequest(jobId) {
    if (!jobId) {
      return;
    }
    if (currentActiveJob()) {
      appState.workspaceStatus = "请先等待当前任务完成，或先停止当前任务。";
      renderApp();
      return;
    }

    const job = (appState.snapshot?.jobs || []).find((item) => item.id === jobId);
    if (!job) {
      appState.workspaceStatus = "未找到原始任务。";
      renderApp();
      return;
    }

    const message = (appState.snapshot?.messages || []).find(
      (item) => item.id === job.message_id && item.role === "user",
    );
    if (!message) {
      appState.workspaceStatus = "未找到原始消息。";
      renderApp();
      return;
    }

    const input = document.getElementById("messageInput");
    appState.composerEditJobId = job.id;
    appState.composerEditOriginalMessage = message.content || "";
    appState.plannerPreview = null;
    input.value = message.content || "";
    input.focus();
    input.setSelectionRange(input.value.length, input.value.length);
    appState.workspaceStatus = "已载入原请求，请修改后点击“重发”。";
    renderApp();
  }

  async function retryJob(jobId) {
    try {
      clearComposerEditState();
      const response = await fetchJSON(`/api/jobs/${jobId}/retry`, {
        method: "POST",
      });
      syncSnapshot(response.snapshot);
      renderApp();
    } catch (error) {
      appState.workspaceStatus = error.message;
      renderApp();
    }
  }

  async function renameWorkspace(workspaceId, label) {
    try {
      await fetchJSON(`/api/workspaces/${workspaceId}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ label }),
      });
      await Promise.all([refreshWorkspaceSnapshot(), refreshWorkspaceList()]);
      appState.workspaceStatus = `已将工作区重命名为 ${label}。`;
      renderApp();
    } catch (error) {
      appState.workspaceStatus = error.message;
      renderApp();
    }
  }

  async function renameConversation(sessionId, label) {
    try {
      const snapshot = await fetchJSON(`/api/sessions/${sessionId}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ label }),
      });
      syncSnapshot(snapshot);
      await refreshWorkspaceSnapshot();
      appState.workspaceStatus = `已将对话重命名为 ${label}。`;
      renderApp();
    } catch (error) {
      appState.workspaceStatus = error.message;
      renderApp();
    }
  }

  async function deleteConversation(sessionId) {
    if (!sessionId) {
      return;
    }

    const isCurrentConversation = sessionId === appState.sessionId;
    if (isCurrentConversation && currentActiveJob()) {
      appState.workspaceStatus = "请先停止当前任务，再删除当前对话。";
      renderApp();
      return;
    }

    const conversation = (appState.workspaceSnapshot?.conversations || []).find(
      (item) => item.id === sessionId,
    );
    const fallbackConversation = isCurrentConversation
      ? [...(appState.workspaceSnapshot?.conversations || [])]
          .filter((item) => item.id !== sessionId)
          .sort(compareConversationCreatedAt)[0] || null
      : null;

    appState.workspaceStatus = "正在删除对话...";
    renderSessionMeta();

    try {
      await fetchJSON(`/api/sessions/${sessionId}`, {
        method: "DELETE",
      });
      if (isCurrentConversation) {
        clearComposerEditState();
        appState.plannerPreview = null;
      }
      await Promise.all([refreshWorkspaceSnapshot(), refreshWorkspaceList()]);
      if (isCurrentConversation) {
        if (!fallbackConversation?.id) {
          throw new Error("删除后未找到可切换的对话。");
        }
        await activateConversation(fallbackConversation.id);
        await refreshWorkspaceSnapshot();
      }
      appState.workspaceStatus = `已删除 ${formatConversationLabel(conversation || { id: sessionId })}。`;
      renderApp();
    } catch (error) {
      appState.workspaceStatus = error.message;
      renderApp();
    }
  }

  async function deleteWorkspace(workspaceId) {
    if (!workspaceId) {
      return;
    }

    const isCurrentWorkspace = workspaceId === appState.workspaceId;
    if (isCurrentWorkspace && currentActiveJob()) {
      appState.workspaceStatus = "请先停止当前任务，再删除当前工作区。";
      renderApp();
      return;
    }

    const workspace =
      (appState.workspaceList || []).find((item) => item.id === workspaceId) ||
      (appState.workspaceSnapshot?.workspace?.id === workspaceId ? appState.workspaceSnapshot.workspace : null) ||
      (appState.snapshot?.workspace?.id === workspaceId ? appState.snapshot.workspace : null);

    appState.workspaceStatus = "正在删除工作区...";
    renderSessionMeta();

    try {
      await fetchJSON(`/api/workspaces/${workspaceId}`, {
        method: "DELETE",
      });

      if (isCurrentWorkspace) {
        clearComposerEditState();
        appState.plannerPreview = null;
      }

      await refreshWorkspaceList();

      if (isCurrentWorkspace) {
        const nextWorkspace = appState.workspaceList[0] || null;
        if (nextWorkspace?.id) {
          const workspaceSnapshot = await fetchJSON(`/api/workspaces/${nextWorkspace.id}`);
          await activateWorkspaceSnapshot(workspaceSnapshot);
        } else {
          const snapshot = await createWorkspaceWithLabel(defaultWorkspaceLabel(), { withSample: false });
          syncSnapshot(snapshot);
          await Promise.all([refreshWorkspaceSnapshot(), refreshWorkspaceList()]);
          connectEvents();
        }
      }

      appState.workspaceStatus = `已删除工作区 ${workspace?.label || workspaceId}。`;
      renderApp();
    } catch (error) {
      appState.workspaceStatus = error.message;
      renderApp();
    }
  }

  async function regenerateResponse(jobId) {
    try {
      clearComposerEditState();
      const response = await fetchJSON(`/api/jobs/${jobId}/regenerate`, {
        method: "POST",
      });
      syncSnapshot(response.snapshot);
      renderApp();
    } catch (error) {
      appState.workspaceStatus = error.message;
      renderApp();
    }
  }

  async function cancelActiveJob(job) {
    if (!job?.id) {
      return;
    }

    appState.cancelPendingJobId = job.id;
    updateComposerControls();

    try {
      const response = await fetchJSON(`/api/jobs/${job.id}/cancel`, {
        method: "POST",
      });
      syncSnapshot(response.snapshot);
      appState.workspaceStatus = "已发送停止请求。";
      renderApp();
    } catch (error) {
      appState.workspaceStatus = error.message;
      renderApp();
    } finally {
      appState.cancelPendingJobId = null;
      updateComposerControls();
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
    const activeJob = currentActiveJob();
    if (activeJob) {
      if (
        appState.cancelPendingJobId === activeJob.id ||
        activeJob.summary === "正在停止当前任务..."
      ) {
        return;
      }
      await cancelActiveJob(activeJob);
      return;
    }

    const input = document.getElementById("messageInput");
    const message = input.value.trim();
    const editJobId = appState.composerEditJobId;
    const isEditRetry = Boolean(editJobId);
    if (!message || !appState.sessionId || appState.composerPending) {
      return;
    }

    appState.composerPending = true;
    updateComposerControls();

    const optimisticMessage = !isEditRetry && appState.snapshot
      ? {
          id: `local_user_${Date.now()}`,
          session_id: appState.sessionId,
          role: "user",
          content: message,
          created_at: new Date().toISOString(),
          local_status: "sending",
        }
      : null;

    input.value = "";
    if (optimisticMessage) {
      appState.snapshot = {
        ...appState.snapshot,
        messages: [...(appState.snapshot.messages || []), optimisticMessage],
      };
      renderApp();
    }

    try {
      const response = isEditRetry
        ? await fetchJSON(`/api/jobs/${editJobId}/retry`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ message }),
          })
        : await fetchJSON("/api/messages", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              session_id: appState.sessionId,
              message,
            }),
          });
      if (isEditRetry) {
        clearComposerEditState();
        appState.workspaceStatus = "已重发编辑后的请求。";
      }
      syncSnapshot(response.snapshot);
      renderApp();
    } catch (error) {
      if (optimisticMessage && appState.snapshot) {
        appState.snapshot = {
          ...appState.snapshot,
          messages: (appState.snapshot.messages || []).filter((item) => item.id !== optimisticMessage.id),
        };
      }
      input.value = message;
      input.focus();
      appState.workspaceStatus = error.message;
      renderApp();
    } finally {
      appState.composerPending = false;
      updateComposerControls();
    }
  }

  function bindUpload() {
    const form = document.getElementById("uploadForm");
    const input = document.getElementById("fileInput");
    const status = document.getElementById("uploadStatus");
    const fileNameEl = document.getElementById("fileName");

    async function doUpload() {
      const file = input.files?.[0];
      if (!file || !appState.sessionId) {
        status.textContent = "请先选择一个 .h5ad 文件。";
        return;
      }

      fileNameEl.textContent = file.name;
      fileNameEl.classList.add("has-file");
      status.textContent = `正在上传 ${file.name}...`;

      try {
        const formData = new FormData();
        formData.append("file", file);
        const response = await fetchJSON(`/api/sessions/${appState.sessionId}/upload`, {
          method: "POST",
          body: formData,
        });
        syncSnapshot(response.snapshot);
        appState.focusObjectId = response.object.id;
        appState.selectedResourceKey = `object:${response.object.id}`;
        status.textContent = `${file.name} 已作为 ${response.object.label} 附加到当前 workspace。`;
        input.value = "";
        fileNameEl.textContent = "选择文件后将自动上传";
        fileNameEl.classList.remove("has-file");
        renderApp();
      } catch (error) {
        status.textContent = error.message;
        fileNameEl.textContent = "上传失败，请重试";
        fileNameEl.classList.remove("has-file");
        input.value = "";
      }
    }

    input.addEventListener("change", () => {
      if (input.files?.length) {
        doUpload();
      }
    });

    form.addEventListener("submit", (event) => {
      event.preventDefault();
      doUpload();
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
        openPlannerPreviewModal();
      } catch (error) {
        status.textContent = error.message;
      }
    });
  }

  function connectEvents() {
    if (appState.eventSource) {
      appState.eventSource.close();
    }
    appState.eventSource = new EventSource(`/api/sessions/${appState.sessionId}/events`);
    appState.eventSource.addEventListener("session_updated", (event) => {
      syncSnapshot(JSON.parse(event.data));
      renderApp();
    });
  }
}

void startApp().catch((error) => {
  console.error("Failed to bootstrap scAgent web app", error);
});
