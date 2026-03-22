const hubState = {
  bundles: [],
  skills: [],
  status: "",
  busyBundles: new Set(),
  openBundles: new Set(),
  selectedBundleID: "",
  selectedSkillName: "",
};

document.addEventListener("DOMContentLoaded", async () => {
  bindHub();
  await refreshHub();
});

function bindHub() {
  const uploadForm = document.getElementById("pluginUploadForm");
  const fileInput = document.getElementById("pluginFileInput");
  const refreshButton = document.getElementById("refreshButton");
  const bundleList = document.getElementById("bundleList");

  uploadForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const file = fileInput.files?.[0];
    if (!file) {
      setStatus("请先选择一个 zip 插件包。", true);
      return;
    }

    const formData = new FormData();
    formData.append("file", file);
    setStatus(`正在安装 ${file.name}...`);
    try {
      const response = await fetchJSON("/api/plugins", {
        method: "POST",
        body: formData,
      });
      hubState.bundles = response.bundles || response.plugins || [];
      hubState.skills = response.skills || [];
      fileInput.value = "";
      reconcileSelection();
      setStatus(`${response.plugin?.name || file.name} 已安装并完成注册。`);
      render();
    } catch (error) {
      setStatus(error.message, true);
    }
  });

  refreshButton.addEventListener("click", async () => {
    setStatus("正在刷新 Skill Hub...");
    await refreshHub();
  });

  bundleList.addEventListener("click", async (event) => {
    const skillButton = event.target.closest("[data-select-skill]");
    if (skillButton) {
      hubState.selectedBundleID = skillButton.getAttribute("data-bundle-id") || "";
      hubState.selectedSkillName = skillButton.getAttribute("data-select-skill") || "";
      render();
      return;
    }

    const actionButton = event.target.closest("[data-bundle-id][data-next-enabled]");
    if (!actionButton) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();

    const bundleID = actionButton.getAttribute("data-bundle-id");
    const enabled = actionButton.getAttribute("data-next-enabled") === "true";
    if (!bundleID || hubState.busyBundles.has(bundleID)) {
      return;
    }

    hubState.busyBundles.add(bundleID);
    render();
    setStatus(`${enabled ? "正在启用" : "正在关闭"} ${bundleID}...`);
    try {
      const response = await fetchJSON(`/api/plugins/${encodeURIComponent(bundleID)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled }),
      });
      hubState.bundles = response.bundles || [];
      hubState.skills = response.skills || [];
      reconcileSelection();
      setStatus(`${response.bundle?.name || bundleID} 已${enabled ? "启用" : "关闭"}。`);
    } catch (error) {
      setStatus(error.message, true);
    } finally {
      hubState.busyBundles.delete(bundleID);
      render();
    }
  });
}

async function refreshHub() {
  try {
    const [pluginsResponse, skillsResponse] = await Promise.all([
      fetchJSON("/api/plugins"),
      fetchJSON("/api/skills"),
    ]);
    hubState.bundles = pluginsResponse.bundles || pluginsResponse.plugins || [];
    hubState.skills = skillsResponse.skills || [];
    reconcileSelection();
    setStatus("Skill Hub 已刷新。");
  } catch (error) {
    setStatus(error.message, true);
  }
  render();
}

function reconcileSelection() {
  const record = findSelectedSkillRecord();
  if (record) {
    hubState.openBundles.add(record.bundle.id);
    return;
  }

  const firstBundle = hubState.bundles.find((bundle) => (bundle.skills || []).length > 0);
  if (!firstBundle) {
    hubState.selectedBundleID = "";
    hubState.selectedSkillName = "";
    return;
  }

  const firstSkill = firstBundle.skills?.[0];
  hubState.selectedBundleID = firstBundle.id;
  hubState.selectedSkillName = firstSkill?.name || "";
  hubState.openBundles.add(firstBundle.id);
}

function render() {
  renderSummary();
  renderBundles();
  renderSkillDetail();
  bindBundleToggles();
}

function renderSummary() {
  const container = document.getElementById("hubSummary");
  const badge = document.getElementById("summaryBadge");
  const bundles = hubState.bundles || [];
  const enabledBundles = bundles.filter((bundle) => bundle.enabled);
  const enabledSkills = bundles
    .filter((bundle) => bundle.enabled)
    .reduce((count, bundle) => count + (bundle.skills || []).length, 0);

  if (badge) {
    badge.textContent = `${enabledBundles.length}/${bundles.length || 0} 已启用`;
  }

  container.innerHTML = `
    ${summaryCard("技能包总数", String(bundles.length))}
    ${summaryCard("启用中的技能包", String(enabledBundles.length))}
    ${summaryCard("外部插件包", String(bundles.filter((bundle) => !bundle.builtin).length))}
    ${summaryCard("已启用技能数", String(enabledSkills))}
  `;
}

function renderBundles() {
  const container = document.getElementById("bundleList");
  if (!hubState.bundles.length) {
    container.innerHTML = "<section class='empty-state'>当前还没有技能包。上传一个 zip 插件包开始扩展。</section>";
    return;
  }

  container.innerHTML = hubState.bundles
    .map((bundle) => {
      const isBusy = hubState.busyBundles.has(bundle.id);
      const actionLabel = bundle.enabled ? "关闭" : "启动";
      const bundleType = bundle.builtin ? "内置技能包" : "外部插件包";
      const isOpen = hubState.openBundles.has(bundle.id);
      return `
        <details class="bundle-card ${bundle.enabled ? "enabled" : "disabled"}" data-bundle-card="${escapeAttribute(bundle.id)}"${isOpen ? " open" : ""}>
          <summary class="bundle-summary">
            <div class="bundle-summary-main">
              <div class="bundle-title-row">
                <h3>${escapeHTML(bundle.name || bundle.id)}</h3>
                <span class="bundle-version">${escapeHTML(bundle.version || bundle.id)}</span>
              </div>
              <div class="pill-row">
                ${statusPill(bundle.enabled ? "ok" : "muted", bundle.enabled ? "已启用" : "已关闭")}
                ${statusPill(bundle.builtin ? "warn" : "muted", bundleType)}
                ${statusPill("muted", `${(bundle.skills || []).length} 个技能`)}
              </div>
            </div>
            <div class="bundle-summary-right">
              <span class="bundle-expand-hint">展开技能</span>
              <span class="bundle-chevron" aria-hidden="true"></span>
            </div>
          </summary>

          <div class="bundle-body">
            <div class="bundle-actions">
              <button
                type="button"
                class="ghost-button"
                data-bundle-id="${escapeAttribute(bundle.id)}"
                data-next-enabled="${bundle.enabled ? "false" : "true"}"
                ${isBusy ? "disabled" : ""}
              >
                ${isBusy ? "处理中..." : `${actionLabel}${bundle.builtin ? "技能包" : "插件"}`}
              </button>
            </div>

            ${
              bundle.description
                ? `<p class="bundle-description">${escapeHTML(bundle.description)}</p>`
                : ""
            }
            ${
              bundle.builtin
                ? `<p class="bundle-note">关闭后，默认的预处理、UMAP、marker 和导出等内置能力都会从规划器与运行时中移除。</p>`
                : ""
            }
            <div class="bundle-meta">
              <span>来源：${escapeHTML(bundle.source_path || "Skill Hub")}</span>
            </div>

            <div class="bundle-skills-head">
              <strong>技能列表</strong>
              <span class="muted">点击某个技能查看详细规范</span>
            </div>
            <div class="skill-button-grid ${bundle.enabled ? "" : "disabled-skills"}">
              ${(bundle.skills || [])
                .map(
                  (skill) => `
                    <button
                      type="button"
                      class="skill-button ${isSelectedSkill(bundle.id, skill.name) ? "selected" : ""}"
                      data-bundle-id="${escapeAttribute(bundle.id)}"
                      data-select-skill="${escapeAttribute(skill.name)}"
                    >
                      <span class="skill-button-name">${escapeHTML(formatSkillName(skill.name))}</span>
                      <span class="skill-button-id">${escapeHTML(skill.name)}</span>
                    </button>
                  `,
                )
                .join("")}
            </div>
          </div>
        </details>
      `;
    })
    .join("");
}

function bindBundleToggles() {
  const detailsList = document.querySelectorAll("[data-bundle-card]");
  for (const details of detailsList) {
    details.addEventListener("toggle", () => {
      const bundleID = details.getAttribute("data-bundle-card");
      if (!bundleID) {
        return;
      }
      if (details.open) {
        hubState.openBundles.add(bundleID);
      } else {
        hubState.openBundles.delete(bundleID);
      }
    });
  }
}

function renderSkillDetail() {
  const container = document.getElementById("skillDetail");
  const record = findSelectedSkillRecord();
  if (!record) {
    container.innerHTML = `
      <section class="detail-empty">
        <strong>还没有选中技能</strong>
        <p>在左侧技能包列表中展开一个 bundle，然后点击任意技能，即可查看该技能的说明、参数规范、输出约定和运行配置。</p>
      </section>
    `;
    return;
  }

  const { bundle, skill } = record;
  const runtimeSpec = renderRuntimeSpec(bundle, skill);
  const inputEntries = Object.entries(skill.input || {});
  const outputEntries = Object.entries(skill.output || {});
  const targetKinds = Array.isArray(skill.target_kinds) ? skill.target_kinds : [];

  container.innerHTML = `
    <article class="detail-card">
      <div class="detail-head">
        <div>
          <div class="pill-row">
            ${statusPill(bundle.enabled ? "ok" : "muted", bundle.enabled ? "所在技能包已启用" : "所在技能包已关闭")}
            ${statusPill(bundle.builtin ? "warn" : "muted", bundle.builtin ? "内置技能" : "插件技能")}
            ${statusPill(skill.support_level === "wired" || !skill.support_level ? "ok" : "muted", skill.support_level || "wired")}
          </div>
          <h4>${escapeHTML(formatSkillName(skill.name))}</h4>
          <p class="detail-subtitle">${escapeHTML(skill.name)}</p>
        </div>
      </div>

      <section class="detail-section">
        <h5>技能说明</h5>
        <p>${escapeHTML(skill.description || "暂无说明。")}</p>
      </section>

      <section class="detail-section detail-meta-grid">
        <div class="meta-card">
          <span class="meta-label">所属技能包</span>
          <strong>${escapeHTML(bundle.name || bundle.id)}</strong>
          <p>${escapeHTML(bundle.id)}</p>
        </div>
        <div class="meta-card">
          <span class="meta-label">适用对象</span>
          <strong>${targetKinds.length ? escapeHTML(targetKinds.join(", ")) : "不限"}</strong>
          <p>规划器会按这些对象类型选择该技能。</p>
        </div>
      </section>

      <section class="detail-section">
        <div class="detail-section-head">
          <h5>输入参数规范</h5>
          <span class="muted">${inputEntries.length} 项</span>
        </div>
        ${renderInputTable(inputEntries)}
      </section>

      <section class="detail-section">
        <div class="detail-section-head">
          <h5>输出约定</h5>
          <span class="muted">${outputEntries.length} 项</span>
        </div>
        ${renderOutputList(outputEntries)}
      </section>

      <section class="detail-section">
        <div class="detail-section-head">
          <h5>实现方式与相关配置</h5>
          <span class="muted">${bundle.builtin ? "内置实现" : "插件入口"}</span>
        </div>
        ${runtimeSpec}
      </section>
    </article>
  `;
}

function renderInputTable(entries) {
  if (!entries.length) {
    return "<p class='muted'>该技能没有额外输入参数，通常只依赖当前目标对象。</p>";
  }
  return `
    <div class="spec-table">
      <div class="spec-row spec-head">
        <span>字段</span>
        <span>类型</span>
        <span>必填</span>
        <span>说明</span>
      </div>
      ${entries
        .map(([name, schema]) => {
          const enumText = Array.isArray(schema.enum) && schema.enum.length
            ? `可选值：${schema.enum.join(", ")}`
            : "";
          const description = [schema.description || "暂无说明", enumText].filter(Boolean).join(" ");
          return `
            <div class="spec-row">
              <span class="spec-key">${escapeHTML(name)}</span>
              <span>${escapeHTML(schema.type || "any")}</span>
              <span>${schema.required ? "是" : "否"}</span>
              <span>${escapeHTML(description)}</span>
            </div>
          `;
        })
        .join("")}
    </div>
  `;
}

function renderOutputList(entries) {
  if (!entries.length) {
    return "<p class='muted'>该技能没有显式声明输出字段。</p>";
  }
  return `
    <div class="output-list">
      ${entries
        .map(
          ([name, value]) => `
            <div class="output-item">
              <strong>${escapeHTML(name)}</strong>
              <span>${escapeHTML(String(value || ""))}</span>
            </div>
          `,
        )
        .join("")}
    </div>
  `;
}

function renderRuntimeSpec(bundle, skill) {
  if (skill.runtime && Object.keys(skill.runtime).length > 0) {
    return `
      <div class="runtime-note">
        <p>该技能通过以下运行配置接入执行器。你可以重点查看入口脚本、调用函数和运行类型。</p>
      </div>
      <pre class="spec-code"><code>${escapeHTML(JSON.stringify(skill.runtime, null, 2))}</code></pre>
    `;
  }

  if (bundle.builtin) {
    return `
      <div class="runtime-note">
        <p>这是内置技能。当前没有单独暴露 runtime 配置，实际执行由系统内置的规划器和 Python runtime 分发逻辑实现。</p>
        <p>如果要了解实现规范，优先关注它的输入参数、输出约定、适用对象和 support level。</p>
      </div>
    `;
  }

  return "<p class='muted'>当前没有可展示的 runtime 配置。</p>";
}

function findSelectedSkillRecord() {
  if (!hubState.selectedSkillName) {
    return null;
  }
  for (const bundle of hubState.bundles) {
    if (hubState.selectedBundleID && bundle.id !== hubState.selectedBundleID) {
      continue;
    }
    for (const skill of bundle.skills || []) {
      if (skill.name === hubState.selectedSkillName) {
        return { bundle, skill };
      }
    }
  }
  return null;
}

function isSelectedSkill(bundleID, skillName) {
  return hubState.selectedBundleID === bundleID && hubState.selectedSkillName === skillName;
}

function summaryCard(label, value) {
  return `
    <div class="summary-card">
      <strong>${escapeHTML(value)}</strong>
      <span>${escapeHTML(label)}</span>
    </div>
  `;
}

function setStatus(message, isError = false) {
  hubState.status = message;
  const status = document.getElementById("hubStatus");
  if (!status) {
    return;
  }
  status.textContent = message;
  status.className = `status-text ${isError ? "error" : "muted"}`;
}

function statusPill(kind, label) {
  return `<span class="status-pill ${kind}">${escapeHTML(label)}</span>`;
}

function formatSkillName(name) {
  const labels = {
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
    plot_gene_umap: "绘制基因 UMAP",
    plot_dotplot: "绘制点图",
    plot_violin: "绘制小提琴图",
    run_python_analysis: "执行自定义 Python 分析",
    export_h5ad: "导出 h5ad",
  };
  return labels[name] || name;
}

async function fetchJSON(url, options) {
  const response = await fetch(url, options);
  if (!response.ok) {
    let message = response.statusText;
    try {
      const payload = await response.json();
      message = payload.error || JSON.stringify(payload);
    } catch {
      message = await response.text();
    }
    throw new Error(message || `请求失败：${response.status}`);
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
