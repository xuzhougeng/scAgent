async function startApp() {
  const [
    { fetchJSON },
    { formatConversationLabel, formatPlannerMode },
    { bindSidebarResize },
    { bindImageModal, bindStatusOverviewModal },
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
  });

  bindSidebarResize();
  bindComposer();
  bindUpload();
  bindPlannerPreview();
  bindImageModal();
  bindStatusOverviewModal();
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

    const optimisticMessage = appState.snapshot
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
      render();
    }

    try {
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
      render();
    }
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
}

void startApp().catch((error) => {
  console.error("Failed to bootstrap scAgent web app", error);
});
