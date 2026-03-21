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

const quickActionPrompts = [
  "Inspect dataset",
  "Plot UMAP",
  "Subset cortex cells",
  "Recluster active object",
  "Find markers",
  "Export h5ad",
];

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
    body: JSON.stringify({ label: "Arabidopsis atlas session" }),
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
      status.textContent = "Select a .h5ad file first.";
      return;
    }

    const formData = new FormData();
    formData.append("file", file);
    status.textContent = `Uploading ${file.name}...`;

    try {
      const response = await fetchJSON(`/api/sessions/${appState.sessionId}/upload`, {
        method: "POST",
        body: formData,
      });
      appState.snapshot = response.snapshot;
      appState.activeObjectId = response.object.id;
      status.textContent = `${file.name} attached as ${response.object.label}.`;
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
      status.textContent = "Session not ready.";
      return;
    }

    const message = input.value.trim();
    if (!message) {
      status.textContent = "Enter a message first.";
      return;
    }

    status.textContent = "Building planner preview...";
    try {
      appState.plannerPreview = await fetchJSON(
        `/api/sessions/${appState.sessionId}/planner-preview`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ message }),
        },
      );
      status.textContent = `Preview ready for ${appState.plannerPreview.planner_mode} planner.`;
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
  for (const prompt of quickActionPrompts) {
    const button = document.createElement("button");
    button.className = "chip";
    button.type = "button";
    button.textContent = prompt;
    button.addEventListener("click", () => {
      const input = document.getElementById("messageInput");
      input.value = prompt;
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

function renderSystemStatus() {
  const container = document.getElementById("systemStatus");
  const status = appState.systemStatus;
  if (!status) {
    container.innerHTML = "<p class='muted'>System status unavailable.</p>";
    return;
  }

  const pills = [
    statusPill(status.system_mode === "live" ? "ok" : "warn", `Mode: ${status.system_mode || "unknown"}`),
    statusPill(status.planner_mode === "llm" ? "ok" : "warn", `Planner: ${status.planner_mode || "unknown"}`),
    statusPill(status.llm_loaded ? "ok" : "muted", `LLM: ${status.llm_loaded ? "loaded" : "not loaded"}`),
    statusPill(status.runtime_connected ? "ok" : "bad", `Runtime: ${status.runtime_connected ? "connected" : "offline"}`),
    statusPill(status.real_h5ad_inspection ? "ok" : "muted", `h5ad inspect: ${status.real_h5ad_inspection ? "real" : "mock"}`),
    statusPill(status.real_analysis_execution ? "ok" : "warn", `Analysis: ${status.real_analysis_execution ? "real" : "mock"}`),
  ];

  const runtime = status.runtime || {};
  const environmentChecks = runtime.environment_checks || [];
  const sampleH5AD = runtime.sample_h5ad;
  const notes = status.notes || [];

  const environmentSection = environmentChecks.length
    ? `
      <div class="status-section">
        <div class="status-section-head">
          <strong>Environment Checks</strong>
          <span class="muted">Python ${escapeHTML(runtime.python_version || "unknown")}</span>
        </div>
        <div class="environment-check-grid">
          ${environmentChecks
            .map(
              (check) => `
                <section class="environment-check ${check.ok ? "ok" : "bad"}">
                  <div class="environment-check-head">
                    <strong>${escapeHTML(check.name)}</strong>
                    ${statusPill(check.ok ? "ok" : "bad", check.ok ? "OK" : "FAIL")}
                  </div>
                  <p class="muted">${escapeHTML(check.detail || (check.ok ? "Ready" : "Unavailable"))}</p>
                </section>
              `,
            )
            .join("")}
        </div>
      </div>
    `
    : "";

  const sampleSection = sampleH5AD
    ? `
      <div class="status-section">
        <div class="status-section-head">
          <strong>Sample h5ad</strong>
          <span class="muted">${escapeHTML(sampleH5AD.path || "")}</span>
        </div>
        <div class="status-detail-grid">
          <div class="kv"><span>Cells</span><span>${sampleH5AD.n_obs ?? "unknown"}</span></div>
          <div class="kv"><span>Genes</span><span>${sampleH5AD.n_vars ?? "unknown"}</span></div>
          <div class="kv"><span>Obs Fields</span><span>${escapeHTML(formatList(sampleH5AD.obs_fields))}</span></div>
          <div class="kv"><span>Embeddings</span><span>${escapeHTML(formatList(sampleH5AD.obsm_keys))}</span></div>
        </div>
      </div>
    `
    : "";

  container.innerHTML = `
    <section class="status-card">
      <div class="status-card-head">
        <strong>System Status</strong>
        <span class="muted">${escapeHTML(status.summary || "")}</span>
      </div>
      <div class="status-pills">${pills.join("")}</div>
      <div class="status-detail-grid">
        <div class="kv"><span>Runtime Mode</span><span>${escapeHTML(status.runtime_mode || "unknown")}</span></div>
        <div class="kv"><span>Executable Skills</span><span>${escapeHTML(formatList(status.executable_skills))}</span></div>
      </div>
      ${environmentSection}
      ${sampleSection}
      ${
        notes.length
          ? `<div class="status-notes">${notes.map((note) => `<p class="muted">${escapeHTML(note)}</p>`).join("")}</div>`
          : ""
      }
    </section>
  `;
}

function renderSessionMeta() {
  const meta = document.getElementById("sessionMeta");
  if (!appState.snapshot) {
    meta.innerHTML = "<p class='muted'>No session loaded.</p>";
    return;
  }
  const { session, objects, jobs, artifacts } = appState.snapshot;
  meta.innerHTML = `
    <div class="kv"><span>Session</span><span>${session.id}</span></div>
    <div class="kv"><span>Dataset</span><span>${session.dataset_id}</span></div>
    <div class="kv"><span>Objects</span><span>${objects.length}</span></div>
    <div class="kv"><span>Jobs</span><span>${jobs.length}</span></div>
    <div class="kv"><span>Artifacts</span><span>${artifacts.length}</span></div>
  `;
}

function renderObjectTree() {
  const container = document.getElementById("objectTree");
  container.innerHTML = "";

  if (!appState.snapshot?.objects?.length) {
    container.innerHTML = "<p class='muted'>No objects registered.</p>";
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
        <span class="label">${object.label}</span>
        <span class="meta">${object.kind} · ${object.n_obs} cells · ${object.state}</span>
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
  node.querySelector(".message-role").textContent = message.role;
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
        <strong>Job Status</strong>
        ${statusPill(job.status === "running" ? "warn" : "muted", job.status)}
      </div>
      <p class="message-job-summary">${escapeHTML(job.summary || "Request accepted. Waiting for planner/runtime updates.")}</p>
      ${
        job.steps?.length
          ? `<div class="message-step-list">
              ${job.steps
                .map(
                  (step) => `
                    <div class="message-step-row">
                      <span>${escapeHTML(formatSkillName(step.skill))}</span>
                      <span>${escapeHTML(step.summary || step.status)}</span>
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
        <strong>${job.status === "failed" ? "Job Result" : "Analysis Result"}</strong>
        ${statusPill(statusKindForJob(job.status), job.status)}
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
                        ${statusPill(statusKindForJob(step.status), step.status)}
                      </div>
                      <p class="muted">${escapeHTML(step.summary || "No summary returned.")}</p>
                      ${
                        step.output_object_id
                          ? `<div class="message-step-meta">Output object: ${escapeHTML(objectLabel(step.output_object_id))}</div>`
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
                <strong>Artifacts</strong>
                <span class="muted">${artifactCards.length} item${artifactCards.length > 1 ? "s" : ""}</span>
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
    container.innerHTML = "<p class='muted'>Select an object to inspect.</p>";
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

  container.innerHTML = `
    <section class="inspector-card">
      <h3>${object.label}</h3>
      <div class="kv"><span>Object ID</span><span>${object.id}</span></div>
      <div class="kv"><span>Kind</span><span>${object.kind}</span></div>
      <div class="kv"><span>Parent</span><span>${object.parent_id || "none"}</span></div>
      <div class="kv"><span>Backend</span><span>${object.backend_ref}</span></div>
      <div class="kv"><span>Cells</span><span>${object.n_obs}</span></div>
      <div class="kv"><span>Genes</span><span>${object.n_vars}</span></div>
      <div class="kv"><span>Residency</span><span>${object.state}</span></div>
      <div class="kv"><span>Materialized</span><span>${object.materialized_path || "not yet"}</span></div>
      <div class="kv"><span>Download</span><span>${
        object.materialized_url
          ? `<a class="inline-link" href="${object.materialized_url}" download>Get h5ad</a>`
          : "not available"
      }</span></div>
    </section>
    <section class="inspector-card">
      <h3>Dataset Assessment</h3>
      <div class="kv"><span>Status</span><span>${escapeHTML(assessment.preprocessing_state || "unknown")}</span></div>
      <div class="kv"><span>Layers</span><span>${escapeHTML(formatList(metadata.layer_keys))}</span></div>
      <div class="kv"><span>Obs Fields</span><span>${escapeHTML(formatList(metadata.obs_fields))}</span></div>
      <div class="kv"><span>Var Fields</span><span>${escapeHTML(formatList(metadata.var_fields))}</span></div>
      <div class="kv"><span>Embeddings</span><span>${escapeHTML(formatList(metadata.obsm_keys))}</span></div>
      <div class="kv"><span>Uns Keys</span><span>${escapeHTML(formatList(metadata.uns_keys))}</span></div>
      <div class="kv"><span>Cell Type</span><span>${escapeHTML(formatAnnotation(cellType))}</span></div>
      <div class="kv"><span>Cluster</span><span>${escapeHTML(formatAnnotation(cluster))}</span></div>
      <div class="kv"><span>Available</span><span>${escapeHTML(formatList(assessment.available_analyses))}</span></div>
      <div class="kv"><span>Missing</span><span>${escapeHTML(formatList(assessment.missing_requirements))}</span></div>
      <div class="kv"><span>Next Steps</span><span>${escapeHTML(formatList(assessment.suggested_next_steps))}</span></div>
    </section>
    <section class="inspector-card">
      <h3>Annotation Candidates</h3>
      ${
        categorical.length
          ? categorical
              .slice(0, 8)
              .map(
                (item) => `
                  <div class="kv">
                    <span>${escapeHTML(item.field)}</span>
                    <span>${escapeHTML(`${item.role || "annotation"} · ${item.n_categories} groups · ${(item.sample_values || []).join(", ")}`)}</span>
                  </div>
                `,
              )
              .join("")
          : "<p class='muted'>No categorical obs fields detected yet.</p>"
      }
    </section>
    <section class="inspector-card">
      <h3>Recent Jobs</h3>
      ${
        relatedJobs.length
          ? relatedJobs
              .slice(-3)
              .reverse()
              .map(
                (job) => `
                  <div class="kv"><span>${job.status}</span><span>${job.summary || "Waiting..."}</span></div>
                `,
              )
              .join("")
          : "<p class='muted'>No jobs linked to this object yet.</p>"
      }
    </section>
  `;
}

function renderPlannerPreview() {
  const container = document.getElementById("plannerPreview");
  const preview = appState.plannerPreview;
  if (!preview) {
    container.innerHTML = "";
    return;
  }

  const blocks = [];
  blocks.push(`
    <section class="inspector-card">
      <h3>Planner Preview</h3>
      <div class="kv"><span>Mode</span><span>${escapeHTML(preview.planner_mode || "")}</span></div>
      <div class="kv"><span>Active</span><span>${escapeHTML(preview.planning_request?.active_object?.label || "none")}</span></div>
      <div class="kv"><span>Note</span><span>${escapeHTML(preview.note || "")}</span></div>
    </section>
  `);

  blocks.push(`
    <section class="inspector-card">
      <h3>Planning Request</h3>
      <pre>${escapeHTML(JSON.stringify(preview.planning_request, null, 2))}</pre>
    </section>
  `);

  if (preview.developer_instructions) {
    blocks.push(`
      <section class="inspector-card">
        <h3>Developer Instructions</h3>
        <pre>${escapeHTML(preview.developer_instructions)}</pre>
      </section>
    `);
  }

  if (preview.request_body) {
    blocks.push(`
      <section class="inspector-card">
        <h3>Planner Request Body</h3>
        <pre>${escapeHTML(JSON.stringify(preview.request_body, null, 2))}</pre>
      </section>
    `);
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
          <a class="inline-link" href="${artifact.url}" target="_blank" rel="noreferrer">Open</a>
          <a class="inline-link" href="${artifact.url}" download>Download</a>
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
    const fallback = `Unable to load preview: ${error.message}`;
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

function activeObject() {
  return (appState.snapshot?.objects || []).find((object) => object.id === appState.activeObjectId);
}

async function fetchJSON(url, options) {
  const response = await fetch(url, options);
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || `Request failed: ${response.status}`);
  }
  return response.json();
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

function formatList(values) {
  if (!values || !values.length) {
    return "none";
  }
  return values.join(", ");
}

function objectLabel(objectId) {
  const object = (appState.snapshot?.objects || []).find((item) => item.id === objectId);
  return object ? object.label : objectId;
}

function formatSkillName(skill) {
  return String(skill || "")
    .split("_")
    .filter(Boolean)
    .map((part) => part[0].toUpperCase() + part.slice(1))
    .join(" ");
}

function formatAnnotation(annotation) {
  if (!annotation) {
    return "not detected";
  }
  const sample = (annotation.sample_values || []).slice(0, 4).join(", ");
  return `${annotation.field} · ${annotation.n_categories} groups${sample ? ` · ${sample}` : ""}`;
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

  titleNode.textContent = title || "Artifact Preview";
  image.src = url;
  image.alt = title || "Artifact Preview";
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
